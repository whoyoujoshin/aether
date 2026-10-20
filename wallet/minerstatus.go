package wallet

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"sync"

	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/client/grpc/cmtservice"
	sdk "github.com/cosmos/cosmos-sdk/types"
	grpctypes "github.com/cosmos/cosmos-sdk/types/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/whoyoujoshin/aether/x/pow"
)

// A miner's standing in the current epoch in one call, instead of an
// agent stitching together half a dozen x/pow queries (or scraping
// powminer's logs). Every query is pinned to the same block height, so
// every field describes the same moment.

// SelectionRule names how the epoch's last block picks the next validator set.
const (
	SelectionTopKByWork       = "top_k_by_work"
	SelectionBeaconWeighted   = "beacon_weighted_sample"
	SelectionSeatRule         = "seat_work_power"
	blockTimeSampleBlocks     = 1000
	eligibilityLookupParallel = 8
)

// Why an address can't be picked this epoch.
const (
	IneligibleNoWork         = "no_work_this_epoch"
	IneligibleNoConsensusKey = "no_consensus_key"
	IneligibleBanned         = "banned"
)

type EpochStatus struct {
	Index  int64 `json:"index"`
	Length int64 `json:"length"`
	// StartHeight is the epoch's first block. SelectionHeight is its
	// last: that block's EndBlock picks the next validator set from this
	// epoch's work, and CometBFT applies it two blocks later.
	StartHeight          int64 `json:"startHeight"`
	SelectionHeight      int64 `json:"selectionHeight"`
	BlocksUntilSelection int64 `json:"blocksUntilSelection"`
	// Zero when the block time couldn't be measured (a chain under 2 blocks).
	AvgBlockSeconds          float64 `json:"avgBlockSeconds"`
	EstSecondsUntilSelection int64   `json:"estSecondsUntilSelection"`
}

type EscrowStatus struct {
	BalanceUaeth string `json:"balanceUaeth"`
	UnlockHeight int64  `json:"unlockHeight,omitempty"`
	Pending      bool   `json:"pending"`
}

type MinerStatus struct {
	Address string      `json:"address"`
	Height  int64       `json:"height"`
	Epoch   EpochStatus `json:"epoch"`

	RegisteredConsensusKey bool     `json:"registeredConsensusKey"`
	Banned                 bool     `json:"banned"`
	WorkThisEpoch          uint64   `json:"workThisEpoch"`
	Eligible               bool     `json:"eligible"`
	NotEligibleBecause     []string `json:"notEligibleBecause,omitempty"`

	SelectionRule  string `json:"selectionRule"`
	TopKSize       int64  `json:"topKSize"`
	EligibleMiners int    `json:"eligibleMiners"`
	// Rank is 1-based among eligible miners by work, the order
	// top_k_by_work and seat_work_power seat in; 0 when not eligible.
	// OnTrack means the address would be picked if the epoch ended now,
	// and is never true under beacon_weighted_sample: a draw isn't known
	// in advance. Under seat_work_power, Rank, OnTrack and WorkShare go
	// by seat work over the window, and WorkShare is of all the window's
	// work, the share the 1% minimum is measured against.
	Rank      int    `json:"rank,omitempty"`
	OnTrack   bool   `json:"onTrack"`
	WorkShare string `json:"workShare,omitempty"`

	// Under seat_work_power: the difficulty this address's accepted
	// submissions met (native and AuxPoW) over the window, and the whole
	// window's.
	SeatWork          string `json:"seatWork,omitempty"`
	SeatWorkThisEpoch string `json:"seatWorkThisEpoch,omitempty"`
	SeatWorkTotal     string `json:"seatWorkTotal,omitempty"`
	// A rule that applies from a later height than this epoch's selection.
	NextSelectionRule  string `json:"nextSelectionRule,omitempty"`
	NextRuleFromHeight int64  `json:"nextRuleFromHeight,omitempty"`

	ActiveValidator bool `json:"activeValidator"`
	// VotingPower is this address's power in the validator set at
	// Height (0 outside it), and PowerShare its share of TotalVotingPower.
	VotingPower      int64        `json:"votingPower"`
	TotalVotingPower int64        `json:"totalVotingPower"`
	PowerShare       string       `json:"powerShare,omitempty"`
	Escrow           EscrowStatus `json:"escrow"`
}

