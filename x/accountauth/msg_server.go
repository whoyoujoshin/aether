package accountauth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	sdkerrors "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"
	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stderrors "github.com/cosmos/cosmos-sdk/types/errors"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
)

type msgServer struct {
	Keeper
}

func NewMsgServerImpl(keeper Keeper) MsgServer {
	return &msgServer{Keeper: keeper}
}

func (k msgServer) RegisterAuthenticator(goCtx context.Context, msg *MsgRegisterAuthenticator) (*MsgRegisterAuthenticatorResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	account, err := sdk.AccAddressFromBech32(msg.Account)
	if err != nil {
		return nil, sdkerrors.Wrapf(ErrInvalidAccount, "invalid account address %q: %s", msg.Account, err)
	}

	a := msg.Authenticator
	switch kind := a.Kind.(type) {
	case *Authenticator_SessionKey:
		sk := kind.SessionKey
		if len(sk.Pubkey) != mldsa.PubKeySize {
			return nil, sdkerrors.Wrapf(ErrInvalidAuthenticator, "session key pubkey must be exactly %d bytes, got %d", mldsa.PubKeySize, len(sk.Pubkey))
		}
		if sk.ExpiresAtUnix <= ctx.BlockTime().Unix() {
			return nil, sdkerrors.Wrap(ErrInvalidAuthenticator, "session key expires_at_unix must be in the future")
		}
		if len(sk.AllowedMsgTypes) == 0 {
			return nil, sdkerrors.Wrap(ErrInvalidAuthenticator, "session key must allow at least one message type")
		}
		if _, ok := sdkmath.NewIntFromString(sk.SpendLimitUaeth); !ok {
			return nil, sdkerrors.Wrapf(ErrInvalidAuthenticator, "spend_limit_uaeth %q is not a valid non-negative integer", sk.SpendLimitUaeth)
		}
		sk.SpentUaeth = "0" // always starts at zero, regardless of what was submitted
	case *Authenticator_GuardianThreshold:
		gt := kind.GuardianThreshold
		if len(gt.GuardianPubkeys) == 0 {
			return nil, sdkerrors.Wrap(ErrInvalidAuthenticator, "guardian threshold needs at least one guardian")
		}
		seen := make(map[string]bool, len(gt.GuardianPubkeys))
		for _, pk := range gt.GuardianPubkeys {
			if len(pk) != mldsa.PubKeySize {
				return nil, sdkerrors.Wrapf(ErrInvalidAuthenticator, "guardian pubkey must be exactly %d bytes, got %d", mldsa.PubKeySize, len(pk))
			}
			if seen[string(pk)] {
				return nil, sdkerrors.Wrap(ErrInvalidAuthenticator, "duplicate guardian pubkey")
			}
			seen[string(pk)] = true
		}
		if gt.Threshold == 0 || int(gt.Threshold) > len(gt.GuardianPubkeys) {
			return nil, sdkerrors.Wrapf(ErrInvalidAuthenticator, "threshold must be between 1 and %d", len(gt.GuardianPubkeys))
		}
		gt.NextSequence = 0 // always starts at zero, regardless of what was submitted
	default:
		return nil, sdkerrors.Wrap(ErrInvalidAuthenticator, "exactly one of session_key or guardian_threshold must be set")
	}

	id := k.Keeper.NextAuthenticatorID(ctx, account)
	a.Id = id
	k.Keeper.SetAuthenticator(ctx, account, *a)

	return &MsgRegisterAuthenticatorResponse{Id: id}, nil
}

func (k msgServer) RevokeAuthenticator(goCtx context.Context, msg *MsgRevokeAuthenticator) (*MsgRevokeAuthenticatorResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	account, err := sdk.AccAddressFromBech32(msg.Account)
	if err != nil {
		return nil, sdkerrors.Wrapf(ErrInvalidAccount, "invalid account address %q: %s", msg.Account, err)
	}
	if _, found := k.Keeper.GetAuthenticator(ctx, account, msg.Id); !found {
		return nil, sdkerrors.Wrapf(ErrAuthenticatorNotFound, "account %s has no authenticator %d", msg.Account, msg.Id)
	}
	k.Keeper.DeleteAuthenticator(ctx, account, msg.Id)
	return &MsgRevokeAuthenticatorResponse{}, nil
}

