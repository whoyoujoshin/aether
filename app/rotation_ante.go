package app

import (
	"bytes"

	errorsmod "cosmossdk.io/errors"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	authante "github.com/cosmos/cosmos-sdk/x/auth/ante"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	"github.com/cosmos/cosmos-sdk/x/authz"

	"github.com/whoyoujoshin/aether/gates"
	"github.com/whoyoujoshin/aether/x/accountauth"
	"github.com/whoyoujoshin/aether/x/governance"
)

// RotatingSetPubKeyDecorator is the SDK's SetPubKeyDecorator plus one
// case, from accountauth.RotationActivationHeight: a signer may carry a
// public key whose address isn't the signer's, if that key is exactly the
// one already on the account. That is an account whose key was rotated
// (x/accountauth, docs/PQ-KEY-ROTATION.md): its address stays the hash of
// its first key, and its stored key is the new one. Signature checking is
// unchanged: SigVerificationDecorator verifies against the stored key.
//
// It runs the stock decorator in every case, so its events (tx.acc_seq,
// tx.signature), its simulation handling and its other checks are the
// SDK's own. For a rotated signer it runs it with the address check off
// (IsSigverifyTx false, which only that check reads in SetPubKeyDecorator)
// and turns it back on for the rest of the chain. Any other mismatch goes
// to the stock decorator as is, which refuses it. Ordinary transactions
// read nothing more and use no more gas than before.
type RotatingSetPubKeyDecorator struct {
	ak    authante.AccountKeeper
	stock authante.SetPubKeyDecorator
}

func NewRotatingSetPubKeyDecorator(ak authante.AccountKeeper) RotatingSetPubKeyDecorator {
	return RotatingSetPubKeyDecorator{ak: ak, stock: authante.NewSetPubKeyDecorator(ak)}
}