// EpochWindow is the epoch containing height, per x/pow's
// CurrentEpoch (height / length) and EndBlock ((height+1) % length == 0
// selects).
func EpochWindow(height, length int64) (index, start, selection int64) {
	if length <= 0 {
		length = 1
	}
	index = height / length
	start = index * length
	selection = start + length - 1
	return index, start, selection
}

// SelectionRuleAt is the rule the block at selectionHeight applies.
func SelectionRuleAt(selectionHeight int64) string {
	if selectionHeight >= pow.SeatRuleActivationHeight {
		return SelectionSeatRule
	}
	if selectionHeight >= pow.RandomnessBeaconActivationHeight {
		return SelectionBeaconWeighted
	}
	return SelectionTopKByWork
}

// MinerStatus reports address's standing at the node's latest block.
func (c *Client) MinerStatus(ctx context.Context, address string) (*MinerStatus, error) {
	if _, err := sdk.AccAddressFromBech32(address); err != nil {
		return nil, err
	}

	cmt := cmtservice.NewServiceClient(c.conn)
	latest, err := cmt.GetLatestBlock(ctx, &cmtservice.GetLatestBlockRequest{})
	if err != nil {
		return nil, fmt.Errorf("latest block: %w", err)
	}
	if latest.SdkBlock == nil {
		return nil, fmt.Errorf("latest block: node returned no block")
	}
	height := latest.SdkBlock.Header.Height
	at := metadata.AppendToOutgoingContext(ctx, grpctypes.GRPCBlockHeightHeader, strconv.FormatInt(height, 10))
	q := pow.NewQueryClient(c.conn)

	params, err := q.Params(at, &pow.QueryParamsRequest{})
	if err != nil {
		return nil, fmt.Errorf("pow params: %w", err)
	}
	board, err := q.MinerLeaderboard(at, &pow.QueryMinerLeaderboardRequest{})
	if err != nil {
		return nil, fmt.Errorf("miner leaderboard: %w", err)
	}
	active, err := q.ActiveValidators(at, &pow.QueryActiveValidatorsRequest{})
	if err != nil {
		return nil, fmt.Errorf("active validators: %w", err)
	}
	escrow, err := q.Escrow(at, &pow.QueryEscrowRequest{Miner: address})
	if err != nil {
		return nil, fmt.Errorf("escrow: %w", err)
	}

	index, start, selection := EpochWindow(height, params.EpochLength)
	if index != board.Epoch {
		return nil, fmt.Errorf("epoch mismatch at height %d: computed %d, node reports %d", height, index, board.Epoch)
	}
	st := &MinerStatus{
		Address: address,
		Height:  height,
		Epoch: EpochStatus{
			Index:                index,
			Length:               params.EpochLength,
			StartHeight:          start,
			SelectionHeight:      selection,
			BlocksUntilSelection: selection - height,
		},
		SelectionRule: SelectionRuleAt(selection),
		TopKSize:      params.TopKSize,
		Escrow: EscrowStatus{
			BalanceUaeth: escrow.Balance,
			UnlockHeight: escrow.UnlockHeight,
			Pending:      escrow.HasPendingEscrow,
		},
	}
	for _, v := range active.Validators {
		if v == address {
			st.ActiveValidator = true
		}
	}

	if avg, ok := c.avgBlockTime(ctx, cmt, height, latest.SdkBlock.Header.Time.UnixNano()); ok {
		st.Epoch.AvgBlockSeconds = avg
		st.Epoch.EstSecondsUntilSelection = int64(avg * float64(st.Epoch.BlocksUntilSelection))
	}

	// This address's own key and ban status, whether or not it mined.
	self, err := c.eligibility(at, height, []string{address})
	if err != nil {
		return nil, err
	}
	st.RegisteredConsensusKey = self[0].registered
	st.Banned = self[0].banned

	// Everyone else who mined this epoch, to rank only eligible candidates.
	miners := make([]string, len(board.Entries))
	for i, e := range board.Entries {
		miners[i] = e.Address
		if e.Address == address {
			st.WorkThisEpoch = e.Work
		}
	}
	elig, err := c.eligibility(at, height, miners)
	if err != nil {
		return nil, err
	}
	total := math.ZeroInt()
	for i, e := range board.Entries {
		if !elig[i].registered || elig[i].banned {
			continue
		}
		st.EligibleMiners++
		total = total.Add(math.NewIntFromUint64(e.Work))
		if e.Address == address {
			st.Rank = st.EligibleMiners
		}
	}

	if st.WorkThisEpoch == 0 {
		st.NotEligibleBecause = append(st.NotEligibleBecause, IneligibleNoWork)
	}
	if !st.RegisteredConsensusKey {
		st.NotEligibleBecause = append(st.NotEligibleBecause, IneligibleNoConsensusKey)
	}
	if st.Banned {
		st.NotEligibleBecause = append(st.NotEligibleBecause, IneligibleBanned)
	}
	st.Eligible = len(st.NotEligibleBecause) == 0
	if st.Eligible && total.IsPositive() {
		st.WorkShare = math.LegacyNewDecFromInt(math.NewIntFromUint64(st.WorkThisEpoch)).QuoInt(total).String()
		st.OnTrack = st.SelectionRule == SelectionTopKByWork && int64(st.Rank) <= st.TopKSize
	}

	switch {
	case st.SelectionRule == SelectionSeatRule:
		if err := c.applySeatRule(at, st); err != nil {
			return nil, err
		}
	case pow.SeatRuleActivationHeight > selection:
		st.NextSelectionRule, st.NextRuleFromHeight = SelectionSeatRule, pow.SeatRuleActivationHeight
	}

	powers, totalPower, err := c.validatorPowers(ctx, height)
	if err != nil {
		return nil, err
	}
	st.TotalVotingPower = totalPower
	st.VotingPower = powerOf(powers, self[0].pubkey)
	if st.VotingPower > 0 {
		st.PowerShare = share(math.NewInt(st.VotingPower), math.NewInt(totalPower))
	}
	return st, nil
}

