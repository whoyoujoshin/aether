package wallet

import (
	"context"
	"fmt"
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
	// top_k_by_work selects in; 0 when not eligible. OnTrack means the
	// address would be picked if the epoch ended now, and is only ever
	// true under top_k_by_work: a beacon sample isn't known in advance.
	Rank      int    `json:"rank,omitempty"`
	OnTrack   bool   `json:"onTrack"`
	WorkShare string `json:"workShare,omitempty"`

	ActiveValidator bool         `json:"activeValidator"`
	Escrow          EscrowStatus `json:"escrow"`
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
	return st, nil
}

type eligibilityResult struct{ registered, banned bool }

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
			out[i] = eligibilityResult{registered: len(res.Value) > 0, banned: ban.Banned}
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
