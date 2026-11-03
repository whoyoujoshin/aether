package app

import (
	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	pow "github.com/whoyoujoshin/aether/x/pow"
)

// miningSubmissionMaxGas bounds the gas limit a fee-free mining submission
// may ask for. One native MsgSubmitPoW uses about 150,000 to 200,000 gas
// with an ML-DSA-44 signature; the browser and desktop miners ask for
// 400,000.
const miningSubmissionMaxGas = 1_000_000

// miningFeeChecker is the node's minimum-gas-price check
// (minimum-gas-prices in app.toml) with one exception: a transaction
// carrying exactly one native MsgSubmitPoW, whose proof-of-work checks
// out against this node's state, needs no fee. Without it, a new miner
// on a chain with a non-zero price couldn't send its first find, having
// nothing to pay with (docs/MAINNET.md: there is no faucet on aether-1).
//
// It can't be used to fill mempools for free: the submission's scrypt
// proof is checked here, in CheckTx, before it is admitted (CheckTx runs
// only the ante chain, not the message), and the chain takes one native
// submission a block. Every other transaction is checked exactly as the
// SDK's default checker (x/auth/ante/validator_tx_fee.go, v0.50.14)
// checks it, which the SDK doesn't export.
//
// This only decides what a node admits to its mempool: in a block, fees
// are never checked against the minimum price, so mixed binaries agree.
func miningFeeChecker(k pow.Keeper) func(ctx sdk.Context, tx sdk.Tx) (sdk.Coins, int64, error) {
	return func(ctx sdk.Context, tx sdk.Tx) (sdk.Coins, int64, error) {
		feeTx, ok := tx.(sdk.FeeTx)
		if !ok {
			return nil, 0, errorsmod.Wrap(sdkerrors.ErrTxDecode, "Tx must be a FeeTx")
		}
		feeCoins := feeTx.GetFee()
		gas := feeTx.GetGas()

		if ctx.IsCheckTx() {
			if miner, native := loneNativeSubmission(tx); native != nil && gas <= miningSubmissionMaxGas {
				if _, err := k.CheckNativeSubmission(ctx, miner, native); err != nil {
					return nil, 0, err
				}
				return feeCoins, MiningPriority, nil
			}
			minGasPrices := ctx.MinGasPrices()
			if !minGasPrices.IsZero() {
				requiredFees := make(sdk.Coins, len(minGasPrices))
				glDec := sdkmath.LegacyNewDec(int64(gas))
				for i, gp := range minGasPrices {
					fee := gp.Amount.Mul(glDec)
					requiredFees[i] = sdk.NewCoin(gp.Denom, fee.Ceil().RoundInt())
				}
				if !feeCoins.IsAnyGTE(requiredFees) {
					return nil, 0, errorsmod.Wrapf(sdkerrors.ErrInsufficientFee, "insufficient fees; got: %s required: %s", feeCoins, requiredFees)
				}
			}
		}
		return feeCoins, txPriority(ctx.MinGasPrices(), feeCoins, int64(gas)), nil
	}
}

// loneNativeSubmission returns the miner and native submission of a
// transaction that carries exactly one MsgSubmitPoW with a native
// submission, or nil.
func loneNativeSubmission(tx sdk.Tx) (sdk.AccAddress, *pow.NativeSubmission) {
	msgs := tx.GetMsgs()
	if len(msgs) != 1 {
		return nil, nil
	}
	m, ok := msgs[0].(*pow.MsgSubmitPoW)
	if !ok || m.GetNative() == nil {
		return nil, nil
	}
	miner, err := sdk.AccAddressFromBech32(m.Miner)
	if err != nil {
		return nil, nil
	}
	return miner, m.GetNative()
}
