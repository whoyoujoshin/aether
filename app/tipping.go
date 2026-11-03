package app

import (
	"math"

	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/baseapp"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/mempool"
	"github.com/spf13/cast"
)

// Tipping: a transaction that pays more than the node's minimum gas price
// goes into a block ahead of one that pays only the minimum, the way a
// priority fee does on Solana or a tip on Ethereum. Paying the minimum is
// enough whenever blocks have room; the tip only matters when they are
// full. Fees, tips included, go to the treasury (app/fees.go).
//
// It is the proposer's choice of what to put in a block, not a rule
// blocks are checked against, so nodes on different binaries still agree
// on every block.
//
// How:
//   - aetherd runs the SDK's priority mempool on every node
//     (TippingMempoolOption); with the SDK's default (none, or a
//     sender-ordered one) the proposal follows CometBFT's arrival order
//     and a tip buys nothing.
//   - A transaction's priority is its fee per gas as a multiple of this
//     node's minimum price for that denom, in millionths (txPriority): 2x
//     the minimum is 2,000,000, in AETH or in USDC alike. The SDK's own
//     priority is the fee per gas as an integer, which is 0 for every fee
//     under one uaeth per gas, so it couldn't tell a tip from the minimum.
//   - A lone native mining submission with valid proof-of-work, which
//     pays no fee (app/mining_fee.go), goes first (MiningPriority): at the
//     bottom it would wait out spam past its 60-block recency window and
//     be lost. Each one costs a real find, and a block takes one.
//
// Limit: CometBFT's own mempool refuses new transactions while it is
// full, whatever they pay (CometBFT 0.38 can't evict by fee). Its size
// should hold several blocks' worth (docs/OPERATOR.md), so a tipped
// transaction finds room while spam waits.

// MiningPriority is the priority of a lone native mining submission with
// valid proof-of-work: ahead of every fee.
const MiningPriority = int64(math.MaxInt64)

// priorityScale makes a priority millionths of the minimum price.
var priorityScale = sdkmath.LegacyNewDec(1_000_000)

// DefaultMempoolMaxTxs caps the priority mempool when app.toml's
// mempool.max-txs doesn't (it is -1 in the file `aetherd init` writes).
// It matches CometBFT's default mempool size; a transaction CometBFT
// holds is in this mempool too.
const DefaultMempoolMaxTxs = 5_000

// NewTippingMempool returns the priority mempool, holding at most maxTxs
// transactions (0: no cap).
func NewTippingMempool(maxTxs int) mempool.Mempool {
	return mempool.NewPriorityMempool(mempool.PriorityNonceMempoolConfig[int64]{
		TxPriority: mempool.NewDefaultTxPriority(),
		MaxTx:      maxTxs,
	})
}

// TippingMempoolOption installs the priority mempool, sized by app.toml's
// mempool.max-txs: above 0 it is the cap, 0 is no cap, and -1 or unset
// (the SDK's "no app mempool") is DefaultMempoolMaxTxs. It goes after
// server.DefaultBaseappOptions, whose mempool it replaces.
func TippingMempoolOption(appOpts servertypes.AppOptions) func(*baseapp.BaseApp) {
	return baseapp.SetMempool(NewTippingMempool(mempoolMaxTxs(appOpts)))
}

func mempoolMaxTxs(appOpts servertypes.AppOptions) int {
	if v := appOpts.Get("mempool.max-txs"); v != nil {
		if n := cast.ToInt(v); n >= 0 {
			return n
		}
	}
	return DefaultMempoolMaxTxs
}

// txPriority is a fee's priority: for each coin of the fee, its price per
// gas as a multiple of this node's minimum price for that denom, in
// millionths, and the best of them, since a fee passes if any one denom
// covers the minimum. A coin in a denom the node doesn't price counts for
// nothing. A node with no minimum price (the testnet's) ranks by price
// per gas itself, in millionths of a base unit.
func txPriority(minGasPrices sdk.DecCoins, fee sdk.Coins, gas int64) int64 {
	if gas <= 0 {
		return 0
	}
	gasDec := sdkmath.LegacyNewDec(gas)
	best := sdkmath.LegacyZeroDec()
	for _, c := range fee {
		var p sdkmath.LegacyDec
		if minGasPrices.IsZero() {
			p = sdkmath.LegacyNewDecFromInt(c.Amount).Mul(priorityScale).Quo(gasDec)
		} else {
			floor := minGasPrices.AmountOf(c.Denom)
			if !floor.IsPositive() {
				continue
			}
			p = sdkmath.LegacyNewDecFromInt(c.Amount).Mul(priorityScale).Quo(gasDec.Mul(floor))
		}
		if p.GT(best) {
			best = p
		}
	}
	// MiningPriority stays above every fee.
	if limit := sdkmath.LegacyNewDec(MiningPriority - 1); best.GT(limit) {
		return MiningPriority - 1
	}
	return best.TruncateInt64()
}
