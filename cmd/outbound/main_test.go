package main

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/relayer"
)

func TestAssess(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	interval, warn := 6*time.Second, 24*time.Hour

	_, err := assess(health{}, now, interval, warn)
	require.ErrorContains(t, err, "no successful cycle yet")

	h := health{LastSuccess: now.Add(-10 * time.Second), ClientExpires: now.Add(48 * time.Hour)}
	got, err := assess(h, now, interval, warn)
	require.NoError(t, err)
	require.True(t, got.OK)

	h.LastSuccess = now.Add(-time.Minute)
	_, err = assess(h, now, interval, warn)
	require.ErrorContains(t, err, "recently")

	h.LastSuccess = now
	h.ClientExpires = now.Add(time.Hour)
	_, err = assess(h, now, interval, warn)
	require.ErrorContains(t, err, "expires soon")
}

func TestStatusRecord(t *testing.T) {
	var st status
	st.record(&relayer.PlanResult{Update: true, Packets: 2, Acks: 1}, time.Time{}, nil)
	st.record(nil, time.Time{}, errors.New("rpc down"))
	h := st.snapshot()
	require.Equal(t, counts{Updates: 1, Packets: 2, Acks: 1, Successes: 1, Failures: 1}, h.Relayed)
	require.Equal(t, "rpc down", h.LastError)

	st.record(&relayer.PlanResult{}, time.Time{}, nil)
	require.Empty(t, st.snapshot().LastError, "a successful cycle clears the error")
}
