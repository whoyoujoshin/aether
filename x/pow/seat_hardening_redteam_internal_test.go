package pow

import (
	"fmt"
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

// Red-team of the seat-hardening arithmetic. These call the real
// computeSeats. A failing assertion is the finding.

func rtAddr(s string) sdk.AccAddress { return sdk.AccAddress(s) }

// Attack: sybils split work to sit just above the 1% bar.
// With eligibleTotal, each account needs >=1% of eligible work.
// Splitting does not increase total power beyond the work share,
// because power is proportional to work. The threshold is the work
// share itself, not the number of seats.
func TestRedTeam_SybilJustAboveOnePercent(t *testing.T) {
	var cands []seatCandidate
	for i := 0; i < 10; i++ {
		cands = append(cands, seatCandidate{Addr: rtAddr(fmt.Sprintf("sybil_%02d", i)), Work: math.NewInt(11), Eligible: true})
	}
	cands = append(cands, seatCandidate{Addr: rtAddr("honest"), Work: math.NewInt(890), Eligible: true})
	prev := []seat{{Addr: rtAddr("honest"), Power: SeatPowerScale}}

	seats, ok := computeSeats(cands, prev, 21, true)
	require.True(t, ok)
	var sybilPower int64
	for _, s := range seats {
		if !s.Addr.Equals(rtAddr("honest")) {
			sybilPower += s.Power
		}
	}
	t.Logf("10 sybils at 1.1%% each hold %d power (threshold: work share, not seat count)", sybilPower)
	// From a seated incumbent, the entry limit moves at most 10% of
	// total power toward the sybils in one boundary.
	require.LessOrEqual(t, sybilPower, int64(12_000_000), "sybils do not exceed their work share plus entry slack")
}

// Attack: newcomers race the 10% entry limit.
// A newcomer whose target is 90% of power still gains at most
// SeatEntryLimitBps in one boundary.
func TestRedTeam_NewcomerCappedAtTenPercent(t *testing.T) {
	prev := []seat{{Addr: rtAddr("incumbent"), Power: SeatPowerScale}}
	cands := []seatCandidate{
		{Addr: rtAddr("incumbent"), Work: math.NewInt(10), Eligible: true},
		{Addr: rtAddr("newcomer"), Work: math.NewInt(90), Eligible: true},
	}
	seats, ok := computeSeats(cands, prev, 21, true)
	require.True(t, ok)
	var newPower int64
	for _, s := range seats {
		if s.Addr.Equals(rtAddr("newcomer")) {
			newPower = s.Power
		}
	}
	t.Logf("newcomer from 0 toward 90%% in one boundary: %d (cap %d)", newPower, SeatPowerScale*SeatEntryLimitBps/10_000)
	require.Greater(t, newPower, int64(0))
	require.LessOrEqual(t, newPower, SeatPowerScale*SeatEntryLimitBps/10_000+1_000_000)
}

// Consensus safety: computeSeats is deterministic. Candidate order
// must not change the result (maps are iterated via the ordered slice).
func TestRedTeam_ComputeSeatsDeterministic(t *testing.T) {
	a, b, c := rtAddr("det_a"), rtAddr("det_b"), rtAddr("det_c")
	order1 := []seatCandidate{
		{Addr: a, Work: math.NewInt(30), Eligible: true},
		{Addr: b, Work: math.NewInt(40), Eligible: true},
		{Addr: c, Work: math.NewInt(30), Eligible: true},
	}
	order2 := []seatCandidate{
		{Addr: c, Work: math.NewInt(30), Eligible: true},
		{Addr: a, Work: math.NewInt(30), Eligible: true},
		{Addr: b, Work: math.NewInt(40), Eligible: true},
	}
	prev := []seat{{Addr: a, Power: 50_000_000}, {Addr: b, Power: 50_000_000}}
	s1, ok1 := computeSeats(order1, prev, 21, true)
	s2, ok2 := computeSeats(order2, prev, 21, true)
	require.True(t, ok1 && ok2)
	require.Equal(t, len(s1), len(s2))
	for i := range s1 {
		require.True(t, s1[i].Addr.Equals(s2[i].Addr), "addr order")
		require.Equal(t, s1[i].Power, s2[i].Power, "power")
	}
}

// Consensus safety: the entry-limit arithmetic does not overflow at
// SeatPowerScale. The intermediate (c*(gain-limit) + t*limit) stays
// inside int64 for the documented scale.
func TestRedTeam_EntryLimitNoOverflow(t *testing.T) {
	prev := []seat{{Addr: rtAddr("incumbent"), Power: SeatPowerScale}}
	cands := []seatCandidate{
		{Addr: rtAddr("incumbent"), Work: math.NewInt(1), Eligible: true},
		{Addr: rtAddr("newcomer"), Work: math.NewInt(99), Eligible: true},
	}
	seats, ok := computeSeats(cands, prev, 21, true)
	require.True(t, ok)
	require.NotEmpty(t, seats)
	var sum int64
	for _, s := range seats {
		sum += s.Power
		require.GreaterOrEqual(t, s.Power, int64(0))
	}
	require.InDelta(t, SeatPowerScale, sum, float64(SeatPowerScale)/100)
}
