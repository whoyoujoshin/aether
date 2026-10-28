package accountauth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"

	sdkerrors "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/gates"
)

// Key rotation (docs/PQ-KEY-ROTATION.md). An Aether address is the hash
// of its first ML-DSA-44 key, so a new key would mean a new account. Here
// the account keeps its address and its key changes: the account's
// stored public key becomes the new one, which the signature check
// (SigVerificationDecorator) uses from then on, and app.
// RotatingSetPubKeyDecorator accepts a transaction that carries it.
//
//   - The current key schedules a rotation (MsgRotateKey) that applies
//     RotationDelayBlocks later, at the end of that block. Until then
//     the old key still signs, so the delay protects the account from
//     being taken over, not its balance: a leaked key can spend until
//     the change applies.
//   - A recovery set (MsgSetRecovery, also delayed) of M of N guardians
//     can rotate at once (MsgRecoverKey) and cancel any pending change
//     (MsgGuardianCancel). Without one, the current key can cancel its
//     own pending change (MsgCancelPendingRotation); an owner and a thief
//     holding the same key can only cancel each other.
//   - Every rotation proves possession of the new key, and every
//     signature covers the chain ID, the account and a nonce. The current
//     key's changes advance nonce; the guardians' actions and recovery-set
//     changes advance recovery_nonce, which the guardians' signatures
//     cover, so the current key can't keep making them stale.
//
// A guardian_threshold authenticator spends; it never rotates.

// RotationActivationHeight switches rotation on: its messages are refused
// before it, the ante decorator keeps the stock check, and nothing is
// applied. A placeholder far past the testnet's tip until an upgrade
// sets it; fresh chains have it from block 1.
const RotationActivationHeight int64 = 40_000_000

// RotationDelayBlocks is how long a current-key rotation or a
// recovery-set change waits before it applies: about a day at 5.64 s
// blocks, long enough for guardians to notice and cancel.
const RotationDelayBlocks int64 = 15_300

// MaxRecoveryGuardians bounds a recovery set.
const MaxRecoveryGuardians = 10

// Event types and attributes.
const (
	EventTypeRotationScheduled = "key_rotation_scheduled"
	EventTypeRotationApplied   = "key_rotation_applied"
	EventTypeRotationFailed    = "key_rotation_failed"
	EventTypeRotationCancelled = "key_rotation_cancelled"
	EventTypeRecoveryScheduled = "recovery_set_scheduled"
	EventTypeRecoveryApplied   = "recovery_set_applied"
	EventTypeKeyRecovered      = "key_recovered"

	AttributeAccount     = "account"
	AttributeApplyHeight = "apply_height"
	AttributeNewPubkey   = "new_pubkey_sha256"
	AttributeReason      = "reason"
)

var (
	// KeyRotationPrefix/<account> -> RotationState.
	KeyRotationPrefix = []byte("rotation/")
	// KeyRotationDuePrefix/<apply height, big-endian>/<account> -> {}:
	// the EndBlocker reads only the heights that are due.
	KeyRotationDuePrefix = []byte("rotation_due/")
)

// AccountKeeper is what rotation needs of x/auth.
type AccountKeeper interface {
	GetAccount(ctx context.Context, addr sdk.AccAddress) sdk.AccountI
	SetAccount(ctx context.Context, acc sdk.AccountI)
}

// WithAccountKeeper gives the keeper the auth keeper rotation reads and
// writes accounts through.
func (k Keeper) WithAccountKeeper(ak AccountKeeper) Keeper {
	k.ak = ak
	return k
}

func rotationKey(account []byte) []byte {
	return append(append([]byte{}, KeyRotationPrefix...), account...)
}

func rotationDueKey(height int64, account []byte) []byte {
	key := append(append([]byte{}, KeyRotationDuePrefix...), sdk.Uint64ToBigEndian(uint64(height))...)
	return append(key, account...)
}

// GetRotationState is the account's rotation record, empty if it has none.
func (k Keeper) GetRotationState(ctx sdk.Context, account sdk.AccAddress) RotationState {
	bz := ctx.KVStore(k.storeKey).Get(rotationKey(account))
	if bz == nil {
		return RotationState{}
	}
	var st RotationState
	k.cdc.MustUnmarshal(bz, &st)
	return st
}

func (k Keeper) setRotationState(ctx sdk.Context, account sdk.AccAddress, st RotationState) {
	ctx.KVStore(k.storeKey).Set(rotationKey(account), k.cdc.MustMarshal(&st))
}

func (k Keeper) setDue(ctx sdk.Context, height int64, account sdk.AccAddress) {
	ctx.KVStore(k.storeKey).Set(rotationDueKey(height, account), []byte{1})
}

