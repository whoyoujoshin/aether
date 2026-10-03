package pow_test

import (
	"errors"
	"testing"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/x/pow"
	"github.com/whoyoujoshin/aether/x/pow/types"
)

const testReward = 5_000_000

// newTracksCase is an auxPowCase with a second recent block (difficulty 1)
// that native submissions can build on, at MergedMiningActivationHeight.
func newTracksCase(t *testing.T) (*auxPowCase, []byte) {
	t.Helper()
	c := newAuxPowCase(t)
	nativeHash := []byte("block-hash-native-work-builds-on")
	ctx := setupRecentBlock(c.k, c.ctx, c.templateHeight-1, nativeHash, 1)
	c.ctx = ctx.WithBlockHeight(pow.MergedMiningActivationHeight).WithBlockTime(time.Unix(1_900_000_000, 0))
	return c, nativeHash
}

func (c *auxPowCase) submitNative(nativeHash []byte, nonce uint64) error {
	msg := newNativeSubmitMsg(sdk.AccAddress("native_miner_address").String(), uint64(c.templateHeight-1), c.ctx.BlockTime().Unix(), nativeHash, []byte("merkle"), nonce, 1)
	_, err := c.srv.SubmitPoW(c.ctx, msg)
	return err
}

func (c *auxPowCase) lastMint(t *testing.T) string {
	t.Helper()
	require.NotEmpty(t, c.bank.MintCalls)
	return c.bank.MintCalls[len(c.bank.MintCalls)-1].Coins.String()
}

// A track mining alone earns the full reward, so native miners lose
// nothing before any pool shows up, and a pool alone isn't short-changed.
func TestMergedMining_LoneTrackEarnsTheFullReward(t *testing.T) {
	c, nativeHash := newTracksCase(t)
	require.NoError(t, c.submitNative(nativeHash, 1))
	require.Equal(t, "5000000uaeth", c.lastMint(t))

	c, _ = newTracksCase(t)
	require.NoError(t, c.submit(c.proof(t, c.pool)))
	require.Equal(t, "5000000uaeth", c.lastMint(t))
}

// While both tracks are mining, a native and a merged submission together
// earn exactly one reward: 75% and 25%.
func TestMergedMining_BothTracksSplitOneReward(t *testing.T) {
	c, nativeHash := newTracksCase(t)
	c.k.SetAuxLastBlockTime(c.ctx, c.ctx.BlockTime().Unix()-60)
	c.k.SetLastBlockTime(c.ctx, c.ctx.BlockTime().Unix()-60)
	c.k.SetAuxDifficulty(c.ctx, math.NewInt(auxTestDifficulty))

	require.NoError(t, c.submitNative(nativeHash, 1))
	require.Equal(t, "3750000uaeth", c.lastMint(t))
	require.NoError(t, c.submit(c.proof(t, c.pool)), "the AuxPoW slot is separate from the native one")
	require.Equal(t, "1250000uaeth", c.lastMint(t))
	require.Len(t, c.bank.MintCalls, 2)
}

func TestMergedMining_ActiveWindow(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	ctx = ctx.WithBlockHeight(pow.MergedMiningActivationHeight).WithBlockTime(time.Unix(1_900_000_000, 0))
	k.SetBlockReward(ctx, math.NewInt(testReward))
	now := ctx.BlockTime().Unix()
	window := 5 * k.GetTargetBlockTime(ctx)

	k.SetAuxLastBlockTime(ctx, now-window)
	require.Equal(t, "3750000", k.NativeReward(ctx).String(), "an AuxPoW submission at the edge of the window still counts")
	k.SetAuxLastBlockTime(ctx, now-window-1)
	require.Equal(t, "5000000", k.NativeReward(ctx).String(), "past the window merged mining has stopped")

	k.SetLastBlockTime(ctx, now-window)
	require.Equal(t, "1250000", k.AuxReward(ctx).String())
	k.SetLastBlockTime(ctx, now-window-1)
	require.Equal(t, "5000000", k.AuxReward(ctx).String())
}

