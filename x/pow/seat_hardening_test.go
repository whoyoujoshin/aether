package pow_test

import (
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/x/pow"
	"github.com/whoyoujoshin/aether/x/pow/testutil"
)

const epochLen = 1440

var hardEpoch = pow.SeatHardeningHeight / epochLen

func powerOf(t *testing.T, k pow.Keeper, ctx sdk.Context, a sdk.AccAddress) int64 {
	t.Helper()
	require.True(t, k.IsActiveValidator(ctx, a), "%s is seated", a)
	p, ok := k.GetValidatorPower(ctx, a)
	require.True(t, ok)
	return p
}

// A4/A5: an unregistered pool's work no longer counts toward the 1% bar.
// Under the seat rule alone it puts both validators under 1% and the set
// freezes; hardened, they're seated by their own work.
func TestSeatHardening_BarOnEligibleWorkOnly(t *testing.T) {
	for _, tc := range []struct {
		boundary int64
		frozen   bool
	}{{pow.SeatRuleActivationHeight + epochLen - 1, true}, {pow.SeatHardeningHeight + epochLen - 1, false}} {
		k, ctx, _ := setupKeeper(t)
		ctx = ctx.WithBlockHeight(tc.boundary)
		epoch := tc.boundary / epochLen
		a, b, pool := sdk.AccAddress("hardening_validator_a"), sdk.AccAddress("hardening_validator_b"), sdk.AccAddress("hardening_unreg_pool_")
		for _, v := range []sdk.AccAddress{a, b} {
			k.SetValidatorPubkey(ctx, v, genValidatorPubkey(t))
			k.SetActiveValidator(ctx, v)
		}
		k.AddSeatWork(ctx, epoch, a, math.NewInt(10))
		k.AddSeatWork(ctx, epoch, b, math.NewInt(10))
		k.AddSeatWork(ctx, epoch, pool, math.NewInt(5_000)) // 99.6% of the work, no key

		updates := k.ComputeValidatorUpdates(ctx, epoch)
		if tc.frozen {
			require.Nil(t, updates, "at %d nobody clears 1%% of all work: the set freezes", tc.boundary)
			continue
		}
		require.Len(t, updates, 2)
		require.Equal(t, int64(50_000_000), powerOf(t, k, ctx, a))
		require.Equal(t, int64(50_000_000), powerOf(t, k, ctx, b))
	}
}

// A6: the liveness window is LongLivenessWindow from the height, removing
// at more than half missed, and a bitmap kept under the old window starts
// over.
func TestSeatHardening_LongLivenessWindow(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	miner := sdk.AccAddress("hardening_liveness____")

	old := ctx.WithBlockHeight(pow.SeatHardeningHeight - 100)
	for i := 0; i < pow.LivenessWindowSize-1; i++ {
		k.RecordValidatorSigning(old, miner, false)
	}

	hard := ctx.WithBlockHeight(pow.SeatHardeningHeight)
	require.False(t, k.RecordValidatorSigning(hard, miner, false), "59 misses under the old window don't carry over")

	var remove bool
	for i := int64(1); i < pow.LongLivenessWindow; i++ {
		remove = k.RecordValidatorSigning(hard, miner, i%2 == 1) // misses at even slots, the first included: 720 in all
	}
	require.False(t, remove, "exactly half missed isn't more than half")
	remove = k.RecordValidatorSigning(hard, miner, false) // overwrites the first miss: still 720
	require.False(t, remove)
	remove = k.RecordValidatorSigning(hard, miner, false) // overwrites a signed slot: 721
	require.True(t, remove, "more than half of %d missed removes", pow.LongLivenessWindow)
}

// A6: a validator removed for downtime gets its power back at the next
// boundary if its miner added work after the removal; one that didn't
// re-enters through the entry limit, as under the seat rule alone.
func TestSeatHardening_ReservedPowerOnReturn(t *testing.T) {
	for _, back := range []bool{true, false} {
		k, ctx, _ := setupKeeper(t)
		a, b := sdk.AccAddress("hardening_reserve_a___"), sdk.AccAddress("hardening_reserve_b___")
		consB := []byte("hardening_cons_b______")
		for _, v := range []sdk.AccAddress{a, b} {
			k.SetValidatorPubkey(ctx, v, genValidatorPubkey(t))
			k.SetActiveValidator(ctx, v)
		}
		k.SetConsensusToMiner(ctx, consB, b)

		// Boundary 1: equal work, equal power.
		e0 := hardEpoch
		ctx = ctx.WithBlockHeight((e0+1)*epochLen - 1)
		k.AddSeatWork(ctx, e0, a, math.NewInt(10))
		k.AddSeatWork(ctx, e0, b, math.NewInt(10))
		require.NotNil(t, k.ComputeValidatorUpdates(ctx, e0))
		require.Equal(t, int64(50_000_000), powerOf(t, k, ctx, b))

		// Next epoch: b mines, then its node goes quiet for most of the window.
		e1 := e0 + 1
		ctx = ctx.WithBlockHeight(e1*epochLen + 10)
		k.AddSeatWork(ctx, e1, a, math.NewInt(10))
		k.AddSeatWork(ctx, e1, b, math.NewInt(5))
		for i := int64(0); i <= pow.LongLivenessWindow/2; i++ {
			k.CheckValidatorLiveness(setupLivenessCtx(ctx, testutil.FakeVoteInfos{fakeVoteInfo(consB, false)}))
		}
		require.False(t, k.IsActiveValidator(ctx, b), "removed for downtime")
		if back {
			k.AddSeatWork(ctx, e1, b, math.NewInt(5)) // its miner is back
		}

		ctx = ctx.WithBlockHeight((e1+1)*epochLen - 1)
		require.NotNil(t, k.ComputeValidatorUpdates(ctx, e1))
		if back {
			require.Equal(t, int64(50_000_000), powerOf(t, k, ctx, b), "back at its power, outside the entry budget")
			require.Equal(t, int64(50_000_000), powerOf(t, k, ctx, a))
		} else {
			require.InDelta(t, 10_000_000, powerOf(t, k, ctx, b), 1, "no sign of life: it re-enters at the entry limit")
			require.InDelta(t, 90_000_000, powerOf(t, k, ctx, a), 1)
		}
	}
}
