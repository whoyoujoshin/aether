package app

import (
	"bytes"

	sdkerrors "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrorsmod "github.com/cosmos/cosmos-sdk/types/errors"
	authante "github.com/cosmos/cosmos-sdk/x/auth/ante"
	"github.com/cosmos/cosmos-sdk/x/auth/signing"

	"github.com/whoyoujoshin/aether/gates"
)

// RotationActivationHeight is the first block at which
// RotatingSetPubKeyDecorator accepts a pubkey that matches the account's
// current pubkey even when its address does not match the signer.
// Below this height the stock check applies. A node running old code
// splits at this height; it does not halt.
//
// The value is a placeholder. Set it before the change goes live.
const RotationActivationHeight int64 = 0 // TODO: set before activation

// RotatingSetPubKeyDecorator is the stock SetPubKeyDecorator with one
// difference: when the gate is on, a pubkey that equals the pubkey
// already on the account is accepted even if its Address() does not
// match the signer. Every other mismatch is still rejected.
type RotatingSetPubKeyDecorator struct {
	ak authante.AccountKeeper
}

func NewRotatingSetPubKeyDecorator(ak authante.AccountKeeper) RotatingSetPubKeyDecorator {
	return RotatingSetPubKeyDecorator{ak: ak}
}

func (d RotatingSetPubKeyDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (sdk.Context, error) {
	if !gates.On(ctx, RotationActivationHeight) || RotationActivationHeight == 0 {
		// Gate off or not yet set: stock behavior.
		return authante.NewSetPubKeyDecorator(d.ak).AnteHandle(ctx, tx, simulate, next)
	}

	sigTx, ok := tx.(signing.SigVerifiableTx)
	if !ok {
		return ctx, sdkerrors.Wrap(sdkerrorsmod.ErrTxDecode, "invalid tx type")
	}
	pubkeys, err := sigTx.GetPubKeys()
	if err != nil {
		return ctx, err
	}
	signers, err := sigTx.GetSigners()
	if err != nil {
		return ctx, err
	}

	for i, pk := range pubkeys {
		if pk == nil {
			continue
		}
		acc, err := authante.GetSignerAcc(ctx, d.ak, signers[i])
		if err != nil {
			return ctx, err
		}
		// Accept a pubkey that matches the one already on the account,
		// even if its address does not match the signer. This is the
		// rotation case: the account address is stable, the pubkey changed.
		if acc.GetPubKey() != nil && bytes.Equal(acc.GetPubKey().Bytes(), pk.Bytes()) {
			continue
		}
		// Otherwise the stock check: address must match.
		if !bytes.Equal(pk.Address(), signers[i]) && ctx.IsSigverifyTx() {
			return ctx, sdkerrors.Wrapf(sdkerrorsmod.ErrInvalidPubKey,
				"pubKey does not match signer address %s", sdk.AccAddress(signers[i]))
		}
		if acc.GetPubKey() == nil {
			if err := acc.SetPubKey(pk); err != nil {
				return ctx, err
			}
			d.ak.SetAccount(ctx, acc)
		}
	}
	return next(ctx, tx, simulate)
}
