package pow_test

import (
	"fmt"
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/x/pow"
)

// Red-team of the seat-hardening keeper paths. These call the real
// keeper (CheckValidatorLiveness, ComputeValidatorUpdates,
// mayRemoveForDowntime via the public removal path). A failing
// assertion is the finding.

// Attack: use up the removal budget on purpose.
// With ten equal validators, the first ~10% of power can be removed.
// An attacker who DoSes the smallest ones first exhausts the cap, so
// a later offline validator stays seated even after it crosses the
// liveness threshold.
func TestRedTeam_RemovalBudgetExhaustedOnPurpose(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	ten := map[string]int64{}
	for i := 0; i < 10; i++ {
		ten[fmt.Sprintf("rt_budget_%02d______", i)] = 10
	}
	ctx, e := seatNet(t, k, ctx, ten)
	ctx = ctx.WithBlockHeight(e*epochLen + 10)

	goOffline(k, ctx, "rt_budget_00______")
	require.False(t, k.IsActiveValidator(ctx, sdk.AccAddress("rt_budget_00______")), "first 10% is removed")

	goOffline(k, ctx, "rt_budget_01______")
	require.True(t, k.IsActiveValidator(ctx, sdk.AccAddress("rt_budget_01______")), "budget spent: second stays")
	t.Logf("threshold: at most %d bps of power removable per epoch", pow.SeatEntryLimitBps)
}

// Attack: game the reserve by going dark and coming back with dust.
// A removed validator that adds any positive seat work after the
// removal counts as back: at the next boundary it starts from the power
// it held, outside the entry budget, and moves toward its share of the
// window's work like any seated validator. Dust buys no more than that:
// it ends where a validator that was never removed, with the same work,
// ends.
func TestRedTeam_ReserveReclaimedWithDustWork(t *testing.T) {
	big, small := "rt_reserve_big_____", "rt_reserve_small___"
	got := map[bool]int64{}
	for _, removed := range []bool{true, false} {
		k, ctx, _ := setupKeeper(t)
		ctx, e := seatNet(t, k, ctx, map[string]int64{big: 90, small: 10})
		ctx = ctx.WithBlockHeight(e*epochLen + 10)
		k.AddSeatWork(ctx, e, sdk.AccAddress(big), math.NewInt(90))
		if removed {
			goOffline(k, ctx, small)
			require.False(t, k.IsActiveValidator(ctx, sdk.AccAddress(small)))
		}
		k.AddSeatWork(ctx, e, sdk.AccAddress(small), math.NewInt(1)) // dust

		ctx = ctx.WithBlockHeight((e+1)*epochLen - 1)
		require.NotNil(t, k.ComputeValidatorUpdates(ctx, e))
		got[removed] = powerOf(t, k, ctx, sdk.AccAddress(small))
	}
	t.Logf("dust return: %d after a removal, %d never removed", got[true], got[false])
	require.Equal(t, got[false], got[true], "the reserve gives back what staying would have kept, no more")
}

// Attack: rotate the consensus key around a removal.
// The reserve is keyed by the miner address. A new address that
// registers a different key does not inherit the reserve; it is a
// newcomer and must enter through the 10% limit, as must the old
// address. The old validator holds under a tenth, so the removal cap
// lets it be removed.
func TestRedTeam_KeyRotationDoesNotInheritReserve(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	old, neu, peer := "rt_rotate_old______", "rt_rotate_new______", "rt_rotate_peer_____"
	ctx, e := seatNet(t, k, ctx, map[string]int64{old: 9, peer: 91})
	before := powerOf(t, k, ctx, sdk.AccAddress(old))
	ctx = ctx.WithBlockHeight(e*epochLen + 10)
	goOffline(k, ctx, old)
	require.False(t, k.IsActiveValidator(ctx, sdk.AccAddress(old)))

	k.SetValidatorPubkey(ctx, sdk.AccAddress(neu), genValidatorPubkey(t))
	k.AddSeatWork(ctx, e, sdk.AccAddress(neu), math.NewInt(9))
	k.AddSeatWork(ctx, e, sdk.AccAddress(peer), math.NewInt(91))

	ctx = ctx.WithBlockHeight((e+1)*epochLen - 1)
	require.NotNil(t, k.ComputeValidatorUpdates(ctx, e))
	// The old address added no work after the removal, so it gets no
	// reserve; its work earlier in the window can still seat it through
	// the entry limit, sharing that budget with the new address.
	oldP, _ := k.GetValidatorPower(ctx, sdk.AccAddress(old))
	newP, _ := k.GetValidatorPower(ctx, sdk.AccAddress(neu))
	t.Logf("old power %d before; after rotation: old address %d, new address %d", before, oldP, newP)
	require.Less(t, newP, before, "the new address does not inherit the old validator's power")
	require.LessOrEqual(t, oldP+newP, pow.SeatPowerScale*pow.SeatEntryLimitBps/10_000+1, "both enter through the entry limit")
}

// Attack: a ban interacts with the downtime cap.
// SetBanned removes eligibility at the boundary even when the
// downtime cap would have kept a 25% validator seated.
func TestRedTeam_BanIgnoresDowntimeCap(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	a, b := "rt_ban_a___________", "rt_ban_b___________"
	ctx, e := seatNet(t, k, ctx, map[string]int64{a: 25, b: 25, "rt_ban_c___________": 25, "rt_ban_d___________": 25})
	ctx = ctx.WithBlockHeight(e*epochLen + 10)

	goOffline(k, ctx, a)
	require.True(t, k.IsActiveValidator(ctx, sdk.AccAddress(a)), "25% is over the downtime cap")

	k.SetBanned(ctx, sdk.AccAddress(b))
	ctx = ctx.WithBlockHeight((e+1)*epochLen - 1)
	for _, name := range []string{a, b, "rt_ban_c___________", "rt_ban_d___________"} {
		k.AddSeatWork(ctx, e, sdk.AccAddress(name), math.NewInt(25))
	}
	k.ComputeValidatorUpdates(ctx, e)
	require.False(t, k.IsActiveValidator(ctx, sdk.AccAddress(b)), "ban removes even when downtime cap would keep")
	t.Log("threshold: ban path is not gated by mayRemoveForDowntime")
}