func (k Keeper) clearDue(ctx sdk.Context, height int64, account sdk.AccAddress) {
	ctx.KVStore(k.storeKey).Delete(rotationDueKey(height, account))
}

// clearPending drops the account's pending key and recovery-set changes.
// It reports whether there was one.
func (k Keeper) clearPending(ctx sdk.Context, account sdk.AccAddress, st *RotationState) bool {
	had := false
	if st.PendingRotation != nil {
		k.clearDue(ctx, st.PendingRotation.ApplyHeight, account)
		st.PendingRotation = nil
		had = true
	}
	if st.PendingRecovery != nil {
		k.clearDue(ctx, st.PendingRecovery.ApplyHeight, account)
		st.PendingRecovery = nil
		had = true
	}
	return had
}

// RotateKeySigningBytes is what the new key signs to prove it is held:
// domain-separated, over the chain, the account, a nonce and the new key
// itself. MsgRotateKey's covers the state's nonce, MsgRecoverKey's its
// recovery_nonce.
func RotateKeySigningBytes(chainID, account string, nonce uint64, newPubkey []byte) []byte {
	h := sha256.New()
	fmt.Fprintf(h, "aether-accountauth-rotate/v1\n%s\n%s\n%d\n%s\n", chainID, account, nonce, hex.EncodeToString(newPubkey))
	return h.Sum(nil)
}

// RecoverKeySigningBytes is what each recovery guardian signs, at the
// state's recovery_nonce, to rotate the account to newPubkey at once.
func RecoverKeySigningBytes(chainID, account string, nonce uint64, newPubkey []byte) []byte {
	h := sha256.New()
	fmt.Fprintf(h, "aether-accountauth-recover/v1\n%s\n%s\n%d\n%s\n", chainID, account, nonce, hex.EncodeToString(newPubkey))
	return h.Sum(nil)
}

// GuardianCancelSigningBytes is what each recovery guardian signs, at the
// state's recovery_nonce, to cancel the account's pending changes.
func GuardianCancelSigningBytes(chainID, account string, nonce uint64) []byte {
	h := sha256.New()
	fmt.Fprintf(h, "aether-accountauth-guardian-cancel/v1\n%s\n%s\n%d\n", chainID, account, nonce)
	return h.Sum(nil)
}

func pubkeyDigest(pk []byte) string {
	sum := sha256.Sum256(pk)
	return hex.EncodeToString(sum[:])
}

func rotationActive(ctx sdk.Context) error {
	if !gates.On(ctx, RotationActivationHeight) {
		return sdkerrors.Wrapf(ErrRotationNotActive, "key rotation starts at height %d", gates.Height(ctx.ChainID(), RotationActivationHeight))
	}
	return nil
}

// rotatingAccount is the account being rotated, which must exist and
// already have a key on chain (it has signed a transaction).
func (k Keeper) rotatingAccount(ctx sdk.Context, addr string) (sdk.AccAddress, sdk.AccountI, error) {
	account, err := sdk.AccAddressFromBech32(addr)
	if err != nil {
		return nil, nil, sdkerrors.Wrapf(ErrInvalidAccount, "invalid account address %q: %s", addr, err)
	}
	if k.ak == nil {
		return nil, nil, sdkerrors.Wrap(ErrRotationNotActive, "no account keeper")
	}
	acc := k.ak.GetAccount(ctx, account)
	if acc == nil || acc.GetPubKey() == nil {
		return nil, nil, sdkerrors.Wrapf(ErrInvalidAccount, "%s has no key on chain yet", addr)
	}
	return account, acc, nil
}

// checkNewKey: an ML-DSA-44 key, not the account's current one, and its
// signature over RotateKeySigningBytes.
func checkNewKey(ctx sdk.Context, account string, acc sdk.AccountI, nonce uint64, newPubkey, sig []byte) error {
	if len(newPubkey) != mldsa.PubKeySize {
		return sdkerrors.Wrapf(ErrInvalidNewKey, "new key must be exactly %d bytes, got %d", mldsa.PubKeySize, len(newPubkey))
	}
	if bytes.Equal(acc.GetPubKey().Bytes(), newPubkey) {
		return sdkerrors.Wrap(ErrInvalidNewKey, "new key is the account's current key")
	}
	if !(&mldsa.PubKey{Key: newPubkey}).VerifySignature(RotateKeySigningBytes(ctx.ChainID(), account, nonce, newPubkey), sig) {
		return sdkerrors.Wrap(ErrInvalidNewKey, "new_key_signature doesn't verify: sign RotateKeySigningBytes with the new key at the current nonce")
	}
	return nil
}

