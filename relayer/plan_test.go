package relayer

import (
	"testing"
	"time"

	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	channeltypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	"github.com/stretchr/testify/require"
)

func TestTimedOut(t *testing.T) {
	at := time.Unix(1_000, 0)
	byHeight := channeltypes.Packet{TimeoutHeight: clienttypes.NewHeight(1, 50)}
	require.False(t, timedOut(byHeight, 49, at))
	require.True(t, timedOut(byHeight, 50, at))

	byTime := channeltypes.Packet{TimeoutTimestamp: uint64(at.UnixNano())}
	require.False(t, timedOut(byTime, 1_000_000, at.Add(-time.Nanosecond)))
	require.True(t, timedOut(byTime, 1, at))
}

func TestRefreshThreshold(t *testing.T) {
	const h = time.Hour
	for _, tc := range []struct {
		name               string
		trusting, after, w time.Duration
	}{
		// Aether's client of Osmosis: 80 h trusting period, Helicase's 5 min.
		{"refreshAfter shorter wins", 80 * h, 5 * time.Minute, 5 * time.Minute},
		// Osmosis's client of Aether: ~53 h, outbound's hourly default.
		{"hourly under a third", 53 * h, h, h},
		// A short trusting period keeps its safety margin.
		{"third of trusting period wins", 2 * h, h, 2 * h / 3},
		{"zero falls back to a third", 80 * h, 0, 80 * h / 3},
		{"negative falls back to a third", 80 * h, -time.Minute, 80 * h / 3},
	} {
		if got := refreshThreshold(tc.trusting, tc.after); got != tc.w {
			t.Errorf("%s: refreshThreshold(%v, %v) = %v, want %v", tc.name, tc.trusting, tc.after, got, tc.w)
		}
	}
}