// The shares always add up to the reward, with rounding going to native.
func TestMergedMining_SharesAddUpToOneReward(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	ctx = ctx.WithBlockHeight(pow.MergedMiningActivationHeight).WithBlockTime(time.Unix(1_900_000_000, 0))
	k.SetLastBlockTime(ctx, ctx.BlockTime().Unix())
	k.SetAuxLastBlockTime(ctx, ctx.BlockTime().Unix())
	for _, r := range []int64{testReward, 5_000_001, 3, 1} {
		k.SetBlockReward(ctx, math.NewInt(r))
		require.Equal(t, r, k.NativeReward(ctx).Add(k.AuxReward(ctx)).Int64(), "reward %d", r)
	}
}

func TestMergedMining_OneSubmissionPerTrackPerBlock(t *testing.T) {
	c, nativeHash := newTracksCase(t)
	require.NoError(t, c.submit(c.proof(t, c.pool)))
	err := c.submit(c.proof(t, sdk.AccAddress("second_pool_address_")))
	require.True(t, errors.Is(err, types.ErrTooManySubmissionsThisBlock), "got %v", err)

	require.NoError(t, c.submitNative(nativeHash, 1))
	err = c.submitNative(nativeHash, 2)
	require.True(t, errors.Is(err, types.ErrTooManySubmissionsThisBlock), "got %v", err)
}

// Merged work retargets only its own difficulty, and native work only
// native difficulty, so pool hash power can't push native difficulty up.
func TestMergedMining_SeparateDifficulties(t *testing.T) {
	c, nativeHash := newTracksCase(t)
	require.Equal(t, c.k.GetDifficulty(c.ctx), c.k.GetAuxDifficulty(c.ctx), "the AuxPoW track starts from native difficulty")

	// An AuxPoW submission one half-life behind schedule halves AuxPoW
	// difficulty (the smooth retarget is in force at this height) and
	// leaves native alone.
	c.k.SetDifficulty(c.ctx, math.NewInt(100))
	native := c.k.GetDifficulty(c.ctx)
	c.k.SetAuxDifficulty(c.ctx, math.NewInt(auxTestDifficulty))
	c.k.SetAuxLastBlockTime(c.ctx, c.ctx.BlockTime().Unix()-60-pow.DifficultyHalfLife)
	c.k.SetMinDifficulty(c.ctx, 1)
	require.NoError(t, c.submit(c.proof(t, c.pool)))
	aux := c.k.GetAuxDifficulty(c.ctx)
	require.Equal(t, "2", aux.String())
	require.Equal(t, native, c.k.GetDifficulty(c.ctx))
	lastAux, _ := c.k.GetAuxLastBlockTime(c.ctx)
	require.Equal(t, c.ctx.BlockTime().Unix(), lastAux)

	// A native submission retargets native difficulty and leaves AuxPoW
	// alone.
	c.k.SetLastBlockTime(c.ctx, c.ctx.BlockTime().Unix()-60-pow.DifficultyHalfLife)
	require.NoError(t, c.submitNative(nativeHash, 1))
	require.Equal(t, aux, c.k.GetAuxDifficulty(c.ctx))
	require.Equal(t, "50", c.k.GetDifficulty(c.ctx).String(), "one half-life behind schedule halves native difficulty")
}

// AuxPoW is checked against the AuxPoW difficulty, not native.
func TestMergedMining_AuxPoWCheckedAgainstItsOwnDifficulty(t *testing.T) {
	c, _ := newTracksCase(t)
	c.k.SetAuxDifficulty(c.ctx, math.NewInt(1<<40))
	err := c.submit(c.proof(t, c.pool))
	require.True(t, errors.Is(err, types.ErrInvalidPoW), "got %v", err)
}

// Below MergedMiningActivationHeight AuxPoW and native share one slot, one
// difficulty and the full reward, as they always did.
func TestMergedMining_BeforeActivationSharesOneTrack(t *testing.T) {
	c, nativeHash := newTracksCase(t)
	c.ctx = c.ctx.WithBlockHeight(pow.MergedMiningActivationHeight - 1)
	c.k.SetLastBlockTime(c.ctx, c.ctx.BlockTime().Unix()-120)
	before := c.k.GetDifficulty(c.ctx)

	p := pow.BuildValidAuxPowForTest(t, []byte("any-hash-the-submitter-liked-000"), auxTestDifficulty, c.ctx.BlockHeight())
	require.NoError(t, c.submit(p))
	require.Equal(t, "5000000uaeth", c.lastMint(t))
	require.NotEqual(t, before, c.k.GetDifficulty(c.ctx), "AuxPoW retargets the one shared difficulty")
	_, ok := c.k.GetAuxLastBlockTime(c.ctx)
	require.False(t, ok, "no AuxPoW-track state is written before the height")

	err := c.submitNative(nativeHash, 1)
	require.True(t, errors.Is(err, types.ErrTooManySubmissionsThisBlock), "one shared slot, got %v", err)
}