// ExecAuthenticated dispatches msg.Msgs as msg.Account, once the
// authenticator named by msg.AuthenticatorId accepts them. Deliberately
// does NOT touch ante-handler logic: msg.Signer already went through
// the normal PostQuantumDecorator + signature-verification path for
// THIS outer tx (proving whoever submitted it controls that ML-DSA
// key), exactly like x/authz's MsgExec is itself an ordinary signed
// message whose grantee-signer is separately authenticated. What's
// custom here is entirely in-handler: deciding whether that already-
// authenticated signer (session key) or a set of attached signatures
// (guardian threshold) is enough to act as account.
func (k msgServer) ExecAuthenticated(goCtx context.Context, msg *MsgExecAuthenticated) (*MsgExecAuthenticatedResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	account, err := sdk.AccAddressFromBech32(msg.Account)
	if err != nil {
		return nil, sdkerrors.Wrapf(ErrInvalidAccount, "invalid account address %q: %s", msg.Account, err)
	}
	signer, err := sdk.AccAddressFromBech32(msg.Signer)
	if err != nil {
		return nil, sdkerrors.Wrapf(ErrInvalidAccount, "invalid signer address %q: %s", msg.Signer, err)
	}
	if len(msg.Msgs) == 0 {
		return nil, sdkerrors.Wrap(ErrInvalidAuthenticator, "no messages to execute")
	}

	a, found := k.Keeper.GetAuthenticator(ctx, account, msg.AuthenticatorId)
	if !found {
		return nil, sdkerrors.Wrapf(ErrAuthenticatorNotFound, "account %s has no authenticator %d", msg.Account, msg.AuthenticatorId)
	}

	msgs := make([]sdk.Msg, len(msg.Msgs))
	for i, any := range msg.Msgs {
		m, ok := any.GetCachedValue().(sdk.Msg)
		if !ok {
			return nil, sdkerrors.Wrapf(ErrInvalidAuthenticator, "message %d is not a valid sdk.Msg", i)
		}
		msgs[i] = m
	}
	for _, m := range msgs {
		signers, _, err := k.cdc.GetMsgV1Signers(m)
		if err != nil {
			return nil, sdkerrors.Wrap(ErrInvalidAuthenticator, err.Error())
		}
		if len(signers) != 1 || !bytes.Equal(signers[0], account) {
			return nil, sdkerrors.Wrapf(ErrWrongMsgSigner, "message %s must be signed as %s", sdk.MsgTypeURL(m), msg.Account)
		}
	}

	switch kind := a.Kind.(type) {
	case *Authenticator_SessionKey:
		if err := k.acceptSessionKey(ctx, account, signer, &a, kind.SessionKey, msgs); err != nil {
			return nil, err
		}
	case *Authenticator_GuardianThreshold:
		if err := k.acceptGuardianThreshold(ctx, account, &a, kind.GuardianThreshold, msg); err != nil {
			return nil, err
		}
	default:
		return nil, sdkerrors.Wrap(ErrInvalidAuthenticator, "authenticator has no configured kind")
	}

	results := make([][]byte, len(msgs))
	for i, m := range msgs {
		handler := k.router.Handler(m)
		if handler == nil {
			return nil, sdkerrors.Wrapf(stderrors.ErrUnknownRequest, "unrecognized message route: %s", sdk.MsgTypeURL(m))
		}
		resp, err := handler(ctx, m)
		if err != nil {
			return nil, sdkerrors.Wrapf(err, "failed to execute message %d", i)
		}
		results[i] = resp.Data
		ctx.EventManager().EmitEvents(resp.GetEvents())
	}

	return &MsgExecAuthenticatedResponse{Results: results}, nil
}