type eligibilityResult struct {
	registered, banned bool
	pubkey             []byte // the registered consensus key, if any
}

// eligibility checks each address's consensus key and ban at height.
// The key has no gRPC query of its own, so it's read straight from
// x/pow's store (the same key GetValidatorPubkey reads), which every
// node already serves.
func (c *Client) eligibility(at context.Context, height int64, addresses []string) ([]eligibilityResult, error) {
	out := make([]eligibilityResult, len(addresses))
	errs := make([]error, len(addresses))
	cmt := cmtservice.NewServiceClient(c.conn)
	q := pow.NewQueryClient(c.conn)
	sem := make(chan struct{}, eligibilityLookupParallel)
	var wg sync.WaitGroup
	for i, a := range addresses {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, a string) {
			defer wg.Done()
			defer func() { <-sem }()
			acc, err := sdk.AccAddressFromBech32(a)
			if err != nil {
				errs[i] = err
				return
			}
			key := append(append([]byte{}, pow.KeyValidatorPubkeyPrefix...), acc.Bytes()...)
			res, err := cmt.ABCIQuery(at, &cmtservice.ABCIQueryRequest{Path: "/store/" + pow.StoreKey + "/key", Data: key, Height: height})
			if err != nil {
				errs[i] = fmt.Errorf("consensus key of %s: %w", a, err)
				return
			}
			if res.Code != 0 {
				errs[i] = fmt.Errorf("consensus key of %s: %s", a, res.Log)
				return
			}
			ban, err := q.BanStatus(at, &pow.QueryBanStatusRequest{Miner: a})
			if err != nil {
				errs[i] = fmt.Errorf("ban status of %s: %w", a, err)
				return
			}
			out[i] = eligibilityResult{registered: len(res.Value) > 0, banned: ban.Banned, pubkey: res.Value}
		}(i, a)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// avgBlockTime is the mean interval over the last blockTimeSampleBlocks
// blocks, the same window cmd/relayer uses.
func (c *Client) avgBlockTime(ctx context.Context, cmt cmtservice.ServiceClient, height, latestUnixNano int64) (float64, bool) {
	from := height - blockTimeSampleBlocks
	if from < 1 {
		from = 1
	}
	if from >= height {
		return 0, false
	}
	old, err := cmt.GetBlockByHeight(ctx, &cmtservice.GetBlockByHeightRequest{Height: from})
	if err != nil || old.SdkBlock == nil {
		return 0, false
	}
	elapsed := float64(latestUnixNano-old.SdkBlock.Header.Time.UnixNano()) / 1e9
	return elapsed / float64(height-from), true
}

// LeaderboardEntry is one miner's proof-of-work this epoch.
type LeaderboardEntry struct {
	Address string `json:"address"`
	Work    uint64 `json:"work"` // accepted native submissions this epoch
	// SeatWork is the window's seat work under seat_work_power.
	SeatWork        string `json:"seatWork,omitempty"`
	ActiveValidator bool   `json:"activeValidator"`
	VotingPower     int64  `json:"votingPower"`
}

// Leaderboard is the current epoch's miners, most work first, as of one
// block.
type Leaderboard struct {
	Height  int64              `json:"height"`
	Epoch   EpochStatus        `json:"epoch"`
	TopK    int64              `json:"topKSize"`
	Rule    string             `json:"selectionRule"`
	Entries []LeaderboardEntry `json:"entries"`
}

// MinerLeaderboard reads the current epoch's leaderboard at the node's
// latest block.
func (c *Client) MinerLeaderboard(ctx context.Context) (*Leaderboard, error) {
	cmt := cmtservice.NewServiceClient(c.conn)
	latest, err := cmt.GetLatestBlock(ctx, &cmtservice.GetLatestBlockRequest{})
	if err != nil {
		return nil, fmt.Errorf("latest block: %w", err)
	}
	if latest.SdkBlock == nil {
		return nil, fmt.Errorf("latest block: node returned no block")
	}
	height := latest.SdkBlock.Header.Height
	at := metadata.AppendToOutgoingContext(ctx, grpctypes.GRPCBlockHeightHeader, strconv.FormatInt(height, 10))
	q := pow.NewQueryClient(c.conn)

	params, err := q.Params(at, &pow.QueryParamsRequest{})
	if err != nil {
		return nil, fmt.Errorf("pow params: %w", err)
	}
	board, err := q.MinerLeaderboard(at, &pow.QueryMinerLeaderboardRequest{})
	if err != nil {
		return nil, fmt.Errorf("miner leaderboard: %w", err)
	}
	active, err := q.ActiveValidators(at, &pow.QueryActiveValidatorsRequest{})
	if err != nil {
		return nil, fmt.Errorf("active validators: %w", err)
	}
	isActive := make(map[string]bool, len(active.Validators))
	for _, v := range active.Validators {
		isActive[v] = true
	}

	index, start, selection := EpochWindow(height, params.EpochLength)
	lb := &Leaderboard{
		Height: height,
		Epoch: EpochStatus{
			Index: index, Length: params.EpochLength, StartHeight: start,
			SelectionHeight: selection, BlocksUntilSelection: selection - height,
		},
		TopK:    params.TopKSize,
		Rule:    SelectionRuleAt(selection),
		Entries: make([]LeaderboardEntry, 0, len(board.Entries)),
	}
	if avg, ok := c.avgBlockTime(ctx, cmt, height, latest.SdkBlock.Header.Time.UnixNano()); ok {
		lb.Epoch.AvgBlockSeconds = avg
		lb.Epoch.EstSecondsUntilSelection = int64(avg * float64(lb.Epoch.BlocksUntilSelection))
	}
	seen := map[string]int{}
	for _, e := range board.Entries {
		seen[e.Address] = len(lb.Entries)
		lb.Entries = append(lb.Entries, LeaderboardEntry{Address: e.Address, Work: e.Work, ActiveValidator: isActive[e.Address]})
	}
	var window *seatWindow
	if lb.Rule == SelectionSeatRule {
		// Merged-mining reward addresses earn seat work without native
		// submissions, so the window's addresses are listed too.
		if window, err = c.seatWindow(at, height, index); err != nil {
			return nil, err
		}
		for _, a := range window.order {
			if _, ok := seen[a]; !ok {
				seen[a] = len(lb.Entries)
				lb.Entries = append(lb.Entries, LeaderboardEntry{Address: a, ActiveValidator: isActive[a]})
			}
			lb.Entries[seen[a]].SeatWork = window.work[a].String()
		}
	}

	addrs := make([]string, len(lb.Entries))
	for i, e := range lb.Entries {
		addrs[i] = e.Address
	}
	keys, err := c.eligibility(at, height, addrs)
	if err != nil {
		return nil, err
	}
	powers, _, err := c.validatorPowers(ctx, height)
	if err != nil {
		return nil, err
	}
	for i := range lb.Entries {
		lb.Entries[i].VotingPower = powerOf(powers, keys[i].pubkey)
	}

	if window != nil {
		sort.SliceStable(lb.Entries, func(i, j int) bool {
			wi, wj := window.work[lb.Entries[i].Address], window.work[lb.Entries[j].Address]
			if wi.IsNil() {
				wi = math.ZeroInt()
			}
			if wj.IsNil() {
				wj = math.ZeroInt()
			}
			return wi.GT(wj)
		})
		return lb, nil
	}
	sort.SliceStable(lb.Entries, func(i, j int) bool { return lb.Entries[i].Work > lb.Entries[j].Work })
	return lb, nil
}
