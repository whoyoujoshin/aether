package accountauth

import (
	"testing"
	"time"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
)

// A thief holding the account's key plants a session key and a
// guardian_threshold authenticator of its own (GHSA-cgcr-f6g2-ch5v).
type planted struct {
	sessionID, guardianID uint64
	sessionAddr           sdk.AccAddress
	guardian              *mldsa.PrivKey
	guardianPub           []byte
}

func (f *rotFixture) plant() planted {
	f.t.Helper()
	var p planted
	_, sessionPub, sessionAddr := genSessionKey(f.t)
	p.sessionAddr = sessionAddr
	p.sessionID = registerSessionKey(f.t, f.ms, f.ctx, f.account, sessionPub,
		[]string{sdk.MsgTypeURL(&banktypes.MsgSend{})}, "1000000000000", 1000*time.Hour)
	p.guardian, p.guardianPub, _ = genSessionKey(f.t)
	reg, err := f.ms.RegisterAuthenticator(f.ctx, &MsgRegisterAuthenticator{Account: f.addr(), Authenticator: &Authenticator{
		Kind: &Authenticator_GuardianThreshold{GuardianThreshold: &GuardianThreshold{GuardianPubkeys: [][]byte{p.guardianPub}, Threshold: 1}}}})
	require.NoError(f.t, err)
	p.guardianID = reg.Id
	return p
}

// drain tries both planted authenticators and returns their errors.
func (f *rotFixture) drain(p planted) (sessionErr, guardianErr error) {
	f.t.Helper()
	send := anyOf(f.t, &banktypes.MsgSend{FromAddress: f.addr(), ToAddress: p.sessionAddr.String(), Amount: sdk.NewCoins(sdk.NewInt64Coin("uaeth", 9_999_999))})
	_, sessionErr = f.ms.ExecAuthenticated(f.ctx, &MsgExecAuthenticated{Signer: p.sessionAddr.String(), Account: f.addr(),
		AuthenticatorId: p.sessionID, Msgs: []*codectypes.Any{send}})
	a, found := f.k.GetAuthenticator(f.ctx, f.account, p.guardianID)
	require.True(f.t, found)
	seq := a.GetGuardianThreshold().NextSequence
	sig, err := p.guardian.Sign(GuardianExecSigningBytes(f.ctx.ChainID(), f.addr(), p.guardianID, seq, []*codectypes.Any{send}))
	require.NoError(f.t, err)
	_, guardianErr = f.ms.ExecAuthenticated(f.ctx, &MsgExecAuthenticated{Signer: p.sessionAddr.String(), Account: f.addr(),
		AuthenticatorId: p.guardianID, Msgs: []*codectypes.Any{send},
		GuardianSignatures: []*GuardianSignature{{Pubkey: p.guardianPub, Signature: sig}}})
	return sessionErr, guardianErr
}

func (f *rotFixture) listed() int {
	f.t.Helper()
	res, err := NewQueryServerImpl(f.k).Authenticators(f.ctx, &QueryAuthenticatorsRequest{Account: f.addr()})
	require.NoError(f.t, err)
	return len(res.Authenticators)
}

func (f *rotFixture) rotateAndApply(to rotKey) {
	f.t.Helper()
	res, err := f.ms.RotateKey(f.ctx, f.rotateMsg(to, f.state().Nonce))
	require.NoError(f.t, err)
	f.at(res.ApplyHeight)
	f.k.ApplyDueRotations(f.ctx)
	require.Equal(f.t, to.pub, f.currentKey())
}

// The report's case: after the owner rotates, nothing the thief planted
// with the old key acts for the account any more. What the new key
// registers does.
func TestKeyChange_RotationRetiresAuthenticators(t *testing.T) {
	f := newRotFixture(t, rotChain, 10)
	p := f.plant()
	s, g := f.drain(p)
	require.NoError(t, s, "the session key works before the change")
	require.NoError(t, g)
	require.Equal(t, uint64(0), f.k.KeyGeneration(f.ctx, f.account))

	f.rotateAndApply(newRotKey(t))
	require.Equal(t, uint64(1), f.k.KeyGeneration(f.ctx, f.account))
	require.True(t, hasEventType(f.ctx, EventTypeDelegationsRetired))
	s, g = f.drain(p)
	require.ErrorIs(t, s, ErrDelegationRetired)
	require.ErrorIs(t, g, ErrDelegationRetired)
	require.Zero(t, f.listed(), "retired authenticators aren't listed")

	// Registered again under the new key, they work again.
	q := f.plant()
	s, g = f.drain(q)
	require.NoError(t, s)
	require.NoError(t, g)
	require.Equal(t, 2, f.listed())
	s, _ = f.drain(p)
	require.ErrorIs(t, s, ErrDelegationRetired, "the old ones stay retired")
}

