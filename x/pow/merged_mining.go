package pow

import (
	"encoding/json"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// Change C of docs/MERGED-MINING-PLAN.md: from MergedMiningActivationHeight
// native and merged (AuxPoW) work run on separate tracks, so pool hash
// power can't crowd out the native miners who pick validators.
//
//   - Difficulty. AuxPoW has its own difficulty, retargeted only on AuxPoW
//     submissions toward the same TargetBlockTime. It starts from the
//     native difficulty the first time it's needed. Native difficulty is
//     retargeted only on native submissions, as before.
//   - Slots. Each block accepts one native and one AuxPoW submission.
//   - Reward. The emission schedule is one block reward per TargetBlockTime,
//     and it stays that way. While both tracks are mining, a native
//     submission earns the reward less the merged share (a governance
//     parameter, MergedMiningRewardShareBps by default) and an
//     AuxPoW submission earns that share, so a native and a merged proof
//     together earn exactly one reward. A track mining alone earns the full
//     reward, so native miners lose nothing until pools actually show up.
//     A track counts as mining if it had a submission accepted within the
//     last mergedMiningActiveWindow target intervals.
//
// Below the height none of these keys are read or written.

// MergedMiningRewardShareBps is the default share of the block reward, in
// basis points, an AuxPoW submission earns while native mining is active
// (and that a native submission gives up while merged mining is active):
// what applies until governance sets another with MsgUpdateParams.
const MergedMiningRewardShareBps int64 = 2_500

// mergedMiningActiveWindow is how many TargetBlockTime intervals after its
// last accepted submission a track still counts as mining. Submissions
// arrive at random, so a track retargeted to one per interval sometimes
// goes several intervals without one; five keeps a live track counted
// as live more than 99% of the time.
const mergedMiningActiveWindow int64 = 5

// AuxMaxDifficulty caps the AuxPoW track. It's well above the native
// MaxDifficulty so pool-scale hash power can be retargeted to one
// submission per TargetBlockTime instead of landing every block at the cap,
// and well below int64 overflow in the retarget arithmetic.
const AuxMaxDifficulty int64 = 1 << 62

var (
	KeyAuxDifficulty                   = []byte("aux_difficulty")
	KeyAuxLastBlockTime                = []byte("aux_last_block_time")
	KeyLastAcceptedAuxSubmissionHeight = []byte("last_accepted_aux_submission_height")
	KeyMergedMiningRewardShareBps      = []byte("merged_mining_reward_share_bps")
)

func (k Keeper) setInt64(ctx sdk.Context, key []byte, v int64) {
	bz, _ := json.Marshal(v)
	ctx.KVStore(k.storeKey).Set(key, bz)
}

func (k Keeper) getInt64(ctx sdk.Context, key []byte) (int64, bool) {
	bz := ctx.KVStore(k.storeKey).Get(key)
	if bz == nil {
		return 0, false
	}
	var v int64
	_ = json.Unmarshal(bz, &v)
	return v, true
}

// GetAuxDifficulty is the AuxPoW track's difficulty: the native difficulty
// until the first AuxPoW submission is accepted on the separate track.
func (k Keeper) GetAuxDifficulty(ctx sdk.Context) math.Int {
	if d, ok := k.getInt64(ctx, KeyAuxDifficulty); ok {
		return math.NewInt(d)
	}
	return k.GetDifficulty(ctx)
}

func (k Keeper) SetAuxDifficulty(ctx sdk.Context, d math.Int) {
	k.setInt64(ctx, KeyAuxDifficulty, d.Int64())
}

func (k Keeper) GetAuxLastBlockTime(ctx sdk.Context) (int64, bool) {
	return k.getInt64(ctx, KeyAuxLastBlockTime)
}

func (k Keeper) SetAuxLastBlockTime(ctx sdk.Context, t int64) {
	k.setInt64(ctx, KeyAuxLastBlockTime, t)
}

// AdjustAuxDifficulty is AdjustDifficulty for the AuxPoW track: the same
// retarget toward TargetBlockTime, from the time of the last accepted
// AuxPoW submission, floored at MinDifficulty and capped at
// AuxMaxDifficulty.
func (k Keeper) AdjustAuxDifficulty(ctx sdk.Context) math.Int {
	current := k.GetAuxDifficulty(ctx)
	lastTime, ok := k.GetAuxLastBlockTime(ctx)
	if !ok {
		return current
	}
	elapsed := ctx.BlockTime().Unix() - lastTime
	if elapsed <= 0 {
		return current
	}
	adjusted := current.MulRaw(k.GetTargetBlockTime(ctx)).QuoRaw(elapsed)
	if minD := k.GetMinDifficulty(ctx); adjusted.LT(minD) {
		adjusted = minD
	}
	if maxD := math.NewInt(AuxMaxDifficulty); adjusted.GT(maxD) {
		adjusted = maxD
	}
	return adjusted
}

// submissionSlotTaken reports whether this block has already accepted a
// submission on the given track. Before MergedMiningActivationHeight
// callers pass auxTrack false for both kinds, which is the one shared slot.
func (k Keeper) submissionSlotTaken(ctx sdk.Context, auxTrack bool) bool {
	var h int64
	var ok bool
	if auxTrack {
		h, ok = k.getInt64(ctx, KeyLastAcceptedAuxSubmissionHeight)
	} else {
		h, ok = k.GetLastAcceptedSubmissionHeight(ctx)
	}
	return ok && h == ctx.BlockHeight()
}

func (k Keeper) takeSubmissionSlot(ctx sdk.Context, auxTrack bool) {
	if auxTrack {
		k.setInt64(ctx, KeyLastAcceptedAuxSubmissionHeight, ctx.BlockHeight())
		return
	}
	k.SetLastAcceptedSubmissionHeight(ctx, ctx.BlockHeight())
}

// trackMining reports whether a track whose last accepted submission was at
// lastTime still counts as mining now.
func (k Keeper) trackMining(ctx sdk.Context, lastTime int64, ok bool) bool {
	if !ok {
		return false
	}
	return ctx.BlockTime().Unix()-lastTime <= mergedMiningActiveWindow*k.GetTargetBlockTime(ctx)
}

// GetMergedMiningRewardShareBps is the merged share in effect, in basis
// points: what governance last set, else MergedMiningRewardShareBps.
func (k Keeper) GetMergedMiningRewardShareBps(ctx sdk.Context) int64 {
	if v, ok := k.getInt64(ctx, KeyMergedMiningRewardShareBps); ok {
		return v
	}
	return MergedMiningRewardShareBps
}

func (k Keeper) SetMergedMiningRewardShareBps(ctx sdk.Context, bps int64) {
	k.setInt64(ctx, KeyMergedMiningRewardShareBps, bps)
}

// mergedShare is the AuxPoW share of reward: the merged share in effect,
// rounded down. The native share is the rest, so the two always add up to
// exactly one reward.
func (k Keeper) mergedShare(ctx sdk.Context, reward math.Int) math.Int {
	return reward.MulRaw(k.GetMergedMiningRewardShareBps(ctx)).QuoRaw(10_000)
}

// NativeReward is what a native submission earns from
// MergedMiningActivationHeight: the full block reward, less the merged
// share while merged mining is active.
func (k Keeper) NativeReward(ctx sdk.Context) math.Int {
	reward := k.GetBlockReward(ctx)
	if last, ok := k.GetAuxLastBlockTime(ctx); k.trackMining(ctx, last, ok) {
		return reward.Sub(k.mergedShare(ctx, reward))
	}
	return reward
}

// AuxReward is what an AuxPoW submission earns from
// MergedMiningActivationHeight: the merged share while native mining is
// active, the full block reward otherwise.
func (k Keeper) AuxReward(ctx sdk.Context) math.Int {
	reward := k.GetBlockReward(ctx)
	if last, ok := k.GetLastBlockTime(ctx); k.trackMining(ctx, last, ok) {
		return k.mergedShare(ctx, reward)
	}
	return reward
}