// validRecoverySet checks a proposed set; an empty one (removal) is valid.
func validRecoverySet(pubkeys [][]byte, threshold uint32) error {
	if len(pubkeys) == 0 && threshold == 0 {
		return nil
	}
	if len(pubkeys) > MaxRecoveryGuardians {
		return sdkerrors.Wrapf(ErrInvalidAuthenticator, "at most %d recovery guardians", MaxRecoveryGuardians)
	}
	seen := make(map[string]bool, len(pubkeys))
	for _, pk := range pubkeys {
		if len(pk) != mldsa.PubKeySize {
			return sdkerrors.Wrapf(ErrInvalidAuthenticator, "guardian pubkey must be exactly %d bytes, got %d", mldsa.PubKeySize, len(pk))
		}
		if seen[string(pk)] {
			return sdkerrors.Wrap(ErrInvalidAuthenticator, "duplicate guardian pubkey")
		}
		seen[string(pk)] = true
	}
	if threshold == 0 || int(threshold) > len(pubkeys) {
		return sdkerrors.Wrapf(ErrInvalidAuthenticator, "threshold must be between 1 and %d", len(pubkeys))
	}
	return nil
}

// recoveryApproves: at least the threshold of distinct guardians in set
// signed signingBytes.
func recoveryApproves(set *RecoverySet, signingBytes []byte, sigs []*GuardianSignature) error {
	if set == nil || len(set.GuardianPubkeys) == 0 {
		return sdkerrors.Wrap(ErrNoRecoverySet, "the account has no recovery set")
	}
	registered := make(map[string]bool, len(set.GuardianPubkeys))
	for _, pk := range set.GuardianPubkeys {
		registered[string(pk)] = true
	}
	valid := map[string]bool{}
	for _, s := range sigs {
		if s == nil || !registered[string(s.Pubkey)] {
			continue
		}
		if (&mldsa.PubKey{Key: s.Pubkey}).VerifySignature(signingBytes, s.Signature) {
			valid[string(s.Pubkey)] = true
		}
	}
	if uint32(len(valid)) < set.Threshold {
		return sdkerrors.Wrapf(ErrThresholdNotMet, "need %d valid recovery guardian signatures, got %d", set.Threshold, len(valid))
	}
	return nil
}

func (k msgServer) RotateKey(goCtx context.Context, msg *MsgRotateKey) (*MsgRotateKeyResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	if err := rotationActive(ctx); err != nil {
		return nil, err
	}
	account, acc, err := k.rotatingAccount(ctx, msg.Account)
	if err != nil {
		return nil, err
	}
	st := k.GetRotationState(ctx, account)
	if st.PendingRotation != nil {
		return nil, sdkerrors.Wrapf(ErrRotationPending, "a rotation applies at %d; cancel it first", st.PendingRotation.ApplyHeight)
	}
	if err := checkNewKey(ctx, msg.Account, acc, st.Nonce, msg.NewPubkey, msg.NewKeySignature); err != nil {
		return nil, err
	}
	apply := ctx.BlockHeight() + RotationDelayBlocks
	st.PendingRotation = &PendingRotation{NewPubkey: msg.NewPubkey, ApplyHeight: apply}
	st.Nonce++
	k.setRotationState(ctx, account, st)
	k.setDue(ctx, apply, account)
	ctx.EventManager().EmitEvent(sdk.NewEvent(EventTypeRotationScheduled,
		sdk.NewAttribute(AttributeAccount, msg.Account),
		sdk.NewAttribute(AttributeApplyHeight, strconv.FormatInt(apply, 10)),
		sdk.NewAttribute(AttributeNewPubkey, pubkeyDigest(msg.NewPubkey))))
	return &MsgRotateKeyResponse{ApplyHeight: apply}, nil
}

func (k msgServer) CancelPendingRotation(goCtx context.Context, msg *MsgCancelPendingRotation) (*MsgCancelPendingRotationResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	if err := rotationActive(ctx); err != nil {
		return nil, err
	}
	account, _, err := k.rotatingAccount(ctx, msg.Account)
	if err != nil {
		return nil, err
	}
	st := k.GetRotationState(ctx, account)
	if !k.clearPending(ctx, account, &st) {
		return nil, sdkerrors.Wrap(ErrNothingPending, "no pending key or recovery-set change")
	}
	st.Nonce++
	k.setRotationState(ctx, account, st)
	ctx.EventManager().EmitEvent(sdk.NewEvent(EventTypeRotationCancelled,
		sdk.NewAttribute(AttributeAccount, msg.Account), sdk.NewAttribute(AttributeReason, "current_key")))
	return &MsgCancelPendingRotationResponse{}, nil
}

