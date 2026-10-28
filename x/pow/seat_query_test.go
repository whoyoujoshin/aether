package pow_test

import (
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/whoyoujoshin/aether/x/pow"
)

// The SeatWork query reports the window the rule reads at the end of the
// epoch: the same work, eligibility and 1% bar, in seating order. What it
// says meets the bar is what the rule then seats.
func TestSeatWorkQuery(t *testing.T) {
	for _, tc := range []struct {
		name     string
		boundary int64
		bar      string
	}{
		// Before the hardening the bar counts every address's work.
		{"seat rule", pow.SeatRuleActivationHeight + epochLen - 1, "7610"},
		// From it, eligible work only (a, b, d).
		{"hardened", pow.SeatHardeningHeight + epochLen - 1, "2310"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, _ := setupKeeper(t)
			epoch := tc.boundary / epochLen
			ctx = ctx.WithBlockHeight(epoch*epochLen + 100) // mid-epoch
			a, b, banned, small, pool := sdk.AccAddress("seat_query_valid_a__"), sdk.AccAddress("seat_query_valid_b__"),
				sdk.AccAddress("seat_query_banned___"), sdk.AccAddress("seat_query_small____"), sdk.AccAddress("seat_query_unreg_pool")
			for _, v := range []sdk.AccAddress{a, b, banned, small} {
				k.SetValidatorPubkey(ctx, v, genValidatorPubkey(t))
			}
			k.SetActiveValidator(ctx, a)
			k.SetActiveValidator(ctx, b)
			k.SetBanned(ctx, banned)
			k.AddSeatWork(ctx, epoch-2, a, math.NewInt(1_000))
			k.AddSeatWork(ctx, epoch, a, math.NewInt(300))
			k.AddSeatWork(ctx, epoch-1, b, math.NewInt(1_000))
			k.AddSeatWork(ctx, epoch, banned, math.NewInt(300))
			k.AddSeatWork(ctx, epoch, small, math.NewInt(10))
			k.AddSeatWork(ctx, epoch, pool, math.NewInt(5_000))        // no consensus key
			k.AddSeatWork(ctx, epoch-3, b, math.NewInt(1_000_000))     // before the window
			k.AddSeatWork(ctx, epoch+1, small, math.NewInt(1_000_000)) // after it

			q := pow.NewQueryServerImpl(k)
			res, err := q.SeatWork(ctx, &pow.QuerySeatWorkRequest{})
			require.NoError(t, err)
			require.Equal(t, epoch, res.Epoch)
			require.Equal(t, epoch-2, res.FirstEpoch)
			require.Equal(t, tc.boundary, res.SelectionHeight)
			require.True(t, res.SeatRule)
			require.Equal(t, "7610", res.WindowTotal)
			require.Equal(t, tc.bar, res.BarTotal)
			require.Equal(t, pow.SeatMinWorkShareBps, res.MinShareBps)
			require.Equal(t, k.GetTopKSize(ctx), res.TopK)

			type row struct {
				addr           sdk.AccAddress
				window, epoch  string
				eligible, meet bool
			}
			want := []row{
				{pool, "5000", "5000", false, false},
				{a, "1300", "300", true, true},
				{b, "1000", "0", true, true},
				{banned, "300", "300", false, false},
				{small, "10", "10", true, false}, // under 1% either way
			}
			require.Len(t, res.Entries, len(want))
			for i, w := range want {
				e := res.Entries[i]
				require.Equal(t, w.addr.String(), e.Address, "entry %d", i)
				require.Equal(t, w.window, e.WindowWork, "%s window", e.Address)
				require.Equal(t, w.epoch, e.EpochWork, "%s epoch", e.Address)
				require.Equal(t, w.eligible, e.Eligible, "%s eligible", e.Address)
				require.Equal(t, w.meet, e.MeetsBar, "%s meets bar", e.Address)
			}

			// One address, and one with no seat work.
			one, err := q.SeatWork(ctx, &pow.QuerySeatWorkRequest{Address: b.String()})
			require.NoError(t, err)
			require.Len(t, one.Entries, 1)
			require.Equal(t, "1000", one.Entries[0].WindowWork)
			require.Equal(t, res.BarTotal, one.BarTotal)
			idle := sdk.AccAddress("seat_query_idle_____")
			k.SetValidatorPubkey(ctx, idle, genValidatorPubkey(t))
			none, err := q.SeatWork(ctx, &pow.QuerySeatWorkRequest{Address: idle.String()})
			require.NoError(t, err)
			require.Equal(t, &pow.SeatWorkInfo{Address: idle.String(), WindowWork: "0", EpochWork: "0", Eligible: true}, none.Entries[0])

			_, err = q.SeatWork(ctx, &pow.QuerySeatWorkRequest{Address: "not-an-address"})
			require.Equal(t, codes.InvalidArgument, status.Code(err))

			// The rule at the boundary seats exactly those the query said
			// meet the bar (TopK isn't binding here).
			ctx = ctx.WithBlockHeight(tc.boundary)
			require.NotNil(t, k.ComputeValidatorUpdates(ctx, epoch))
			for _, e := range res.Entries {
				acc := sdk.MustAccAddressFromBech32(e.Address)
				require.Equal(t, e.MeetsBar, k.IsActiveValidator(ctx, acc), "%s seated", e.Address)
			}
		})
	}
}

// Below the activation height the query reports the window but says the
// rule isn't in force.
func TestSeatWorkQuery_BeforeTheRule(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	ctx = ctx.WithBlockHeight(pow.SeatRuleActivationHeight - epochLen)
	res, err := pow.NewQueryServerImpl(k).SeatWork(ctx, &pow.QuerySeatWorkRequest{})
	require.NoError(t, err)
	require.False(t, res.SeatRule)
	require.Empty(t, res.Entries)
	require.Equal(t, "0", res.WindowTotal)
}
