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
// removal reclaims its full prior power at the next boundary, outside
// the entry budget. Dust is enough.
func TestRedTeam_ReserveReclaimedWithDustWork(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	big, small := "rt_reserve_big_____", "rt_reserve_small___"
	ctx, e := seatNet(t, k, ctx, map[string]int64{big: 90, small: 10})
	before, ok := k.GetValidatorPower(ctx, sdk.AccAddress(small))
	require.True(t, ok)
	require.InDelta(t, 10_000_000, before, 100_000)

	ctx = ctx.WithBlockHeight(e*epochLen + 10)
	k.AddSeatWork(ctx, e, sdk.AccAddress(big), math.NewInt(90))
	goOffline(k, ctx, small)
	require.False(t, k.IsActiveValidator(ctx, sdk.AccAddress(small)))

	k.AddSeatWork(ctx, e, sdk.AccAddress(small), math.NewInt(1)) // dust

	ctx = ctx.WithBlockHeight((e+1)*epochLen - 1)
	require.NotNil(t, k.ComputeValidatorUpdates(ctx, e))
	after := powerOf(t, k, ctx, sdk.AccAddress(small))
	t.Logf("dust reclaim: power %d before, %d after (threshold: any positive work)", before, after)
	require.Greater(t, after, before*8/10, "dust work reclaims nearly full prior power")
}

// Attack: rotate the consensus key around a removal.
// The reserve is keyed by the miner address. A new address that
// registers a different key does not inherit the reserve; it is a
// newcomer and must enter through the 10% limit.
func TestRedTeam_KeyRotationDoesNotInheritReserve(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	old, neu := "rt_rotate_old______", "rt_rotate_new______"
	ctx, e := seatNet(t, k, ctx, map[string]int64{old: 50, "rt_rotate_peer_____": 50})
	ctx = ctx.WithBlockHeight(e*epochLen + 10)
	goOffline(k, ctx, old)
	require.False(t, k.IsActiveValidator(ctx, sdk.AccAddress(old)))

	k.SetValidatorPubkey(ctx, sdk.AccAddress(neu), genValidatorPubkey(t))
	k.AddSeatWork(ctx, e, sdk.AccAddress(neu), math.NewInt(50))
	k.AddSeatWork(ctx, e, sdk.AccAddress("rt_rotate_peer_____"), math.NewInt(50))

	ctx = ctx.WithBlockHeight((e+1)*epochLen - 1)
	require.NotNil(t, k.ComputeValidatorUpdates(ctx, e))
	require.False(t, k.IsActiveValidator(ctx, sdk.AccAddress(old)), "old address does not return")
	if k.IsActiveValidator(ctx, sdk.AccAddress(neu)) {
		p := powerOf(t, k, ctx, sdk.AccAddress(neu))
		t.Logf("newcomer power after rotation: %d (entry-limited, not inherited)", p)
		require.Less(t, p, int64(15_000_000), "does not inherit the old validator's power")
	}
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