func (k msgServer) SetRecovery(goCtx context.Context, msg *MsgSetRecovery) (*MsgSetRecoveryResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	if err := rotationActive(ctx); err != nil {
		return nil, err
	}
	account, _, err := k.rotatingAccount(ctx, msg.Account)
	if err != nil {
		return nil, err
	}
	if err := validRecoverySet(msg.GuardianPubkeys, msg.Threshold); err != nil {
		return nil, err
	}
	st := k.GetRotationState(ctx, account)
	if st.PendingRecovery != nil {
		return nil, sdkerrors.Wrapf(ErrRotationPending, "a recovery-set change applies at %d; cancel it first", st.PendingRecovery.ApplyHeight)
	}
	if len(msg.GuardianPubkeys) == 0 && st.Recovery == nil {
		return nil, sdkerrors.Wrap(ErrNoRecoverySet, "no recovery set to remove")
	}
	apply := ctx.BlockHeight() + RotationDelayBlocks
	st.PendingRecovery = &PendingRecovery{Recovery: &RecoverySet{GuardianPubkeys: msg.GuardianPubkeys, Threshold: msg.Threshold}, ApplyHeight: apply}
	st.Nonce++
	k.setRotationState(ctx, account, st)
	k.setDue(ctx, apply, account)
	ctx.EventManager().EmitEvent(sdk.NewEvent(EventTypeRecoveryScheduled,
		sdk.NewAttribute(AttributeAccount, msg.Account),
		sdk.NewAttribute(AttributeApplyHeight, strconv.FormatInt(apply, 10))))
	return &MsgSetRecoveryResponse{ApplyHeight: apply}, nil
}

func (k msgServer) RecoverKey(goCtx context.Context, msg *MsgRecoverKey) (*MsgRecoverKeyResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	if err := rotationActive(ctx); err != nil {
		return nil, err
	}
	account, acc, err := k.rotatingAccount(ctx, msg.Account)
	if err != nil {
		return nil, err
	}
	st := k.GetRotationState(ctx, account)
	if err := recoveryApproves(st.Recovery, RecoverKeySigningBytes(ctx.ChainID(), msg.Account, st.RecoveryNonce, msg.NewPubkey), msg.GuardianSignatures); err != nil {
		return nil, err
	}
	if err := checkNewKey(ctx, msg.Account, acc, st.RecoveryNonce, msg.NewPubkey, msg.NewKeySignature); err != nil {
		return nil, err
	}
	if err := acc.SetPubKey(&mldsa.PubKey{Key: msg.NewPubkey}); err != nil {
		return nil, sdkerrors.Wrap(ErrInvalidNewKey, err.Error())
	}
	k.ak.SetAccount(ctx, acc)
	k.clearPending(ctx, account, &st)
	st.Nonce++
	st.RecoveryNonce++
	k.setRotationState(ctx, account, st)
	ctx.EventManager().EmitEvent(sdk.NewEvent(EventTypeKeyRecovered,
		sdk.NewAttribute(AttributeAccount, msg.Account),
		sdk.NewAttribute(AttributeNewPubkey, pubkeyDigest(msg.NewPubkey))))
	return &MsgRecoverKeyResponse{}, nil
}

func (k msgServer) GuardianCancel(goCtx context.Context, msg *MsgGuardianCancel) (*MsgGuardianCancelResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	if err := rotationActive(ctx); err != nil {
		return nil, err
	}
	account, _, err := k.rotatingAccount(ctx, msg.Account)
	if err != nil {
		return nil, err
	}
	st := k.GetRotationState(ctx, account)
	if err := recoveryApproves(st.Recovery, GuardianCancelSigningBytes(ctx.ChainID(), msg.Account, st.RecoveryNonce), msg.GuardianSignatures); err != nil {
		return nil, err
	}
	if !k.clearPending(ctx, account, &st) {
		return nil, sdkerrors.Wrap(ErrNothingPending, "no pending key or recovery-set change")
	}
	st.Nonce++
	st.RecoveryNonce++
	k.setRotationState(ctx, account, st)
	ctx.EventManager().EmitEvent(sdk.NewEvent(EventTypeRotationCancelled,
		sdk.NewAttribute(AttributeAccount, msg.Account), sdk.NewAttribute(AttributeReason, "recovery_guardians")))
	return &MsgGuardianCancelResponse{}, nil
}

