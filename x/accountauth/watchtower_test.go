package accountauth

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func (f *rotFixture) watchtowerCancel(w rotKey) error {
	_, err := f.ms.WatchtowerCancel(f.ctx, &MsgWatchtowerCancel{Submitter: f.addr(), Account: f.addr(), WatchtowerPubkey: w.pub,
		Signature: w.sign(f.t, WatchtowerCancelSigningBytes(rotChain, f.addr(), f.state().Nonce))})
	return err
}

func (f *rotFixture) guardianSetRecovery(signers []rotKey, set RecoverySet) error {
	sb := GuardianSetRecoverySigningBytes(rotChain, f.addr(), f.state().RecoveryNonce, set)
	var sigs []*GuardianSignature
	for _, g := range signers {
		sigs = append(sigs, g.guardianSig(f.t, sb))
	}
	_, err := f.ms.GuardianSetRecovery(f.ctx, &MsgGuardianSetRecovery{Submitter: f.addr(), Account: f.addr(),
		GuardianPubkeys: set.GuardianPubkeys, Threshold: set.Threshold, WatchtowerPubkeys: set.WatchtowerPubkeys, GuardianSignatures: sigs})
	return err
}

// A watchtower needs guardians (who alone can remove it), and isn't one.
func TestWatchtower_SetChecks(t *testing.T) {
	f := newRotFixture(t, rotChain, 10)
	g, w := recoverySet(t, 2), recoverySet(t, 4)
	for name, m := range map[string]*MsgSetRecovery{
		"without guardians": {Account: f.addr(), WatchtowerPubkeys: pubs(w[:1])},
		"too many":          {Account: f.addr(), GuardianPubkeys: pubs(g), Threshold: 1, WatchtowerPubkeys: pubs(w)},
		"also a guardian":   {Account: f.addr(), GuardianPubkeys: pubs(g), Threshold: 1, WatchtowerPubkeys: [][]byte{g[0].pub}},
		"repeated":          {Account: f.addr(), GuardianPubkeys: pubs(g), Threshold: 1, WatchtowerPubkeys: [][]byte{w[0].pub, w[0].pub}},
		"short key":         {Account: f.addr(), GuardianPubkeys: pubs(g), Threshold: 1, WatchtowerPubkeys: [][]byte{w[0].pub[:8]}},
	} {
		_, err := f.ms.SetRecovery(f.ctx, m)
		require.ErrorIs(t, err, ErrInvalidAuthenticator, name)
	}
	f.installRecovery(g, 2, w[:MaxWatchtowers]...)
}

// A watchtower cancels what the current key schedules, a key change or a
// change to the set (a thief removing it), never at a stale nonce, and
// without touching the guardians' nonce.
func TestWatchtower_CancelsTheCurrentKeysChanges(t *testing.T) {
	f := newRotFixture(t, rotChain, 10)
	g, w := recoverySet(t, 2), newRotKey(t)
	f.installRecovery(g, 2, w)
	rn := f.state().RecoveryNonce

	require.ErrorIs(t, f.watchtowerCancel(w), ErrNothingPending)
	_, err := f.ms.RotateKey(f.ctx, f.rotateMsg(newRotKey(t), f.state().Nonce))
	require.NoError(t, err)
	stale := w.sign(t, WatchtowerCancelSigningBytes(rotChain, f.addr(), f.state().Nonce-1))
	_, err = f.ms.WatchtowerCancel(f.ctx, &MsgWatchtowerCancel{Submitter: f.addr(), Account: f.addr(), WatchtowerPubkey: w.pub, Signature: stale})
	require.ErrorIs(t, err, ErrNotWatchtower, "signed before the change it would cancel")
	require.ErrorIs(t, f.watchtowerCancel(newRotKey(t)), ErrNotWatchtower, "not a watchtower")
	require.ErrorIs(t, f.watchtowerCancel(g[0]), ErrNotWatchtower, "a guardian isn't a watchtower")

	require.NoError(t, f.watchtowerCancel(w))
	require.Nil(t, f.state().PendingRotation)
	require.True(t, hasEventType(f.ctx, EventTypeRotationCancelled))
	require.Equal(t, rn, f.state().RecoveryNonce, "the guardians' signatures stay good")

	// A thief holding the key tries to remove the set, watchtower and all.
	_, err = f.ms.SetRecovery(f.ctx, &MsgSetRecovery{Account: f.addr()})
	require.NoError(t, err)
	require.NoError(t, f.watchtowerCancel(w))
	require.Nil(t, f.state().PendingRecovery)
	require.NotNil(t, f.state().Recovery)
}

