package pow_test

import (
	"errors"
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/x/pow"
	"github.com/whoyoujoshin/aether/x/pow/testutil"
	"github.com/whoyoujoshin/aether/x/pow/types"
)

// auxPowCase is one merged-mining submission at MergedMiningActivationHeight:
// a recent block at templateHeight, a pool's reward address, and a relayer
// (the signer) that holds no reward rights of its own.
type auxPowCase struct {
	k              pow.Keeper
	ctx            sdk.Context
	bank           *testutil.MockBankKeeper
	srv            pow.MsgServer
	templateHeight int64
	blockHash      []byte
	pool, relayer  sdk.AccAddress
}

const auxTestDifficulty = 4 // low, so proofs grind in a moment

func newAuxPowCase(t *testing.T) *auxPowCase {
	t.Helper()
	k, ctx, bank := setupKeeper(t)
	c := &auxPowCase{
		k: k, bank: bank, srv: pow.NewMsgServerImpl(k),
		templateHeight: pow.MergedMiningActivationHeight - 1,
		blockHash:      []byte("block-hash-at-the-template-height"),
		pool:           sdk.AccAddress("pool_payout_address_"),
	}
	c.relayer, _ = validMinerAddr(t)
	ctx = setupRecentBlock(k, ctx, c.templateHeight, c.blockHash, auxTestDifficulty)
	c.ctx = ctx.WithBlockHeight(pow.MergedMiningActivationHeight)
	k.SetBlockReward(c.ctx, math.NewInt(5_000_000))
	return c
}

// proof is a valid proof committing to the template for reward, with the
// submission's template fields set to match.
func (c *auxPowCase) proof(t *testing.T, reward sdk.AccAddress) *pow.AuxPowData {
	t.Helper()
	aux := pow.AuxPoWTemplateHash(c.ctx.ChainID(), c.templateHeight, c.blockHash, reward)
	p := pow.BuildValidAuxPowForTest(t, aux, auxTestDifficulty, c.ctx.BlockHeight())
	p.TemplateHeight, p.RewardAddress = c.templateHeight, reward.String()
	return p
}

func (c *auxPowCase) submit(p *pow.AuxPowData) error {
	_, err := c.srv.SubmitPoW(c.ctx, &pow.MsgSubmitPoW{Miner: c.relayer.String(), Submission: &pow.MsgSubmitPoW_AuxPow{AuxPow: p}})
	return err
}

func TestSubmitAuxPoW_PaysTheRewardAddressNotTheSigner(t *testing.T) {
	c := newAuxPowCase(t)
	require.NoError(t, c.submit(c.proof(t, c.pool)))
	require.Len(t, c.bank.MintCalls, 1)
	require.Equal(t, c.pool, c.bank.SendCalls[0].RecipientAddr, "the pool committed in the proof is paid")
	for _, s := range c.bank.SendCalls {
		require.NotEqual(t, c.relayer, s.RecipientAddr, "the relayer that signed is never paid")
	}
}

// Anyone who sees a pool's proof can resubmit it, but only to pay the pool:
// pointing reward_address elsewhere no longer matches what the parent
// chain's coinbase committed to.
func TestSubmitAuxPoW_CopiedProofCannotBeRedirected(t *testing.T) {
	c := newAuxPowCase(t)
	p := c.proof(t, c.pool)
	thief := sdk.AccAddress("someone_who_saw_it__")
	p.RewardAddress = thief.String()
	err := c.submit(p)
	require.True(t, errors.Is(err, types.ErrInvalidPoW), "got %v", err)
	require.Empty(t, c.bank.MintCalls)
}

func TestSubmitAuxPoW_TemplateMustBeRecentAndKnown(t *testing.T) {
	c := newAuxPowCase(t)
	p := c.proof(t, c.pool)

	// Past the recency window, like stale native work.
	stale := c.ctx.WithBlockHeight(c.templateHeight + c.k.GetRecencyWindowK(c.ctx) + 1)
	_, err := c.srv.SubmitPoW(stale, &pow.MsgSubmitPoW{Miner: c.relayer.String(), Submission: &pow.MsgSubmitPoW_AuxPow{AuxPow: p}})
	require.True(t, errors.Is(err, types.ErrStaleAncestor), "got %v", err)

	// A height with no recorded block.
	unknown := c.proof(t, c.pool)
	unknown.TemplateHeight = c.templateHeight - 5
	require.True(t, errors.Is(c.submit(unknown), types.ErrUnknownAncestor))

	// No reward address at all.
	missing := c.proof(t, c.pool)
	missing.RewardAddress = ""
	require.True(t, errors.Is(c.submit(missing), types.ErrInvalidPoW))
	require.Empty(t, c.bank.MintCalls)
}

// A banned miner can't be paid by having someone else relay its proof.
func TestSubmitAuxPoW_BannedRewardAddressIsRefused(t *testing.T) {
	c := newAuxPowCase(t)
	c.k.SetBanned(c.ctx, c.pool)
	require.True(t, errors.Is(c.submit(c.proof(t, c.pool)), types.ErrBannedMiner))
	require.Empty(t, c.bank.MintCalls)
}

// Below MergedMiningActivationHeight nothing changes: aux_block_hash is not
// checked against a template and the signer is paid, so history replays
// the same.
func TestSubmitAuxPoW_BeforeActivationPaysTheSigner(t *testing.T) {
	c := newAuxPowCase(t)
	c.ctx = c.ctx.WithBlockHeight(pow.MergedMiningActivationHeight - 1)
	p := pow.BuildValidAuxPowForTest(t, []byte("any-hash-the-submitter-liked-000"), auxTestDifficulty, c.ctx.BlockHeight())
	require.NoError(t, c.submit(p))
	require.Equal(t, c.relayer, c.bank.SendCalls[0].RecipientAddr)
}
