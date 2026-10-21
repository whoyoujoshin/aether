package pow_test

import (
	"fmt"
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

// The height starts an epoch, so the whole epoch runs under one liveness
// window, and comes after the seat rule, whose powers the cap measures.
func TestSeatHardening_Height(t *testing.T) {
	require.Zero(t, pow.SeatHardeningHeight%epochLen)
	require.Greater(t, pow.SeatHardeningHeight, pow.SeatRuleActivationHeight)
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

// seatNet sets up validators with keys and consensus addresses, active,
// and runs boundaries from hardEpoch with work per epoch until their
// powers have settled. It returns the next epoch.
func seatNet(t *testing.T, k pow.Keeper, ctx sdk.Context, work map[string]int64) (sdk.Context, int64) {
	t.Helper()
	for name := range work {
		v := sdk.AccAddress(name)
		k.SetValidatorPubkey(ctx, v, genValidatorPubkey(t))
		k.SetActiveValidator(ctx, v)
		k.SetConsensusToMiner(ctx, []byte("cons_"+name), v)
	}
	e := hardEpoch
	for ; e < hardEpoch+8; e++ {
		ctx = ctx.WithBlockHeight((e+1)*epochLen - 1)
		for name, w := range work {
			k.AddSeatWork(ctx, e, sdk.AccAddress(name), math.NewInt(w))
		}
		k.ComputeValidatorUpdates(ctx, e)
	}
	return ctx, e
}

// goOffline has name miss blocks until the liveness window removes it, or
// would have.
func goOffline(k pow.Keeper, ctx sdk.Context, name string) {
	for i := int64(0); i <= pow.LongLivenessWindow/2; i++ {
		k.CheckValidatorLiveness(setupLivenessCtx(ctx, testutil.FakeVoteInfos{fakeVoteInfo([]byte("cons_"+name), false)}))
	}
}

// A6, removal cap: downtime removals between boundaries take out at most
// 10% of the power. Of four validators with a quarter each, none is
// removed; of ten with a tenth each, one is and the next isn't, until the
// boundary resets the budget.
func TestSeatHardening_RemovalCap(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	ctx, e := seatNet(t, k, ctx, map[string]int64{"cap_four_a__________": 10, "cap_four_b__________": 10, "cap_four_c__________": 10, "cap_four_d__________": 10})
	ctx = ctx.WithBlockHeight(e*epochLen + 10)
	goOffline(k, ctx, "cap_four_a__________")
	require.True(t, k.IsActiveValidator(ctx, sdk.AccAddress("cap_four_a__________")), "25% is past the 10% cap: kept, still counted as missing")

	k, ctx, _ = setupKeeper(t)
	ten := map[string]int64{}
	for i := 0; i < 10; i++ {
		ten[fmt.Sprintf("cap_ten_%02d_________", i)] = 10
	}
	ctx, e = seatNet(t, k, ctx, ten)
	ctx = ctx.WithBlockHeight(e*epochLen + 10)
	goOffline(k, ctx, "cap_ten_00_________")
	goOffline(k, ctx, "cap_ten_01_________")
	require.False(t, k.IsActiveValidator(ctx, sdk.AccAddress("cap_ten_00_________")), "the first tenth is removed")
	require.True(t, k.IsActiveValidator(ctx, sdk.AccAddress("cap_ten_01_________")), "a second tenth in the same epoch is past the cap")

	// After the boundary the budget is fresh.
	ctx = ctx.WithBlockHeight((e+1)*epochLen - 1)
	for name, w := range ten {
		k.AddSeatWork(ctx, e, sdk.AccAddress(name), math.NewInt(w))
	}
	k.ComputeValidatorUpdates(ctx, e)
	ctx = ctx.WithBlockHeight((e+1)*epochLen + 10)
	goOffline(k, ctx, "cap_ten_01_________")
	require.False(t, k.IsActiveValidator(ctx, sdk.AccAddress("cap_ten_01_________")), "removed in the next epoch")
}

// A6: a validator removed for downtime gets its power back at the next
// boundary if its miner added work after the removal, outside the entry
// budget. One that didn't competes for that budget with a newcomer, as
// under the seat rule alone, and comes back with less.
func TestSeatHardening_ReservedPowerOnReturn(t *testing.T) {
	a, b, c := "reserve_big_________", "reserve_small_______", "reserve_newcomer____"
	got := map[bool]int64{}
	var before int64
	for _, back := range []bool{true, false} {
		k, ctx, _ := setupKeeper(t)
		ctx, e := seatNet(t, k, ctx, map[string]int64{a: 90, b: 10})
		var ok bool
		before, ok = k.GetValidatorPower(ctx, sdk.AccAddress(b))
		require.True(t, ok)
		require.InDelta(t, 10_000_000, before, 100_000)

		k.SetValidatorPubkey(ctx, sdk.AccAddress(c), genValidatorPubkey(t)) // registers, not seated
		ctx = ctx.WithBlockHeight(e*epochLen + 10)
		k.AddSeatWork(ctx, e, sdk.AccAddress(a), math.NewInt(90))
		k.AddSeatWork(ctx, e, sdk.AccAddress(b), math.NewInt(5))
		k.AddSeatWork(ctx, e, sdk.AccAddress(c), math.NewInt(40))
		goOffline(k, ctx, b)
		require.False(t, k.IsActiveValidator(ctx, sdk.AccAddress(b)), "a tenth is within the cap: removed")
		if back {
			k.AddSeatWork(ctx, e, sdk.AccAddress(b), math.NewInt(5)) // its miner is back
		}

		ctx = ctx.WithBlockHeight((e+1)*epochLen - 1)
		require.NotNil(t, k.ComputeValidatorUpdates(ctx, e))
		got[back] = powerOf(t, k, ctx, sdk.AccAddress(b))
		t.Logf("back=%v: power %d before, %d after", back, before, got[back])
		require.True(t, k.IsActiveValidator(ctx, sdk.AccAddress(c)), "the newcomer entered either way")
	}
	// Back, it moves from its own power toward its target like any seated
	// validator; still dark, it starts from nothing and shares the entry
	// budget with the newcomer.
	require.Greater(t, got[true], before*8/10)
	require.Less(t, got[false], got[true]*6/10)
}