// Gitty's case: a watchtower gone bad cancels every change the owner
// makes. It can't stop the guardians, who remove it at once, after which
// its signatures count for nothing and the owner's change applies. It
// can't stop a recovery either: that applies at once.
func TestWatchtower_GuardiansOverrideAHostileOne(t *testing.T) {
	f := newRotFixture(t, rotChain, 10)
	g, bad := recoverySet(t, 2), newRotKey(t)
	f.installRecovery(g, 2, bad)

	_, err := f.ms.RotateKey(f.ctx, f.rotateMsg(newRotKey(t), f.state().Nonce))
	require.NoError(t, err)
	require.NoError(t, f.watchtowerCancel(bad), "the owner's own rotation, vetoed")

	// The guardians, at their threshold, drop the watchtower at once.
	keep := RecoverySet{GuardianPubkeys: pubs(g), Threshold: 2}
	require.ErrorIs(t, f.guardianSetRecovery(g[:1], keep), ErrThresholdNotMet)
	sb := GuardianSetRecoverySigningBytes(rotChain, f.addr(), f.state().RecoveryNonce, keep)
	drop := &MsgGuardianSetRecovery{Submitter: f.addr(), Account: f.addr(), GuardianPubkeys: keep.GuardianPubkeys, Threshold: 2,
		GuardianSignatures: []*GuardianSignature{g[0].guardianSig(t, sb), g[1].guardianSig(t, sb)}}
	_, err = f.ms.GuardianSetRecovery(f.ctx, drop)
	require.NoError(t, err)
	require.Empty(t, f.state().Recovery.WatchtowerPubkeys)
	_, err = f.ms.GuardianSetRecovery(f.ctx, drop)
	require.ErrorIs(t, err, ErrThresholdNotMet, "the same signatures don't replay")

	next := newRotKey(t)
	res, err := f.ms.RotateKey(f.ctx, f.rotateMsg(next, f.state().Nonce))
	require.NoError(t, err)
	require.ErrorIs(t, f.watchtowerCancel(bad), ErrNotWatchtower, "removed")
	f.at(res.ApplyHeight)
	f.k.ApplyDueRotations(f.ctx)
	require.Equal(t, next.pub, f.currentKey())

	// With a watchtower back in place, the guardians still recover at once.
	f.installRecovery(g, 2, bad)
	rescue := newRotKey(t)
	rn := f.state().RecoveryNonce
	sb = RecoverKeySigningBytes(rotChain, f.addr(), rn, rescue.pub)
	_, err = f.ms.RecoverKey(f.ctx, &MsgRecoverKey{Submitter: f.addr(), Account: f.addr(), NewPubkey: rescue.pub,
		NewKeySignature:    rescue.sign(t, RotateKeySigningBytes(rotChain, f.addr(), rn, rescue.pub)),
		GuardianSignatures: []*GuardianSignature{g[0].guardianSig(t, sb), g[1].guardianSig(t, sb)}})
	require.NoError(t, err)
	require.Equal(t, rescue.pub, f.currentKey())
	require.ErrorIs(t, f.watchtowerCancel(bad), ErrNothingPending, "nothing to cancel: it applied at once")
}

// The guardians replace the set at once: new guardians and threshold, or
// none, dropping the current key's pending set change; the old set's
// signatures then stop counting.
func TestGuardianSetRecovery(t *testing.T) {
	f := newRotFixture(t, rotChain, 10)
	g := recoverySet(t, 3)
	f.installRecovery(g, 2)
	_, err := f.ms.SetRecovery(f.ctx, &MsgSetRecovery{Account: f.addr()}) // a thief's removal, pending
	require.NoError(t, err)

	newSet := recoverySet(t, 2)
	w := newRotKey(t)
	set := RecoverySet{GuardianPubkeys: pubs(newSet), Threshold: 1, WatchtowerPubkeys: [][]byte{w.pub}}
	signed := GuardianSetRecoverySigningBytes(rotChain, f.addr(), f.state().RecoveryNonce, set)
	_, err = f.ms.GuardianSetRecovery(f.ctx, &MsgGuardianSetRecovery{Submitter: f.addr(), Account: f.addr(),
		GuardianPubkeys: pubs(newSet), Threshold: 2, WatchtowerPubkeys: [][]byte{w.pub},
		GuardianSignatures: []*GuardianSignature{g[0].guardianSig(t, signed), g[1].guardianSig(t, signed)}})
	require.ErrorIs(t, err, ErrThresholdNotMet, "signed for threshold 1, sent with 2")
	require.ErrorIs(t, f.guardianSetRecovery(g[:2], RecoverySet{GuardianPubkeys: pubs(newSet), Threshold: 1, WatchtowerPubkeys: [][]byte{newSet[0].pub}}), ErrInvalidAuthenticator, "a watchtower that's also a guardian")
	require.ErrorIs(t, f.guardianSetRecovery(g[:2], RecoverySet{WatchtowerPubkeys: [][]byte{w.pub}}), ErrInvalidAuthenticator, "a watchtower with no guardians")

	require.NoError(t, f.guardianSetRecovery(g[:2], set))
	st := f.state()
	require.Equal(t, pubs(newSet), st.Recovery.GuardianPubkeys)
	require.Equal(t, [][]byte{w.pub}, st.Recovery.WatchtowerPubkeys)
	require.Nil(t, st.PendingRecovery, "the pending removal is gone")
	require.ErrorIs(t, f.guardianSetRecovery(g[:2], RecoverySet{}), ErrThresholdNotMet, "the old guardians no longer count")

	require.NoError(t, f.guardianSetRecovery(newSet[:1], RecoverySet{}))
	require.Nil(t, f.state().Recovery, "removed, watchtower too")
	require.ErrorIs(t, f.guardianSetRecovery(newSet[:1], RecoverySet{}), ErrNoRecoverySet)
}

// Before the height both are refused, and neither runs through an
// authenticator.
func TestWatchtower_GateAndDelegation(t *testing.T) {
	f := newRotFixture(t, "aether-testnet-1", RotationActivationHeight-1)
	_, err := f.ms.WatchtowerCancel(f.ctx, &MsgWatchtowerCancel{Submitter: f.addr(), Account: f.addr()})
	require.ErrorIs(t, err, ErrRotationNotActive)
	_, err = f.ms.GuardianSetRecovery(f.ctx, &MsgGuardianSetRecovery{Submitter: f.addr(), Account: f.addr()})
	require.ErrorIs(t, err, ErrRotationNotActive)
	require.True(t, IsRotationMsg(&MsgWatchtowerCancel{}))
	require.True(t, IsRotationMsg(&MsgGuardianSetRecovery{}))
	require.True(t, IsRotationMsgTypeURL("/aether.accountauth.v1.MsgWatchtowerCancel"))
	require.True(t, IsRotationMsgTypeURL("/aether.accountauth.v1.MsgGuardianSetRecovery"))
}