func TestMergedMining_DifficultyQuery(t *testing.T) {
	c, _ := newTracksCase(t)
	q := pow.NewQueryServerImpl(c.k)
	c.k.SetAuxDifficulty(c.ctx, math.NewInt(12345))
	resp, err := q.Difficulty(c.ctx, &pow.QueryDifficultyRequest{})
	require.NoError(t, err)
	require.Equal(t, "12345", resp.AuxDifficulty)

	resp, err = q.Difficulty(c.ctx.WithBlockHeight(pow.MergedMiningActivationHeight-1), &pow.QueryDifficultyRequest{})
	require.NoError(t, err)
	require.Empty(t, resp.AuxDifficulty)
}

func shareMsg(bps uint32) *pow.MsgUpdateParams {
	return &pow.MsgUpdateParams{
		Authority: testAuthority, EpochLength: 1440, TopKSize: 21, BondCooldown: 4320, RecencyWindowK: 60, BeaconRoundsPerBlock: 5000,
		MergedMiningRewardShareBps: bps,
	}
}

// Governance sets the merged share from the activation height, and the
// rewards follow it: the pair still adds up to one reward.
func TestMergedMining_ShareIsAGovernanceParameter(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	ctx = ctx.WithBlockHeight(pow.MergedMiningActivationHeight).WithBlockTime(time.Unix(1_900_000_000, 0))
	srv := pow.NewMsgServerImpl(k)
	k.SetBlockReward(ctx, math.NewInt(testReward))
	k.SetLastBlockTime(ctx, ctx.BlockTime().Unix())
	k.SetAuxLastBlockTime(ctx, ctx.BlockTime().Unix())
	require.Equal(t, pow.MergedMiningRewardShareBps, k.GetMergedMiningRewardShareBps(ctx), "the default until governance sets one")

	_, err := srv.UpdateParams(ctx, shareMsg(1_000))
	require.NoError(t, err)
	require.Equal(t, int64(1_000), k.GetMergedMiningRewardShareBps(ctx))
	require.Equal(t, "500000", k.AuxReward(ctx).String())
	require.Equal(t, "4500000", k.NativeReward(ctx).String())

	// 0 keeps it: a proposal drafted without the field doesn't zero it.
	_, err = srv.UpdateParams(ctx, shareMsg(0))
	require.NoError(t, err)
	require.Equal(t, int64(1_000), k.GetMergedMiningRewardShareBps(ctx))

	// The whole reward is the most it can be.
	_, err = srv.UpdateParams(ctx, shareMsg(10_000))
	require.NoError(t, err)
	require.Equal(t, "0", k.NativeReward(ctx).String())
	_, err = srv.UpdateParams(ctx, shareMsg(10_001))
	require.True(t, errors.Is(err, types.ErrInvalidParamValue), "got %v", err)

	resp, err := pow.NewQueryServerImpl(k).Params(ctx, &pow.QueryParamsRequest{})
	require.NoError(t, err)
	require.Equal(t, uint32(10_000), resp.MergedMiningRewardShareBps)
}

// Below the activation height the share can't be set, and a proposal that
// leaves it out (every one so far, like the live bond_cooldown proposal)
// writes nothing new: history replays the same.
func TestMergedMining_ShareCantBeSetBeforeActivation(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	ctx = ctx.WithBlockHeight(pow.MergedMiningActivationHeight - 1)
	srv := pow.NewMsgServerImpl(k)

	k.SetEpochLength(ctx, 7)
	_, err := srv.UpdateParams(ctx, shareMsg(1_000))
	require.True(t, errors.Is(err, types.ErrInvalidParamValue), "got %v", err)
	require.Equal(t, int64(7), k.GetEpochLength(ctx), "a refused proposal applies nothing")

	_, err = srv.UpdateParams(ctx, shareMsg(0))
	require.NoError(t, err)
	require.Nil(t, ctx.KVStore(k.StoreKeyForTest()).Get(pow.KeyMergedMiningRewardShareBps), "no merged-mining key written below the height")
}
