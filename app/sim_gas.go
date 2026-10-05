package app

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
	txsigning "github.com/cosmos/cosmos-sdk/types/tx/signing"
	"github.com/cosmos/cosmos-sdk/x/auth/signing"

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