// ApplyDueRotations applies every pending change whose height has come,
// at the end of the block (app.EndBlocker, from RotationActivationHeight).
// A key change sets the account's key; a recovery-set change replaces or
// removes the set. Entries are read only for heights up to this one and
// removed as they're handled.
func (k Keeper) ApplyDueRotations(ctx sdk.Context) {
	if k.ak == nil || !gates.On(ctx, RotationActivationHeight) {
		return
	}
	store := ctx.KVStore(k.storeKey)
	end := append(append([]byte{}, KeyRotationDuePrefix...), sdk.Uint64ToBigEndian(uint64(ctx.BlockHeight()+1))...)
	it := store.Iterator(KeyRotationDuePrefix, end)
	var due [][]byte
	for ; it.Valid(); it.Next() {
		due = append(due, bytes.Clone(it.Key()))
	}
	it.Close()

	for _, key := range due {
		store.Delete(key)
		account := sdk.AccAddress(key[len(KeyRotationDuePrefix)+8:])
		st := k.GetRotationState(ctx, account)
		changed := false
		if p := st.PendingRotation; p != nil && p.ApplyHeight <= ctx.BlockHeight() {
			st.PendingRotation = nil
			changed = true
			k.applyRotation(ctx, account, p)
		}
		if p := st.PendingRecovery; p != nil && p.ApplyHeight <= ctx.BlockHeight() {
			st.PendingRecovery = nil
			changed = true
			if p.Recovery == nil || len(p.Recovery.GuardianPubkeys) == 0 {
				st.Recovery = nil
			} else {
				st.Recovery = p.Recovery
			}
			// Signatures made for the old set don't carry over.
			st.RecoveryNonce++
			ctx.EventManager().EmitEvent(sdk.NewEvent(EventTypeRecoveryApplied, sdk.NewAttribute(AttributeAccount, account.String())))
		}
		if changed {
			k.setRotationState(ctx, account, st)
		}
	}
}

func (k Keeper) applyRotation(ctx sdk.Context, account sdk.AccAddress, p *PendingRotation) {
	fail := func(reason string) {
		ctx.EventManager().EmitEvent(sdk.NewEvent(EventTypeRotationFailed,
			sdk.NewAttribute(AttributeAccount, account.String()), sdk.NewAttribute(AttributeReason, reason)))
	}
	acc := k.ak.GetAccount(ctx, account)
	if acc == nil {
		fail("account not found")
		return
	}
	if err := acc.SetPubKey(&mldsa.PubKey{Key: p.NewPubkey}); err != nil {
		fail(err.Error())
		return
	}
	k.ak.SetAccount(ctx, acc)
	ctx.EventManager().EmitEvent(sdk.NewEvent(EventTypeRotationApplied,
		sdk.NewAttribute(AttributeAccount, account.String()),
		sdk.NewAttribute(AttributeNewPubkey, pubkeyDigest(p.NewPubkey))))
}

// IsRotationMsg: the messages that change who signs for an account.
// ExecAuthenticated refuses to run them, so neither a session key nor a
// guardian_threshold authenticator can rotate, cancel or change recovery;
// the app's ante handler refuses them anywhere but at the top level of a
// transaction (app/rotation_ante.go).
func IsRotationMsg(m sdk.Msg) bool {
	switch m.(type) {
	case *MsgRotateKey, *MsgCancelPendingRotation, *MsgSetRecovery, *MsgRecoverKey, *MsgGuardianCancel:
		return true
	}
	return false
}

// IsRotationMsgTypeURL is IsRotationMsg by type URL, for authz grants.
func IsRotationMsgTypeURL(typeURL string) bool {
	switch typeURL {
	case sdk.MsgTypeURL(&MsgRotateKey{}), sdk.MsgTypeURL(&MsgCancelPendingRotation{}), sdk.MsgTypeURL(&MsgSetRecovery{}),
		sdk.MsgTypeURL(&MsgRecoverKey{}), sdk.MsgTypeURL(&MsgGuardianCancel{}):
		return true
	}
	return false
}

func (q queryServer) Rotation(goCtx context.Context, req *QueryRotationRequest) (*QueryRotationResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	account, err := sdk.AccAddressFromBech32(req.Account)
	if err != nil {
		return nil, sdkerrors.Wrapf(ErrInvalidAccount, "invalid account address %q: %s", req.Account, err)
	}
	st := q.Keeper.GetRotationState(ctx, account)
	res := &QueryRotationResponse{State: &st, Active: gates.On(ctx, RotationActivationHeight), DelayBlocks: RotationDelayBlocks}
	if q.Keeper.ak != nil {
		if acc := q.Keeper.ak.GetAccount(ctx, account); acc != nil && acc.GetPubKey() != nil {
			res.CurrentPubkey = acc.GetPubKey().Bytes()
		}
	}
	return res, nil
}
