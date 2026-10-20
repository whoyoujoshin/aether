// Package gates says from which height each height-gated change applies
// on a given chain.
//
// Every gated change in x/pow, x/governance and app was switched on at a
// height on aether-testnet-1, and below that height the old rule still
// applies, so a node replaying the testnet from genesis computes what
// the chain did. A fresh chain has no such history: on it every gate
// opens at FreshFrom, the first block (docs/MAINNET.md §4). The testnet
// heights stay as they are, so nothing changes on aether-testnet-1.
//
// A fresh chain is the mainnet (MainnetChainID) or any chain whose ID
// starts with RehearsalPrefix, such as aether-rc-1 or a devnet named
// aether-rc-dev. Every other chain ID, the empty one in unit tests
// included, gets the testnet heights.
package gates

import (
	"strings"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

const (
	TestnetChainID  = "aether-testnet-1"
	MainnetChainID  = "aether-1"
	RehearsalPrefix = "aether-rc-"
)

// FreshFrom is the height every gate opens at on a fresh chain. It is 1,
// not 0: changes that run once, at their height, such as creating a
// module's stores and running its InitGenesis, still run, in block 1.
const FreshFrom int64 = 1

// Fresh reports whether chainID starts with every gate open.
func Fresh(chainID string) bool {
	return chainID == MainnetChainID || strings.HasPrefix(chainID, RehearsalPrefix)
}

// Height is the height a change gated at testnetHeight on the testnet
// applies from on chainID.
func Height(chainID string, testnetHeight int64) int64 {
	if Fresh(chainID) {
		return FreshFrom
	}
	return testnetHeight
}

// On reports whether the change gated at testnetHeight applies to ctx's
// block.
func On(ctx sdk.Context, testnetHeight int64) bool {
	return ctx.BlockHeight() >= Height(ctx.ChainID(), testnetHeight)
}

// At reports whether ctx's block is the one a once-only change gated at
// testnetHeight runs in.
func At(ctx sdk.Context, testnetHeight int64) bool {
	return ctx.BlockHeight() == Height(ctx.ChainID(), testnetHeight)
}
