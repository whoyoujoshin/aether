package pow_test

import (
	"crypto/ed25519"
	"errors"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	cometed25519 "github.com/cometbft/cometbft/crypto/ed25519"
	cometencoding "github.com/cometbft/cometbft/crypto/encoding"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/x/pow"
	"github.com/whoyoujoshin/aether/x/pow/types"
)

type consensusKey struct {
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

func newConsensusKey(t *testing.T) consensusKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	return consensusKey{pub, priv}
}

func (c consensusKey) proto(t *testing.T) abci.ValidatorUpdate {
	t.Helper()
	pk, err := cometencoding.PubKeyToProto(cometed25519.PubKey(c.pub))
	require.NoError(t, err)
	return abci.ValidatorUpdate{PubKey: pk}
}

func register(srv pow.MsgServer, ctx sdk.Context, miner sdk.AccAddress, key consensusKey) error {
	_, err := srv.RegisterValidatorPubkey(ctx, &pow.MsgRegisterValidatorPubkey{
		Miner:           miner.String(),
		ConsensusPubkey: key.pub,
		Signature:       ed25519.Sign(key.priv, []byte(miner.String())),
	})
	return err
}

var (
	minerA = sdk.AccAddress("guard_test_miner_a__")
	minerB = sdk.AccAddress("guard_test_miner_b__")
)

func TestConsensusKeyGuard_RejectsKeyHeldByAnotherMiner(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)
	ctx = ctx.WithBlockHeight(pow.ConsensusKeyGuardActivationHeight)
	key := newConsensusKey(t)

	require.NoError(t, register(srv, ctx, minerA, key))
	err := register(srv, ctx, minerB, key)
	require.True(t, errors.Is(err, types.ErrConsensusKeyInUse), "got %v", err)
	require.Contains(t, err.Error(), minerA.String(), "the error names the account holding the key")

	_, bHasKey := k.GetValidatorPubkey(ctx, minerB)
	require.False(t, bHasKey, "the refused registration stored nothing")
	holder, _ := k.GetMinerByConsensusAddr(ctx, cometed25519.PubKey(key.pub).Address())
	require.Equal(t, minerA, holder)

	// The holder itself may register it again.
	require.NoError(t, register(srv, ctx, minerA, key))
}

func TestConsensusKeyGuard_KeyReleasedByRotationCanBeTaken(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)
	ctx = ctx.WithBlockHeight(pow.ConsensusKeyGuardActivationHeight)
	key, other := newConsensusKey(t), newConsensusKey(t)

	require.NoError(t, register(srv, ctx, minerA, key))
	require.NoError(t, register(srv, ctx, minerA, other), "A moves to another key, releasing the first")
	require.NoError(t, register(srv, ctx, minerB, key), "the stale reverse-index entry for A doesn't block B")

	stored, _ := k.GetValidatorPubkey(ctx, minerB)
	require.Equal(t, []byte(key.pub), stored)
}

func TestConsensusKeyGuard_GenesisBootstrapKeyCanBeTaken(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)
	ctx = ctx.WithBlockHeight(pow.ConsensusKeyGuardActivationHeight)
	key := newConsensusKey(t)

	pk, err := cometencoding.PubKeyToProto(cometed25519.PubKey(key.pub))
	require.NoError(t, err)
	require.NoError(t, k.BootstrapValidator(ctx, pk))

	require.NoError(t, register(srv, ctx, minerA, key),
		"a genesis validator's operator can bind its node's key to a miner account")
}

// Below the activation height the old behavior stays, so a fresh node
// replaying history computes what the chain did.
func TestConsensusKeyGuard_BeforeActivation_SharedKeyAllowed(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)
	ctx = ctx.WithBlockHeight(pow.ConsensusKeyGuardActivationHeight - 1)
	key := newConsensusKey(t)

	require.NoError(t, register(srv, ctx, minerA, key))
	require.NoError(t, register(srv, ctx, minerB, key))

	a, _ := k.GetValidatorPubkey(ctx, minerA)
	b, _ := k.GetValidatorPubkey(ctx, minerB)
	require.Equal(t, a, b)
}

