package accountauth

import (
	"context"
	"testing"

	"cosmossdk.io/log"
	"cosmossdk.io/store"
	"cosmossdk.io/store/metrics"
	storetypes "cosmossdk.io/store/types"
	tmproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/gates"
)

// fakeAccounts is x/auth's account store, enough for rotation.
type fakeAccounts map[string]sdk.AccountI

func (f fakeAccounts) GetAccount(_ context.Context, a sdk.AccAddress) sdk.AccountI {
	return f[string(a)]
}
func (f fakeAccounts) SetAccount(_ context.Context, acc sdk.AccountI) {
	f[string(acc.GetAddress())] = acc
}

type rotKey struct {
	priv *mldsa.PrivKey
	pub  []byte
}

func newRotKey(t *testing.T) rotKey {
	t.Helper()
	priv, err := mldsa.GenPrivKey()
	require.NoError(t, err)
	return rotKey{priv: priv, pub: priv.PubKey().Bytes()}
}

func (k rotKey) sign(t *testing.T, msg []byte) []byte {
	t.Helper()
	sig, err := k.priv.Sign(msg)
	require.NoError(t, err)
	return sig
}

func (k rotKey) guardianSig(t *testing.T, msg []byte) *GuardianSignature {
	return &GuardianSignature{Pubkey: k.pub, Signature: k.sign(t, msg)}
}

const rotChain = gates.RehearsalPrefix + "rotation" // every gate open from block 1

type rotFixture struct {
	t        *testing.T
	ms       MsgServer
	k        Keeper
	ctx      sdk.Context
	accounts fakeAccounts
	owner    rotKey
	account  sdk.AccAddress
}

func newRotFixture(t *testing.T, chainID string, height int64) *rotFixture {
	t.Helper()
	storeKey := storetypes.NewKVStoreKey(StoreKey)
	db := dbm.NewMemDB()
	cms := store.NewCommitMultiStore(db, log.NewNopLogger(), metrics.NewNoOpMetrics())
	cms.MountStoreWithDB(storeKey, storetypes.StoreTypeIAVL, db)
	require.NoError(t, cms.LoadLatestVersion())
	ctx := sdk.NewContext(cms, tmproto.Header{ChainID: chainID, Height: height}, false, log.NewNopLogger()).WithChainID(chainID).WithBlockHeight(height)

	accounts := fakeAccounts{}
	owner := newRotKey(t)
	account := sdk.AccAddress((&mldsa.PubKey{Key: owner.pub}).Address())
	acc := authtypes.NewBaseAccountWithAddress(account)
	require.NoError(t, acc.SetPubKey(&mldsa.PubKey{Key: owner.pub}))
	accounts.SetAccount(ctx, acc)

	k := NewKeeper(newTestCodec(), storeKey, fakeRouter{}).WithAccountKeeper(accounts)
	return &rotFixture{t: t, ms: NewMsgServerImpl(k), k: k, ctx: ctx, accounts: accounts, owner: owner, account: account}
}

func (f *rotFixture) addr() string { return f.account.String() }

func (f *rotFixture) state() RotationState { return f.k.GetRotationState(f.ctx, f.account) }

func (f *rotFixture) currentKey() []byte {
	return f.accounts.GetAccount(f.ctx, f.account).GetPubKey().Bytes()
}

func (f *rotFixture) rotateMsg(newKey rotKey, nonce uint64) *MsgRotateKey {
	return &MsgRotateKey{Account: f.addr(), NewPubkey: newKey.pub,
		NewKeySignature: newKey.sign(f.t, RotateKeySigningBytes(f.ctx.ChainID(), f.addr(), nonce, newKey.pub))}
}

func (f *rotFixture) at(height int64) {
	f.ctx = f.ctx.WithBlockHeight(height).WithEventManager(sdk.NewEventManager())
}

func hasEventType(ctx sdk.Context, typ string) bool {
	for _, e := range ctx.EventManager().Events() {
		if e.Type == typ {
			return true
		}
	}
	return false
}