// acceptSessionKey checks that signer really is this session key
// (address-derived from its registered pubkey), that it hasn't
// expired, that every message type is on its allow-list, and that
// bank sends stay within its remaining lifetime spend limit --
// updating SpentUaeth and persisting it before returning.
func (k msgServer) acceptSessionKey(ctx sdk.Context, account, signer sdk.AccAddress, a *Authenticator, sk *SessionKey, msgs []sdk.Msg) error {
	if !bytes.Equal(signer, (&mldsa.PubKey{Key: sk.Pubkey}).Address().Bytes()) {
		return sdkerrors.Wrap(ErrWrongSigner, "signer is not this session key")
	}
	if ctx.BlockTime().Unix() >= sk.ExpiresAtUnix {
		return sdkerrors.Wrapf(ErrSessionKeyExpired, "expired at %s", time.Unix(sk.ExpiresAtUnix, 0).UTC())
	}

	allowed := make(map[string]bool, len(sk.AllowedMsgTypes))
	for _, t := range sk.AllowedMsgTypes {
		allowed[t] = true
	}

	limit, _ := sdkmath.NewIntFromString(sk.SpendLimitUaeth)
	spent, ok := sdkmath.NewIntFromString(sk.SpentUaeth)
	if !ok {
		spent = sdkmath.ZeroInt()
	}

	for _, m := range msgs {
		typeURL := sdk.MsgTypeURL(m)
		if !allowed[typeURL] {
			return sdkerrors.Wrapf(ErrMsgTypeNotAllowed, "%s is not on this session key's allow-list", typeURL)
		}
		if send, ok := m.(*banktypes.MsgSend); ok {
			spent = spent.Add(send.Amount.AmountOf("uaeth"))
			if spent.GT(limit) {
				return sdkerrors.Wrapf(ErrSpendLimitExceeded, "this session key's lifetime spend limit is %s uaeth", limit)
			}
		}
	}

	sk.SpentUaeth = spent.String()
	a.Kind = &Authenticator_SessionKey{SessionKey: sk}
	k.Keeper.SetAuthenticator(ctx, account, *a)
	return nil
}

// acceptGuardianThreshold verifies at least threshold valid signatures
// from distinct registered guardians over the exec's canonical signing
// bytes (see GuardianExecSigningBytes), then advances next_sequence so
// the same signatures can never be replayed.
func (k msgServer) acceptGuardianThreshold(ctx sdk.Context, account sdk.AccAddress, a *Authenticator, gt *GuardianThreshold, msg *MsgExecAuthenticated) error {
	signingBytes := GuardianExecSigningBytes(ctx.ChainID(), msg.Account, msg.AuthenticatorId, gt.NextSequence, msg.Msgs)

	registered := make(map[string]bool, len(gt.GuardianPubkeys))
	for _, pk := range gt.GuardianPubkeys {
		registered[string(pk)] = true
	}

	validDistinct := make(map[string]bool, len(msg.GuardianSignatures))
	for _, sig := range msg.GuardianSignatures {
		if !registered[string(sig.Pubkey)] {
			continue // not a registered guardian: silently ignored, same as an unrecognized signer would be
		}
		if (&mldsa.PubKey{Key: sig.Pubkey}).VerifySignature(signingBytes, sig.Signature) {
			validDistinct[string(sig.Pubkey)] = true
		}
	}
	if uint32(len(validDistinct)) < gt.Threshold {
		return sdkerrors.Wrapf(ErrThresholdNotMet, "need %d valid guardian signatures, got %d", gt.Threshold, len(validDistinct))
	}

	k.Keeper.saveGuardianSequence(ctx, account, *a, gt, gt.NextSequence+1)
	return nil
}

// GuardianExecSigningBytes is the exact byte string a guardian signs to
// authorize one MsgExecAuthenticated: domain-separated, covering the
// chain, account, authenticator, replay-protection sequence, and every
// inner message's type URL and raw bytes (in order) -- changing any of
// those invalidates every existing signature.
func GuardianExecSigningBytes(chainID, account string, authenticatorID uint64, sequence uint64, msgs []*cdctypes.Any) []byte {
	h := sha256.New()
	fmt.Fprintf(h, "aether-accountauth-exec/v1\n%s\n%s\n%d\n%d\n", chainID, account, authenticatorID, sequence)
	for _, m := range msgs {
		fmt.Fprintf(h, "%s\n%d\n", m.TypeUrl, len(m.Value))
		h.Write(m.Value)
	}
	return h.Sum(nil)
}