func TestRotationWhileActive_LeavesActiveSetSoNoRemovalNamesTheNewKey(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)
	ctx = ctx.WithBlockHeight(pow.ConsensusKeyGuardActivationHeight)
	oldKey, newKey := newConsensusKey(t), newConsensusKey(t)

	require.NoError(t, register(srv, ctx, minerA, oldKey))
	k.SetActiveValidator(ctx, minerA)
	require.NoError(t, register(srv, ctx, minerA, newKey))

	require.False(t, k.IsActiveValidator(ctx, minerA), "rotating takes it out of the active set")
	pending := k.IteratePendingKeyRevocations(ctx)
	require.Len(t, pending, 1)
	require.Equal(t, []byte(oldKey.pub), pending[0].OldPubkey, "the old key still loses its power")

	// The epoch ends without A: another miner qualifies, A doesn't.
	epoch := k.CurrentEpoch(ctx)
	k.SetValidatorPubkey(ctx, minerB, newConsensusKey(t).pub)
	k.AddMiningWork(ctx, epoch, minerB, 1)
	for _, u := range k.ComputeValidatorUpdates(ctx, epoch) {
		require.NotEqual(t, newKey.proto(t).PubKey, u.PubKey,
			"no update may name A's new key: CometBFT never gave it power, so removing it would halt the chain")
	}
}

// Between RotationRevocationActivationHeight and the guard, a rotating
// validator stayed in the active set; replay must keep that.
func TestRotationWhileActive_BeforeGuard_StaysActive(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)
	ctx = ctx.WithBlockHeight(pow.RotationRevocationActivationHeight)
	require.Less(t, pow.RotationRevocationActivationHeight, pow.ConsensusKeyGuardActivationHeight)

	require.NoError(t, register(srv, ctx, minerA, newConsensusKey(t)))
	k.SetActiveValidator(ctx, minerA)
	require.NoError(t, register(srv, ctx, minerA, newConsensusKey(t)))
	require.True(t, k.IsActiveValidator(ctx, minerA))
}

func TestDedupeValidatorUpdates(t *testing.T) {
	a, b := newConsensusKey(t), newConsensusKey(t)
	with := func(c consensusKey, power int64) abci.ValidatorUpdate {
		u := c.proto(t)
		u.Power = power
		return u
	}

	unique := []abci.ValidatorUpdate{with(a, 0), with(b, 5)}
	got, dropped := pow.DedupeValidatorUpdates(unique)
	require.Equal(t, 0, dropped)
	require.Equal(t, unique, got)

	// A removal then an addition of the same key: the addition wins.
	got, dropped = pow.DedupeValidatorUpdates([]abci.ValidatorUpdate{with(a, 0), with(b, 0), with(a, 7)})
	require.Equal(t, 1, dropped)
	require.Equal(t, []abci.ValidatorUpdate{with(b, 0), with(a, 7)}, got)

	// The same key picked twice.
	got, dropped = pow.DedupeValidatorUpdates([]abci.ValidatorUpdate{with(a, 7), with(a, 7)})
	require.Equal(t, 1, dropped)
	require.Equal(t, []abci.ValidatorUpdate{with(a, 7)}, got)

	got, dropped = pow.DedupeValidatorUpdates(nil)
	require.Equal(t, 0, dropped)
	require.Empty(t, got)
}

// The halt reproduced on a devnet 2026-09-28: a miner bound the genesis
// validator's own key, mined, and was picked at the epoch's last block.
// EndBlock removed the bootstrap entry (K:0) and added the miner (K:power)
// in one list, and CometBFT refused it. Now it hands over one update.
func TestEndBlock_GenesisKeyHandoverAtSelectionCommits(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)
	am := pow.NewAppModule(codec.NewProtoCodec(codectypes.NewInterfaceRegistry()), k)
	key := newConsensusKey(t)

	const epochLength = 40
	k.SetEpochLength(ctx, epochLength)
	ctx = ctx.WithBlockHeight(2*epochLength - 1) // a selection block (height+1 divisible by the length)

	pk, err := cometencoding.PubKeyToProto(cometed25519.PubKey(key.pub))
	require.NoError(t, err)
	require.NoError(t, k.BootstrapValidator(ctx, pk))
	require.NoError(t, register(srv, ctx, minerA, key))
	k.AddMiningWork(ctx, k.CurrentEpoch(ctx), minerA, 3)

	updates, err := am.EndBlock(ctx)
	require.NoError(t, err)
	require.Len(t, updates, 1, "one update for the key, not a removal and an addition")
	require.Equal(t, pk, updates[0].PubKey)
	require.Equal(t, int64(pow.ValidatorVotingPower), updates[0].Power, "the key keeps its power, now as the miner's")
	require.True(t, k.IsActiveValidator(ctx, minerA))
	require.False(t, k.IsActiveValidator(ctx, sdk.AccAddress(cometed25519.PubKey(key.pub).Address())))
}