// Before RotationActivationHeight every rotation message is refused and
// nothing applies.
func TestRotation_Gate(t *testing.T) {
	f := newRotFixture(t, "aether-testnet-1", RotationActivationHeight-1)
	_, err := f.ms.RotateKey(f.ctx, f.rotateMsg(newRotKey(t), 0))
	require.ErrorIs(t, err, ErrRotationNotActive)
	_, err = f.ms.SetRecovery(f.ctx, &MsgSetRecovery{Account: f.addr(), GuardianPubkeys: [][]byte{newRotKey(t).pub}, Threshold: 1})
	require.ErrorIs(t, err, ErrRotationNotActive)
	_, err = f.ms.CancelPendingRotation(f.ctx, &MsgCancelPendingRotation{Account: f.addr()})
	require.ErrorIs(t, err, ErrRotationNotActive)
	_, err = f.ms.RecoverKey(f.ctx, &MsgRecoverKey{Submitter: f.addr(), Account: f.addr()})
	require.ErrorIs(t, err, ErrRotationNotActive)
	_, err = f.ms.GuardianCancel(f.ctx, &MsgGuardianCancel{Submitter: f.addr(), Account: f.addr()})
	require.ErrorIs(t, err, ErrRotationNotActive)

	// At the height on the testnet, and from block 1 on a fresh chain.
	f.at(RotationActivationHeight)
	_, err = f.ms.RotateKey(f.ctx, f.rotateMsg(newRotKey(t), 0))
	require.NoError(t, err)
	g := newRotFixture(t, rotChain, 1)
	_, err = g.ms.RotateKey(g.ctx, g.rotateMsg(newRotKey(t), 0))
	require.NoError(t, err)
}

// A current-key rotation waits RotationDelayBlocks and applies at the end
// of that block, address unchanged.
func TestRotation_ScheduledThenApplied(t *testing.T) {
	f := newRotFixture(t, rotChain, 100)
	next := newRotKey(t)
	res, err := f.ms.RotateKey(f.ctx, f.rotateMsg(next, 0))
	require.NoError(t, err)
	require.Equal(t, int64(100)+RotationDelayBlocks, res.ApplyHeight)
	require.True(t, hasEventType(f.ctx, EventTypeRotationScheduled))
	st := f.state()
	require.Equal(t, uint64(1), st.Nonce)
	require.Equal(t, next.pub, st.PendingRotation.NewPubkey)

	// A second one while it's pending is refused.
	_, err = f.ms.RotateKey(f.ctx, f.rotateMsg(newRotKey(t), 1))
	require.ErrorIs(t, err, ErrRotationPending)

	f.at(res.ApplyHeight - 1)
	f.k.ApplyDueRotations(f.ctx)
	require.Equal(t, f.owner.pub, f.currentKey(), "not before its height")

	f.at(res.ApplyHeight)
	f.k.ApplyDueRotations(f.ctx)
	require.Equal(t, next.pub, f.currentKey())
	require.Equal(t, f.account, f.accounts.GetAccount(f.ctx, f.account).GetAddress(), "address unchanged")
	require.True(t, hasEventType(f.ctx, EventTypeRotationApplied))
	require.Nil(t, f.state().PendingRotation)

	// Nothing left due.
	it := f.ctx.KVStore(f.k.storeKey).Iterator(KeyRotationDuePrefix, storetypes.PrefixEndBytes(KeyRotationDuePrefix))
	require.False(t, it.Valid())
	it.Close()

	// The new key now rotates, at the advanced nonce.
	_, err = f.ms.RotateKey(f.ctx, f.rotateMsg(newRotKey(t), 1))
	require.NoError(t, err)
}

func TestRotation_NewKeyChecks(t *testing.T) {
	f := newRotFixture(t, rotChain, 10)
	next := newRotKey(t)
	for name, msg := range map[string]*MsgRotateKey{
		"proof at the wrong nonce": f.rotateMsg(next, 7),
		"proof by another key": {Account: f.addr(), NewPubkey: next.pub,
			NewKeySignature: newRotKey(t).sign(t, RotateKeySigningBytes(rotChain, f.addr(), 0, next.pub))},
		"proof on another chain": {Account: f.addr(), NewPubkey: next.pub,
			NewKeySignature: next.sign(t, RotateKeySigningBytes("aether-1", f.addr(), 0, next.pub))},
		"the current key": f.rotateMsg(f.owner, 0),
		"a short key":     {Account: f.addr(), NewPubkey: next.pub[:100], NewKeySignature: []byte{1}},
	} {
		_, err := f.ms.RotateKey(f.ctx, msg)
		require.ErrorIs(t, err, ErrInvalidNewKey, name)
	}
	// An account that never signed has no key to rotate.
	stranger := sdk.AccAddress("never_signed_account")
	f.accounts.SetAccount(f.ctx, authtypes.NewBaseAccountWithAddress(stranger))
	_, err := f.ms.RotateKey(f.ctx, &MsgRotateKey{Account: stranger.String(), NewPubkey: next.pub,
		NewKeySignature: next.sign(t, RotateKeySigningBytes(rotChain, stranger.String(), 0, next.pub))})
	require.ErrorIs(t, err, ErrInvalidAccount)
	require.Equal(t, uint64(0), f.state().Nonce, "nothing accepted")
}