// The same after a guardian recovery, which changes the key at once.
func TestKeyChange_RecoveryRetiresAuthenticators(t *testing.T) {
	f := newRotFixture(t, rotChain, 10)
	guardians := recoverySet(t, 2)
	f.installRecovery(guardians, 2)
	p := f.plant()

	rescue := newRotKey(t)
	rn := f.state().RecoveryNonce
	sb := RecoverKeySigningBytes(rotChain, f.addr(), rn, rescue.pub)
	_, err := f.ms.RecoverKey(f.ctx, &MsgRecoverKey{Submitter: f.addr(), Account: f.addr(), NewPubkey: rescue.pub,
		NewKeySignature:    rescue.sign(t, RotateKeySigningBytes(rotChain, f.addr(), rn, rescue.pub)),
		GuardianSignatures: []*GuardianSignature{guardians[0].guardianSig(t, sb), guardians[1].guardianSig(t, sb)}})
	require.NoError(t, err)
	require.Equal(t, uint64(1), f.k.KeyGeneration(f.ctx, f.account))
	s, g := f.drain(p)
	require.ErrorIs(t, s, ErrDelegationRetired)
	require.ErrorIs(t, g, ErrDelegationRetired)
}

// Revoking one forgets its record; a later id is never confused with it.
func TestKeyChange_Revoke(t *testing.T) {
	f := newRotFixture(t, rotChain, 10)
	p := f.plant()
	_, err := f.ms.RevokeAuthenticator(f.ctx, &MsgRevokeAuthenticator{Account: f.addr(), Id: p.sessionID})
	require.NoError(t, err)
	require.Nil(t, f.ctx.KVStore(f.k.storeKey).Get(authenticatorGenKey(f.account, p.sessionID)))
}

// On the testnet nothing changes before KeyChangeRevokesHeight: a key
// change there leaves what the old key planted working, as before. From
// the height the first key change retires it, including what was planted
// before the height.
func TestKeyChange_Gate(t *testing.T) {
	before := KeyChangeRevokesHeight - RotationDelayBlocks - 100
	f := newRotFixture(t, "aether-testnet-1", before)
	p := f.plant()
	f.rotateAndApply(newRotKey(t))
	require.Less(t, f.ctx.BlockHeight(), KeyChangeRevokesHeight)
	require.Equal(t, uint64(0), f.k.KeyGeneration(f.ctx, f.account), "no generation before the height")
	require.False(t, hasEventType(f.ctx, EventTypeDelegationsRetired))
	s, g := f.drain(p)
	require.NoError(t, s, "unchanged before the height")
	require.NoError(t, g)

	f.at(KeyChangeRevokesHeight)
	s, _ = f.drain(p)
	require.NoError(t, s, "the height alone retires nothing")
	f.rotateAndApply(newRotKey(t))
	s, g = f.drain(p)
	require.ErrorIs(t, s, ErrDelegationRetired, "the first change after the height retires older ones")
	require.ErrorIs(t, g, ErrDelegationRetired)
}

// Grants and allowances follow the granter's generation.
func TestKeyChange_GrantsAndAllowances(t *testing.T) {
	f := newRotFixture(t, rotChain, 10)
	thief := sdk.AccAddress("thief_______________")
	typeURL := sdk.MsgTypeURL(&banktypes.MsgSend{})
	require.True(t, f.k.GrantCurrent(f.ctx, f.account, thief, typeURL), "no record, generation 0")
	f.k.RecordGrant(f.ctx, f.account, thief, typeURL)
	f.k.RecordAllowance(f.ctx, f.account, thief)
	require.True(t, f.k.GrantCurrent(f.ctx, f.account, thief, typeURL))
	require.True(t, f.k.AllowanceCurrent(f.ctx, f.account, thief))

	f.rotateAndApply(newRotKey(t))
	require.False(t, f.k.GrantCurrent(f.ctx, f.account, thief, typeURL))
	require.False(t, f.k.AllowanceCurrent(f.ctx, f.account, thief))
	require.True(t, f.k.GrantCurrent(f.ctx, thief, f.account, typeURL), "keyed by granter: the other way round is untouched")

	f.k.RecordGrant(f.ctx, f.account, thief, typeURL)
	f.k.RecordAllowance(f.ctx, f.account, thief)
	require.True(t, f.k.GrantCurrent(f.ctx, f.account, thief, typeURL), "granted again by the new key")
	require.True(t, f.k.AllowanceCurrent(f.ctx, f.account, thief))
	f.k.ForgetGrant(f.ctx, f.account, thief, typeURL)
	require.False(t, f.k.GrantCurrent(f.ctx, f.account, thief, typeURL), "revoked: no record reads as generation 0, now stale")
}
