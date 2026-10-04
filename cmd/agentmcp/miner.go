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
	NotEligibleBecause     []string `json:"notEligibleBecause,omitempty" jsonschema:"any of no_work_this_epoch, no_consensus_key, banned"`

	SelectionRule  string `json:"selectionRule" jsonschema:"top_k_by_work: the topKSize eligible miners with the most work are picked; beacon_weighted_sample: topKSize are drawn at random, weighted by work"`
	TopKSize       int64  `json:"topKSize"`
	EligibleMiners int    `json:"eligibleMiners"`
	Rank           int    `json:"rank,omitempty" jsonschema:"1-based position among eligible miners by work; absent if not eligible"`
	OnTrack        bool   `json:"onTrack" jsonschema:"would be picked if the epoch ended now; only ever true under top_k_by_work, since a random draw isn't known in advance"`
	WorkShare      string `json:"workShare,omitempty" jsonschema:"this address's share of all eligible work this epoch, 0 to 1; under beacon_weighted_sample it drives the odds of being drawn"`

	ActiveValidator bool           `json:"activeValidator" jsonschema:"in the validator set right now"`
	Escrow          minerEscrowDTO `json:"escrow"`
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
		ActiveValidator:        st.ActiveValidator,
		Escrow: minerEscrowDTO{
			Balance:      amountDTOFromUaethString(st.Escrow.BalanceUaeth),
			UnlockHeight: st.Escrow.UnlockHeight,
			Pending:      st.Escrow.Pending,
		},
	}, nil
}