// The current key cancels its own pending rotation; the old proof can't
// be replayed after.
func TestRotation_Cancel(t *testing.T) {
	f := newRotFixture(t, rotChain, 10)
	next := newRotKey(t)
	msg := f.rotateMsg(next, 0)
	res, err := f.ms.RotateKey(f.ctx, msg)
	require.NoError(t, err)
	_, err = f.ms.CancelPendingRotation(f.ctx, &MsgCancelPendingRotation{Account: f.addr()})
	require.NoError(t, err)
	require.Nil(t, f.state().PendingRotation)
	_, err = f.ms.CancelPendingRotation(f.ctx, &MsgCancelPendingRotation{Account: f.addr()})
	require.ErrorIs(t, err, ErrNothingPending)

	f.at(res.ApplyHeight)
	f.k.ApplyDueRotations(f.ctx)
	require.Equal(t, f.owner.pub, f.currentKey(), "a cancelled rotation never applies")

	_, err = f.ms.RotateKey(f.ctx, msg)
	require.ErrorIs(t, err, ErrInvalidNewKey, "its proof covered the old nonce")
}

func recoverySet(t *testing.T, n int) []rotKey {
	keys := make([]rotKey, n)
	for i := range keys {
		keys[i] = newRotKey(t)
	}
	return keys
}

func pubs(keys []rotKey) [][]byte {
	if len(keys) == 0 {
		return nil
	}
	out := make([][]byte, len(keys))
	for i, k := range keys {
		out[i] = k.pub
	}
	return out
}

// Setting a recovery set waits the delay too: a thief holding the key
// can't install guardians and recover at once.
func (f *rotFixture) installRecovery(guardians []rotKey, threshold uint32, watchtowers ...rotKey) {
	f.t.Helper()
	res, err := f.ms.SetRecovery(f.ctx, &MsgSetRecovery{Account: f.addr(), GuardianPubkeys: pubs(guardians), Threshold: threshold, WatchtowerPubkeys: pubs(watchtowers)})
	require.NoError(f.t, err)
	before := f.state().Recovery
	f.at(res.ApplyHeight - 1)
	f.k.ApplyDueRotations(f.ctx)
	require.Equal(f.t, before, f.state().Recovery, "not before its height")
	f.at(res.ApplyHeight)
	f.k.ApplyDueRotations(f.ctx)
	require.Equal(f.t, pubs(guardians), f.state().Recovery.GuardianPubkeys)
	require.Equal(f.t, threshold, f.state().Recovery.Threshold)
	require.Equal(f.t, pubs(watchtowers), f.state().Recovery.WatchtowerPubkeys)
	require.True(f.t, hasEventType(f.ctx, EventTypeRecoveryApplied))
}

func TestRotation_SetRecoveryChecks(t *testing.T) {
	f := newRotFixture(t, rotChain, 10)
	g := recoverySet(t, 3)
	for name, m := range map[string]*MsgSetRecovery{
		"threshold over N":   {Account: f.addr(), GuardianPubkeys: pubs(g), Threshold: 4},
		"threshold zero":     {Account: f.addr(), GuardianPubkeys: pubs(g), Threshold: 0},
		"duplicate guardian": {Account: f.addr(), GuardianPubkeys: [][]byte{g[0].pub, g[0].pub}, Threshold: 1},
		"short key":          {Account: f.addr(), GuardianPubkeys: [][]byte{g[0].pub[:10]}, Threshold: 1},
		"too many":           {Account: f.addr(), GuardianPubkeys: pubs(recoverySet(t, MaxRecoveryGuardians+1)), Threshold: 1},
	} {
		_, err := f.ms.SetRecovery(f.ctx, m)
		require.ErrorIs(t, err, ErrInvalidAuthenticator, name)
	}
	_, err := f.ms.SetRecovery(f.ctx, &MsgSetRecovery{Account: f.addr()})
	require.ErrorIs(t, err, ErrNoRecoverySet, "nothing to remove")

	f.installRecovery(g, 2)
	// Removal is delayed as well.
	res, err := f.ms.SetRecovery(f.ctx, &MsgSetRecovery{Account: f.addr()})
	require.NoError(t, err)
	f.at(res.ApplyHeight)
	f.k.ApplyDueRotations(f.ctx)
	require.Nil(t, f.state().Recovery)
}

