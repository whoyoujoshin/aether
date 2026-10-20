package main

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/whoyoujoshin/aether/wallet"
)

// get_miner_status: whether an address will be picked as a validator
// this epoch, without scraping powminer's logs.

type getMinerStatusInput struct {
	Address string `json:"address,omitempty" jsonschema:"address to check; defaults to this agent's own account if omitted"`
}

type minerEpochDTO struct {
	Index                    int64   `json:"index"`
	Length                   int64   `json:"length" jsonschema:"blocks per epoch"`
	StartHeight              int64   `json:"startHeight"`
	SelectionHeight          int64   `json:"selectionHeight" jsonschema:"the epoch's last block: work submitted up to and including it counts, and it picks the next validator set, which takes effect two blocks later"`
	BlocksUntilSelection     int64   `json:"blocksUntilSelection" jsonschema:"0 means the selection block is the latest block, already applied; the next epoch starts at the next block"`
	AvgBlockSeconds          float64 `json:"avgBlockSeconds" jsonschema:"measured over the last 1,000 blocks; 0 if it couldn't be measured"`
	EstSecondsUntilSelection int64   `json:"estSecondsUntilSelection" jsonschema:"an estimate from avgBlockSeconds, not a guarantee"`
}

type minerEscrowDTO struct {
	Balance      amountDTO `json:"balance" jsonschema:"mining rewards held back until unlockHeight"`
	UnlockHeight int64     `json:"unlockHeight,omitempty"`
	Pending      bool      `json:"pending"`
}

type getMinerStatusOutput struct {
	Address string        `json:"address"`
	Height  int64         `json:"height" jsonschema:"every field is as of this block"`
	Epoch   minerEpochDTO `json:"epoch"`

	RegisteredConsensusKey bool     `json:"registeredConsensusKey" jsonschema:"a consensus public key is registered for this address; without one, mining never makes it a validator"`
	Banned                 bool     `json:"banned" jsonschema:"permanently banned for equivocation"`
	WorkThisEpoch          uint64   `json:"workThisEpoch"`
	Eligible               bool     `json:"eligible" jsonschema:"can be picked when this epoch ends: has work this epoch, a registered consensus key, and no ban"`
	NotEligibleBecause     []string `json:"notEligibleBecause,omitempty" jsonschema:"any of no_work_this_epoch, no_consensus_key, banned; under seat_work_power no_seat_work_in_window or below_minimum_share in place of no_work_this_epoch"`

	SelectionRule  string `json:"selectionRule" jsonschema:"top_k_by_work: the topKSize eligible miners with the most work are picked; beacon_weighted_sample: topKSize are drawn at random, weighted by work; seat_work_power: the topKSize miners with the most seat work over the window are seated, each with power in proportion to it (power moves at most 10% of the total an epoch)"`
	TopKSize       int64  `json:"topKSize"`
	EligibleMiners int    `json:"eligibleMiners"`
	Rank           int    `json:"rank,omitempty" jsonschema:"1-based position among eligible miners by work (by seat work under seat_work_power); absent if not eligible"`
	OnTrack        bool   `json:"onTrack" jsonschema:"would be picked if the epoch ended now; never true under beacon_weighted_sample, since a random draw isn't known in advance"`
	WorkShare      string `json:"workShare,omitempty" jsonschema:"0 to 1: this address's share of all eligible work this epoch; under seat_work_power, its share of all the window's seat work, which must be at least 0.01 to qualify"`

	SeatWork           string `json:"seatWork,omitempty" jsonschema:"under seat_work_power: the difficulty this address's accepted submissions met over the last 3 epochs, native and merged-mined; it decides seats and power"`
	SeatWorkThisEpoch  string `json:"seatWorkThisEpoch,omitempty" jsonschema:"under seat_work_power: the part of seatWork earned in this epoch"`
	SeatWorkTotal      string `json:"seatWorkTotal,omitempty" jsonschema:"under seat_work_power: the total the 1% bar is measured against: every address's seat work over the window (from the seat hardening height, registered addresses' only)"`
	NextSelectionRule  string `json:"nextSelectionRule,omitempty" jsonschema:"a different rule that applies from nextRuleFromHeight"`
	NextRuleFromHeight int64  `json:"nextRuleFromHeight,omitempty"`

	ActiveValidator  bool           `json:"activeValidator" jsonschema:"in the validator set right now"`
	VotingPower      int64          `json:"votingPower" jsonschema:"this address's voting power in the validator set at height; 0 outside it"`
	TotalVotingPower int64          `json:"totalVotingPower"`
	PowerShare       string         `json:"powerShare,omitempty" jsonschema:"votingPower over totalVotingPower, 0 to 1"`
	Escrow           minerEscrowDTO `json:"escrow"`
}

func toolGetMinerStatus(ctx context.Context, _ *mcp.CallToolRequest, input getMinerStatusInput) (*mcp.CallToolResult, getMinerStatusOutput, error) {
	address, err := agentOrAddress(input.Address)
	if err != nil {
		return nil, getMinerStatusOutput{}, err
	}
	out, err := minerStatusFor(ctx, address)
	return nil, out, err
}

// minerStatusFor reads an address. It does not open the keyring.
func minerStatusFor(ctx context.Context, address string) (getMinerStatusOutput, error) {
	client, err := wallet.NewClient(grpcEndpoint)
	if err != nil {
		return getMinerStatusOutput{}, err
	}
	defer client.Close()

	st, err := client.MinerStatus(ctx, address)
	if err != nil {
		return getMinerStatusOutput{}, err
	}
	return getMinerStatusOutput{
		Address: st.Address,
		Height:  st.Height,
		Epoch: minerEpochDTO{
			Index:                    st.Epoch.Index,
			Length:                   st.Epoch.Length,
			StartHeight:              st.Epoch.StartHeight,
			SelectionHeight:          st.Epoch.SelectionHeight,
			BlocksUntilSelection:     st.Epoch.BlocksUntilSelection,
			AvgBlockSeconds:          st.Epoch.AvgBlockSeconds,
			EstSecondsUntilSelection: st.Epoch.EstSecondsUntilSelection,
		},
		RegisteredConsensusKey: st.RegisteredConsensusKey,
		Banned:                 st.Banned,
		WorkThisEpoch:          st.WorkThisEpoch,
		Eligible:               st.Eligible,
		NotEligibleBecause:     st.NotEligibleBecause,
		SelectionRule:          st.SelectionRule,
		TopKSize:               st.TopKSize,
		EligibleMiners:         st.EligibleMiners,
		Rank:                   st.Rank,
		OnTrack:                st.OnTrack,
		WorkShare:              st.WorkShare,
		SeatWork:               st.SeatWork,
		SeatWorkThisEpoch:      st.SeatWorkThisEpoch,
		SeatWorkTotal:          st.SeatWorkTotal,
		NextSelectionRule:      st.NextSelectionRule,
		NextRuleFromHeight:     st.NextRuleFromHeight,
		ActiveValidator:        st.ActiveValidator,
		VotingPower:            st.VotingPower,
		TotalVotingPower:       st.TotalVotingPower,
		PowerShare:             st.PowerShare,
		Escrow: minerEscrowDTO{
			Balance:      amountDTOFromUaethString(st.Escrow.BalanceUaeth),
			UnlockHeight: st.Escrow.UnlockHeight,
			Pending:      st.Escrow.Pending,
		},
	}, nil
}
