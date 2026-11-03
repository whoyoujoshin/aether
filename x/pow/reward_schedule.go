package pow

import (
	"encoding/json"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/gates"
)

// The reward schedule's years, on a fresh chain, are years of block
// time since genesis.
//
// The schedule (5 AETH, 66% kept each year for 8 years, then the 0.20
// AETH tail) was written as SecondsPerYear / TargetBlockTime blocks a
// year: 525,960 blocks, a block a minute. TargetBlockTime is really the
// time ASERT aims for between accepted submissions; blocks come every
// ~5.7 s, so each "year" lasted about 35 days and the 8 years about nine
// months. On aether-1 and aether-rc-* chains a year is SecondsPerYear of
// block time after the genesis time, so the 8 years are 8 years.
//
// Block time is consensus data, not a node's clock: CometBFT sets it
// from the previous commit's vote timestamps (a power-weighted median),
// so every node computes the same year, and moving it needs a third of
// the power. The testnet and every other chain ID keep the height-based
// years, so their rewards don't change.

// KeyRewardScheduleStart holds the unix time (seconds) the schedule's
// first year starts at: the genesis time, written by InitGenesis on a
// fresh chain.
var KeyRewardScheduleStart = []byte("reward_schedule_start")

func (k Keeper) SetRewardScheduleStart(ctx sdk.Context, unix int64) {
	bz, _ := json.Marshal(unix)
	ctx.KVStore(k.storeKey).Set(KeyRewardScheduleStart, bz)
}

// GetRewardScheduleStart returns the schedule's start, and false if none
// is stored (every chain but a fresh one).
func (k Keeper) GetRewardScheduleStart(ctx sdk.Context) (int64, bool) {
	bz := ctx.KVStore(k.storeKey).Get(KeyRewardScheduleStart)
	if bz == nil {
		return 0, false
	}
	var unix int64
	if err := json.Unmarshal(bz, &unix); err != nil {
		return 0, false
	}
	return unix, true
}

// rewardYearsElapsed is how many whole years of the schedule have passed
// at height: years of block time since the start on a fresh chain,
// SecondsPerYear / TargetBlockTime blocks a year everywhere else.
func (k Keeper) rewardYearsElapsed(ctx sdk.Context, height int64) int64 {
	if gates.Fresh(ctx.ChainID()) {
		if start, ok := k.GetRewardScheduleStart(ctx); ok {
			elapsed := ctx.BlockTime().Unix() - start
			if elapsed < 0 {
				return 0
			}
			return elapsed / SecondsPerYear
		}
	}
	targetBlockTime := k.GetTargetBlockTime(ctx)
	if targetBlockTime <= 0 {
		targetBlockTime = 60 // sane fallback; should not occur in practice
	}
	return height / (SecondsPerYear / targetBlockTime)
}

// initRewardSchedule records the schedule's start on a fresh chain: the
// genesis file's reward_schedule_start if it gives one (an exported
// chain carries its original start), else the genesis time, which is
// the block time InitChain runs at.
func (k Keeper) initRewardSchedule(ctx sdk.Context, fromGenesis int64) {
	if !gates.Fresh(ctx.ChainID()) {
		return
	}
	start := fromGenesis
	if start <= 0 {
		start = ctx.BlockTime().Unix()
	}
	k.SetRewardScheduleStart(ctx, start)
}