// The guardians rotate at once, clearing any pending change, and their
// signatures can't be replayed.
func TestRotation_Recover(t *testing.T) {
	f := newRotFixture(t, rotChain, 10)
	g := recoverySet(t, 3)
	f.installRecovery(g, 2)
	thiefKey := newRotKey(t)
	_, err := f.ms.RotateKey(f.ctx, f.rotateMsg(thiefKey, f.state().Nonce)) // the thief, holding the key, schedules a takeover
	require.NoError(t, err)

	rescue := newRotKey(t)
	rn := f.state().RecoveryNonce
	sb := RecoverKeySigningBytes(rotChain, f.addr(), rn, rescue.pub)
	pop := rescue.sign(t, RotateKeySigningBytes(rotChain, f.addr(), rn, rescue.pub))
	msg := &MsgRecoverKey{Submitter: f.addr(), Account: f.addr(), NewPubkey: rescue.pub, NewKeySignature: pop,
		GuardianSignatures: []*GuardianSignature{g[0].guardianSig(t, sb)}}
	_, err = f.ms.RecoverKey(f.ctx, msg)
	require.ErrorIs(t, err, ErrThresholdNotMet, "one of two")

	msg.GuardianSignatures = append(msg.GuardianSignatures, g[0].guardianSig(t, sb))
	_, err = f.ms.RecoverKey(f.ctx, msg)
	require.ErrorIs(t, err, ErrThresholdNotMet, "the same guardian twice")

	outsider := newRotKey(t)
	msg.GuardianSignatures = []*GuardianSignature{g[0].guardianSig(t, sb), outsider.guardianSig(t, sb)}
	_, err = f.ms.RecoverKey(f.ctx, msg)
	require.ErrorIs(t, err, ErrThresholdNotMet, "an outsider")

	msg.GuardianSignatures = []*GuardianSignature{g[0].guardianSig(t, sb), g[2].guardianSig(t, sb)}
	_, err = f.ms.RecoverKey(f.ctx, msg)
	require.NoError(t, err)
	require.Equal(t, rescue.pub, f.currentKey(), "at once")
	require.Nil(t, f.state().PendingRotation, "the thief's rotation is gone")
	require.True(t, hasEventType(f.ctx, EventTypeKeyRecovered))

	_, err = f.ms.RecoverKey(f.ctx, msg)
	require.Error(t, err, "replayed")

	// The thief's rotation never applies.
	f.at(f.ctx.BlockHeight() + RotationDelayBlocks + 1)
	f.k.ApplyDueRotations(f.ctx)
	require.Equal(t, rescue.pub, f.currentKey())

	// Later the owner rotates on; the guardians' old approval can't put
	// the account back on the key it named.
	later := newRotKey(t)
	res, err := f.ms.RotateKey(f.ctx, f.rotateMsg(later, f.state().Nonce))
	require.NoError(t, err)
	f.at(res.ApplyHeight)
	f.k.ApplyDueRotations(f.ctx)
	require.Equal(t, later.pub, f.currentKey())
	_, err = f.ms.RecoverKey(f.ctx, msg)
	require.Error(t, err, "replayed after a later rotation")
	require.Equal(t, later.pub, f.currentKey())
}

// A thief holding the current key can advance the key nonce as often as
// it likes; the guardians' signatures cover the recovery nonce, which it
// can't move, so the recovery still goes through.
func TestRotation_ThiefCannotStaleTheGuardians(t *testing.T) {
	f := newRotFixture(t, rotChain, 10)
	g := recoverySet(t, 2)
	f.installRecovery(g, 2)
	rescue := newRotKey(t)
	rn := f.state().RecoveryNonce
	sb := RecoverKeySigningBytes(rotChain, f.addr(), rn, rescue.pub)
	msg := &MsgRecoverKey{Submitter: f.addr(), Account: f.addr(), NewPubkey: rescue.pub,
		NewKeySignature:    rescue.sign(t, RotateKeySigningBytes(rotChain, f.addr(), rn, rescue.pub)),
		GuardianSignatures: []*GuardianSignature{g[0].guardianSig(t, sb), g[1].guardianSig(t, sb)}}

	for i := 0; i < 5; i++ {
		_, err := f.ms.RotateKey(f.ctx, f.rotateMsg(newRotKey(t), f.state().Nonce))
		require.NoError(t, err)
		_, err = f.ms.CancelPendingRotation(f.ctx, &MsgCancelPendingRotation{Account: f.addr()})
		require.NoError(t, err)
	}
	require.Equal(t, rn, f.state().RecoveryNonce)
	_, err := f.ms.RecoverKey(f.ctx, msg)
	require.NoError(t, err)
	require.Equal(t, rescue.pub, f.currentKey())
}

