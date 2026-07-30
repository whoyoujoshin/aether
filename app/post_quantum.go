package app

import (
	
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "cosmossdk.io/errors"
	"github.com/cosmos/cosmos-sdk/x/auth/signing"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
)

// ErrNonPostQuantumSignature is returned when a transaction is signed
// with anything other than ML-DSA-44. Registered, per the SDK's error
// module convention, under a dedicated codespace.
var ErrNonPostQuantumSignature = sdkerrors.Register("app", 1, "signature is not a post-quantum (ML-DSA-44) key")

// PostQuantumDecorator enforces the locked design (see
// pq-signatures-decision.md): ML-DSA-44 is mandatory for every account
// transaction from genesis, with no classical fallback. This is a
// TYPE-ENFORCEMENT gate only -- real cryptographic signature
// verification for ML-DSA already happens automatically via the stock
// ante-handler chain's own SigVerificationDecorator, which calls
// VerifySignature() polymorphically on whatever PubKey type is
// present. Since mldsa.PubKey correctly implements that interface, no
// custom verification logic is needed here or anywhere else -- this
// decorator's only job is to reject any signer whose pubkey is NOT
// mldsa.PubKey (e.g. secp256k1, ed25519), which would otherwise also
// verify successfully since both remain registered, valid types.
type PostQuantumDecorator struct{}

func NewPostQuantumDecorator() PostQuantumDecorator {
	return PostQuantumDecorator{}
}

func (pqd PostQuantumDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (newCtx sdk.Context, err error) {
	sigTx, ok := tx.(signing.SigVerifiableTx)
	if !ok {
		return ctx, sdkerrors.Wrap(ErrNonPostQuantumSignature, "invalid transaction type: does not support signature verification")
	}

	pubKeys, err := sigTx.GetPubKeys()
	if err != nil {
		return ctx, sdkerrors.Wrap(err, "failed to retrieve signer public keys")
	}

	for i, pubKey := range pubKeys {
		if pubKey == nil {
			// Signer's pubkey isn't in this specific message (already
			// registered on their account from a prior tx) -- nothing to
			// check here; their account-level pubkey was already
			// validated as ML-DSA when it was first set.
			continue
		}
		if _, ok := pubKey.(*mldsa.PubKey); !ok {
			return ctx, sdkerrors.Wrapf(ErrNonPostQuantumSignature,
				"signer %d uses key type %q, but Aether requires ML-DSA-44 (mldsa44) for all account transactions",
				i, pubKey.Type())
		}
	}

	return next(ctx, tx, simulate)
}

// IsPostQuantumReady returns whether the chain is enforcing PQ
// signatures. Always true now -- PQ enforcement is mandatory from
// genesis, not governance-gated.
func IsPostQuantumReady() bool {
	return true
}