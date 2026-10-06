package app

import (
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	txsigning "github.com/cosmos/cosmos-sdk/types/tx/signing"
	"github.com/cosmos/cosmos-sdk/x/auth/signing"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
)

// sdkSimSigSize is the size of the placeholder signature x/auth's
// ConsumeTxSizeGasDecorator charges for when simulating a tx
// (simSecp256k1Sig in x/auth/ante/sigverify.go): a secp256k1 signature.
const sdkSimSigSize = 64

// consumeSimulatedMLDSASigGas makes `--gas auto` estimates cover the
// real ML-DSA-44 signature.
//
// A simulated tx carries empty signatures, so its byte-size gas leaves
// them out. x/auth adds them back by charging for a 64-byte secp256k1
// placeholder, but an ML-DSA-44 signature is mldsa.SignatureSize (2,420)
// bytes. Without this the estimate falls about 23,560 gas short at the
// default 10 gas per byte, and a tx sent at that estimate runs out of
// gas.
//
// It runs only when simulating, which never happens in CheckTx or
// FinalizeBlock, so it doesn't change consensus.
func consumeSimulatedMLDSASigGas(ctx sdk.Context, tx sdk.Tx, costPerByte uint64) {
	sigTx, ok := tx.(signing.SigVerifiableTx)
	if !ok {
		return
	}
	sigs, err := sigTx.GetSignaturesV2()
	if err != nil {
		return
	}
	for _, sig := range sigs {
		data, ok := sig.Data.(*txsigning.SingleSignatureData)
		if !ok || len(data.Signature) != 0 {
			continue
		}
		// PostQuantumDecorator has already rejected any other key type,
		// and a signer with no pubkey in the tx has an ML-DSA one on
		// chain.
		if _, isMLDSA := sig.PubKey.(*mldsa.PubKey); sig.PubKey != nil && !isMLDSA {
			continue
		}
		ctx.GasMeter().ConsumeGas(costPerByte*(mldsa.SignatureSize-sdkSimSigSize), "txSize: mldsa44 signature")
	}
}

// consumeSimulatedFeeGas makes `--gas auto --gas-prices` estimates cover
// moving the fee.
//
// With --gas-prices, the CLI simulates at gas 0, so the fee it simulates
// is zero ("0uaeth"), and x/auth skips moving a zero fee. The real tx
// moves its fee from the payer (or granter) to the fee collector, which
// reads the payer's account: with a 1,312-byte ML-DSA-44 pubkey on it,
// that's most of the 15k gas the estimate came in short by. So a
// simulated tx whose fee names a denom but is zero pays for moving 1 of
// each denom, on a cache that's thrown away. A tx with no fee at all is
// left alone: its real version moves nothing either.
//
// It runs only when simulating, so it doesn't change consensus.
func (app *App) consumeSimulatedFeeGas(ctx sdk.Context, tx sdk.Tx) {
	feeTx, ok := tx.(sdk.FeeTx)
	if !ok {
		return
	}
	fee := feeTx.GetFee()
	if len(fee) == 0 || !fee.IsZero() {
		return
	}
	probe := sdk.NewCoins()
	for _, c := range fee {
		probe = probe.Add(sdk.NewCoin(c.Denom, sdkmath.OneInt()))
	}
	from := sdk.AccAddress(feeTx.FeePayer())
	if granter := feeTx.FeeGranter(); granter != nil {
		from = granter
	}
	// The cache shares ctx's gas meter; its writes are dropped. If the
	// payer can't cover even 1 unit, the real tx fails anyway.
	cache, _ := ctx.CacheContext()
	_ = app.BankKeeper.SendCoinsFromAccountToModule(cache, from, authtypes.FeeCollectorName, probe)
}
