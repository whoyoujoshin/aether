package pow

import (
	"fmt"
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

func seatAddr(name string) sdk.AccAddress { return sdk.AccAddress(fmt.Sprintf("%-20s", name)) }

func cand(name string, work int64) seatCandidate {
	return seatCandidate{Addr: seatAddr(name), Work: math.NewInt(work), Eligible: true}
}

// share is the fraction of the seats' total power held by addresses whose
// name starts with prefix.
func share(seats []seat, prefix string) float64 {
	var mine, total int64
	for _, s := range seats {
		total += s.Power
		if string(s.Addr[:len(prefix)]) == prefix {
			mine += s.Power
		}
	}
	return float64(mine) / float64(total)
}

// The attack docs/MAINNET.md describes: 17 accounts with a little work
// each. Under the seat rule they hold what their work is worth, split or
// not.
func TestComputeSeats_SplittingGainsNothing(t *testing.T) {
	honest := []seatCandidate{cand("honest-a", 300), cand("honest-b", 300), cand("honest-c", 200), cand("honest-d", 200)}

	whole := append(append([]seatCandidate{}, honest...), cand("attacker", 340))
	one, ok := computeSeats(whole, nil, 21)
	require.True(t, ok)

	split := append([]seatCandidate{}, honest...)
	for i := 0; i < 17; i++ {
		split = append(split, cand(fmt.Sprintf("attacker-%02d", i), 20))
	}
	many, ok := computeSeats(split, nil, 21)
	require.True(t, ok)
	require.Len(t, many, 21)

	require.InDelta(t, 340.0/1340, share(one, "attacker"), 1e-6)
	require.InDelta(t, share(one, "attacker"), share(many, "attacker"), 1e-6, "17 accounts hold what one would")
	require.Less(t, share(many, "attacker"), 1.0/3)
}

// Accounts below 1% of the window's work don't qualify, and their work
// still counts toward the total, so dust can't lower the bar.
func TestComputeSeats_MinimumShare(t *testing.T) {
	cands := []seatCandidate{cand("big", 9_800), cand("dust-1", 50), cand("dust-2", 50), cand("enough", 200)}
	seats, ok := computeSeats(cands, nil, 21)
	require.True(t, ok)
	require.Len(t, seats, 2)
	require.Equal(t, seatAddr("big"), seats[0].Addr)
	require.Equal(t, seatAddr("enough"), seats[1].Addr)
	require.Equal(t, SeatPowerScale*98/100, seats[0].Power)
	require.Equal(t, SeatPowerScale*2/100, seats[1].Power)

	// Ineligible work (no consensus key, or banned) counts toward the
	// total but takes no seat.
	cands = []seatCandidate{cand("keyed", 100), {Addr: seatAddr("no-key"), Work: math.NewInt(10_000)}}
	_, ok = computeSeats(cands, nil, 21)
	require.False(t, ok, "keyed has under 1% of all work")
}

func TestComputeSeats_TopKAndOrder(t *testing.T) {
	cands := []seatCandidate{cand("c", 20), cand("a", 30), cand("b", 30), cand("d", 20)}
	seats, ok := computeSeats(cands, nil, 3)
	require.True(t, ok)
	require.Equal(t, []sdk.AccAddress{seatAddr("a"), seatAddr("b"), seatAddr("c")}, []sdk.AccAddress{seats[0].Addr, seats[1].Addr, seats[2].Addr})

	_, ok = computeSeats(nil, nil, 3)
	require.False(t, ok)
}

// A newcomer with 90% of the hash gains at most 10% of the power an
// epoch: past 1/3 only in its fourth epoch, past 2/3 only in its seventh.
func TestComputeSeats_EntryLimit(t *testing.T) {
	work := []seatCandidate{cand("honest-a", 25), cand("honest-b", 25), cand("honest-c", 25), cand("honest-d", 25), cand("newcomer", 900)}
	prev := []seat{
		{seatAddr("honest-a"), ValidatorVotingPower}, {seatAddr("honest-b"), ValidatorVotingPower},
		{seatAddr("honest-c"), ValidatorVotingPower}, {seatAddr("honest-d"), ValidatorVotingPower},
	}
	var shares []float64
	for epoch := 0; epoch < 9; epoch++ {
		seats, ok := computeSeats(work, prev, 21)
		require.True(t, ok)
		got := share(seats, "newcomer")
		if epoch > 0 {
			require.LessOrEqual(t, got-shares[epoch-1], 0.1+1e-6, "epoch %d", epoch)
		}
		shares = append(shares, got)
		prev = seats
	}
	require.InDelta(t, 0.1, shares[0], 1e-6)
	require.Less(t, shares[2], 1.0/3)
	require.Greater(t, shares[3], 1.0/3)
	require.Less(t, shares[5], 2.0/3)
	require.Greater(t, shares[6], 2.0/3)
	require.InDelta(t, 0.9, shares[8], 1e-6, "it reaches what its work is worth")
}

// Small shifts among the same validators aren't slowed down.
func TestComputeSeats_SmallShiftsApplyAtOnce(t *testing.T) {
	prev := []seat{{seatAddr("a"), 50_000_000}, {seatAddr("b"), 50_000_000}}
	seats, ok := computeSeats([]seatCandidate{cand("a", 55), cand("b", 45)}, prev, 21)
	require.True(t, ok)
	require.Equal(t, []seat{{seatAddr("a"), 55_000_000}, {seatAddr("b"), 45_000_000}}, seats)
}

// A validator that stops mining loses its power at the same pace, rather
// than all at once, and is gone once it rounds to nothing.
func TestComputeSeats_LeaverFadesOut(t *testing.T) {
	prev := []seat{{seatAddr("stays"), 50_000_000}, {seatAddr("leaves"), 50_000_000}}
	seats, ok := computeSeats([]seatCandidate{cand("stays", 100)}, prev, 21)
	require.True(t, ok)
	require.Len(t, seats, 2)
	require.InDelta(t, 0.4, share(seats, "leaves"), 1e-6)

	for i := 0; i < 5; i++ {
		seats, _ = computeSeats([]seatCandidate{cand("stays", 100)}, seats, 21)
	}
	require.Len(t, seats, 1)
	require.Equal(t, seatAddr("stays"), seats[0].Addr)
}

// Work up to AuxMaxDifficulty a submission, over a window, doesn't
// overflow the power arithmetic.
func TestComputeSeats_PoolScaleWork(t *testing.T) {
	pool := math.NewInt(AuxMaxDifficulty).MulRaw(3 * 1440)
	seats, ok := computeSeats([]seatCandidate{{Addr: seatAddr("pool"), Work: pool, Eligible: true}, cand("cpu", 1_000)}, nil, 21)
	require.True(t, ok)
	require.Equal(t, []seat{{seatAddr("pool"), SeatPowerScale}}, seats, "a CPU miner's work is far under 1% of a pool's")
}