func (d RotatingSetPubKeyDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (sdk.Context, error) {
	if simulate || !ctx.IsSigverifyTx() || !gates.On(ctx, accountauth.RotationActivationHeight) {
		return d.stock.AnteHandle(ctx, tx, simulate, next)
	}
	sigTx, ok := tx.(authsigning.SigVerifiableTx)
	if !ok {
		return d.stock.AnteHandle(ctx, tx, simulate, next)
	}
	pubkeys, err := sigTx.GetPubKeys()
	if err != nil {
		return d.stock.AnteHandle(ctx, tx, simulate, next)
	}
	signers, err := sigTx.GetSigners()
	if err != nil || len(pubkeys) > len(signers) {
		return d.stock.AnteHandle(ctx, tx, simulate, next)
	}
	rotated := false
	for i, pk := range pubkeys {
		if pk == nil || bytes.Equal(pk.Address(), signers[i]) {
			continue
		}
		acc := d.ak.GetAccount(ctx, signers[i])
		if acc == nil || acc.GetPubKey() == nil || !acc.GetPubKey().Equals(pk) {
			// Not a rotated account's own key: the stock check refuses it.
			return d.stock.AnteHandle(ctx, tx, simulate, next)
		}
		rotated = true
	}
	if !rotated {
		return d.stock.AnteHandle(ctx, tx, simulate, next)
	}
	return d.stock.AnteHandle(ctx.WithIsSigverifyTx(false), tx, simulate, func(c sdk.Context, tx sdk.Tx, simulate bool) (sdk.Context, error) {
		return next(c.WithIsSigverifyTx(true), tx, simulate)
	})
}

// newAnteChain is authante.NewAnteHandler (Cosmos SDK v0.50.14,
// x/auth/ante/ante.go) with SetPubKeyDecorator replaced by
// RotatingSetPubKeyDecorator: the same decorators, in the same order,
// with the same options. The SDK offers no way to replace one decorator.
// TestAnteChain_SDKVersion fails if the SDK changes, so this is checked
// against the new NewAnteHandler before an upgrade.
func newAnteChain(options authante.HandlerOptions) (sdk.AnteHandler, error) {
	if options.AccountKeeper == nil {
		return nil, errorsmod.Wrap(sdkerrors.ErrLogic, "account keeper is required for ante builder")
	}
	if options.BankKeeper == nil {
		return nil, errorsmod.Wrap(sdkerrors.ErrLogic, "bank keeper is required for ante builder")
	}
	if options.SignModeHandler == nil {
		return nil, errorsmod.Wrap(sdkerrors.ErrLogic, "sign mode handler is required for ante builder")
	}
	return sdk.ChainAnteDecorators(
		authante.NewSetUpContextDecorator(),
		authante.NewExtensionOptionsDecorator(options.ExtensionOptionChecker),
		authante.NewValidateBasicDecorator(),
		authante.NewTxTimeoutHeightDecorator(),
		authante.NewValidateMemoDecorator(options.AccountKeeper),
		authante.NewConsumeGasForTxSizeDecorator(options.AccountKeeper),
		authante.NewDeductFeeDecorator(options.AccountKeeper, options.BankKeeper, options.FeegrantKeeper, options.TxFeeChecker),
		NewRotatingSetPubKeyDecorator(options.AccountKeeper),
		authante.NewValidateSigCountDecorator(options.AccountKeeper),
		authante.NewSigGasConsumeDecorator(options.AccountKeeper, options.SigGasConsumer),
		authante.NewSigVerificationDecorator(options.AccountKeeper, options.SignModeHandler),
		authante.NewIncrementSequenceDecorator(options.AccountKeeper),
	), nil
}

// maxRotationWalkDepth bounds how deep checkRotationMsgs looks into
// nested messages. From RotationActivationHeight a transaction nested
// deeper is refused; before it, it is let through as before.
const maxRotationWalkDepth = 6

// checkRotationMsgs runs before the rest of the ante chain, in every
// mode.
//
// Before RotationActivationHeight it refuses any transaction that holds a
// rotation message or an authz grant for one, at any depth it looks. A
// node still on an older binary can't decode those types; one on this
// binary would process them; if a block carried one the two would get
// different results and the chain would halt. Refusing them in CheckTx
// keeps them out of every mempool, so no honest proposer of either
// binary includes one while validators upgrade.
//
// From the height, a rotation message must be at the top level of the
// transaction, signed by the account's own key (or, for MsgRecoverKey and
// MsgGuardianCancel, submitted with its guardians' signatures). Inside an
// authz MsgExec, an x/accountauth MsgExecAuthenticated or a governance
// proposal it is refused, and so is an authz grant for one: a grantee
// holding such a grant could otherwise move the granter's address to a
// key of its own.
func (app *App) checkRotationMsgs(ctx sdk.Context, tx sdk.Tx) error {
	active := gates.On(ctx, accountauth.RotationActivationHeight)
	for _, m := range tx.GetMsgs() {
		if accountauth.IsRotationMsg(m) {
			if !active {
				return errorsmod.Wrapf(accountauth.ErrRotationNotActive, "%s is not active until height %d", sdk.MsgTypeURL(m), gates.Height(ctx.ChainID(), accountauth.RotationActivationHeight))
			}
			continue
		}
		if err := app.walkNestedRotation(ctx, m, active, 1); err != nil {
			return err
		}
	}
	return nil
}

func (app *App) walkNestedRotation(ctx sdk.Context, m sdk.Msg, active bool, depth int) error {
	var inner []sdk.Msg
	switch msg := m.(type) {
	case *authz.MsgGrant:
		if a, err := msg.GetAuthorization(); err == nil && a != nil && accountauth.IsRotationMsgTypeURL(a.MsgTypeURL()) {
			return errorsmod.Wrapf(accountauth.ErrRotationNotDelegable, "an authz grant for %s", a.MsgTypeURL())
		}
		return nil
	case *authz.MsgExec:
		msgs, err := msg.GetMessages()
		if err != nil {
			return nil // the authz handler refuses it
		}
		inner = msgs
	case *accountauth.MsgExecAuthenticated:
		inner = app.unpackMsgs(msg.Msgs)
	case *governance.MsgSubmitParamChangeProposal:
		if msg.ExecuteMsg != nil {
			inner = app.unpackMsgs([]*codectypes.Any{msg.ExecuteMsg})
		}
	default:
		return nil
	}
	if depth > maxRotationWalkDepth {
		if active {
			return errorsmod.Wrapf(sdkerrors.ErrInvalidRequest, "messages nested more than %d deep", maxRotationWalkDepth)
		}
		return nil
	}
	for _, im := range inner {
		if accountauth.IsRotationMsg(im) {
			if !active {
				return errorsmod.Wrapf(accountauth.ErrRotationNotActive, "%s is not active until height %d", sdk.MsgTypeURL(im), gates.Height(ctx.ChainID(), accountauth.RotationActivationHeight))
			}
			return errorsmod.Wrapf(accountauth.ErrRotationNotDelegable, "%s must be at the top level of a transaction", sdk.MsgTypeURL(im))
		}
		if err := app.walkNestedRotation(ctx, im, active, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// unpackMsgs is the messages in anys that decode; the rest are refused
// by the handler that runs them.
func (app *App) unpackMsgs(anys []*codectypes.Any) []sdk.Msg {
	var out []sdk.Msg
	for _, a := range anys {
		if a == nil {
			continue
		}
		if m, ok := a.GetCachedValue().(sdk.Msg); ok {
			out = append(out, m)
			continue
		}
		var m sdk.Msg
		if err := app.interfaceRegistry.UnpackAny(a, &m); err == nil {
			out = append(out, m)
		}
	}
	return out
}