// The guardians cancel a pending key and recovery-set change; signatures
// for the old set stop working once a new set applies.
func TestRotation_GuardianCancel(t *testing.T) {
	f := newRotFixture(t, rotChain, 10)
	g := recoverySet(t, 2)
	f.installRecovery(g, 1)
	_, err := f.ms.RotateKey(f.ctx, f.rotateMsg(newRotKey(t), f.state().Nonce))
	require.NoError(t, err)
	_, err = f.ms.SetRecovery(f.ctx, &MsgSetRecovery{Account: f.addr()}) // the thief tries to remove them
	require.NoError(t, err)

	sb := GuardianCancelSigningBytes(rotChain, f.addr(), f.state().RecoveryNonce)
	_, err = f.ms.GuardianCancel(f.ctx, &MsgGuardianCancel{Submitter: f.addr(), Account: f.addr(),
		GuardianSignatures: []*GuardianSignature{newRotKey(t).guardianSig(t, sb)}})
	require.ErrorIs(t, err, ErrThresholdNotMet)
	_, err = f.ms.GuardianCancel(f.ctx, &MsgGuardianCancel{Submitter: f.addr(), Account: f.addr(),
		GuardianSignatures: []*GuardianSignature{g[1].guardianSig(t, sb)}})
	require.NoError(t, err)
	st := f.state()
	require.Nil(t, st.PendingRotation)
	require.Nil(t, st.PendingRecovery)
	require.NotNil(t, st.Recovery, "the set stays")

	// A new set: the old guardians' signatures no longer count.
	newSet := recoverySet(t, 1)
	oldRN := f.state().RecoveryNonce
	f.installRecovery(newSet, 1)
	require.Equal(t, oldRN+1, f.state().RecoveryNonce)
	_, err = f.ms.RotateKey(f.ctx, f.rotateMsg(newRotKey(t), f.state().Nonce))
	require.NoError(t, err)
	stale := GuardianCancelSigningBytes(rotChain, f.addr(), oldRN)
	_, err = f.ms.GuardianCancel(f.ctx, &MsgGuardianCancel{Submitter: f.addr(), Account: f.addr(),
		GuardianSignatures: []*GuardianSignature{g[1].guardianSig(t, stale)}})
	require.ErrorIs(t, err, ErrThresholdNotMet)
}

// Without a recovery set, the guardian paths are refused.
func TestRotation_NoRecoverySet(t *testing.T) {
	f := newRotFixture(t, rotChain, 10)
	_, err := f.ms.GuardianCancel(f.ctx, &MsgGuardianCancel{Submitter: f.addr(), Account: f.addr()})
	require.ErrorIs(t, err, ErrNoRecoverySet)
	_, err = f.ms.RecoverKey(f.ctx, &MsgRecoverKey{Submitter: f.addr(), Account: f.addr(), NewPubkey: newRotKey(t).pub})
	require.ErrorIs(t, err, ErrNoRecoverySet)
}

// Neither a session key nor a guardian_threshold authenticator can run a
// rotation message.
func TestRotation_NotThroughAuthenticators(t *testing.T) {
	f := newRotFixture(t, rotChain, 10)
	_, sessionPub, sessionAddr := genSessionKey(t)
	reg, err := f.ms.RegisterAuthenticator(f.ctx, &MsgRegisterAuthenticator{Account: f.addr(), Authenticator: &Authenticator{
		Kind: &Authenticator_SessionKey{SessionKey: &SessionKey{Pubkey: sessionPub, ExpiresAtUnix: f.ctx.BlockTime().Unix() + 3600,
			AllowedMsgTypes: []string{sdk.MsgTypeURL(&MsgRotateKey{})}, SpendLimitUaeth: "0"}}}})
	require.NoError(t, err)
	_, err = f.ms.ExecAuthenticated(f.ctx, &MsgExecAuthenticated{Signer: sessionAddr.String(), Account: f.addr(),
		AuthenticatorId: reg.Id, Msgs: []*codectypes.Any{anyOf(t, f.rotateMsg(newRotKey(t), 0))}})
	require.ErrorIs(t, err, ErrRotationNotDelegable)
	require.Nil(t, f.state().PendingRotation)
}
