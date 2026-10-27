package main

import (
	"testing"
	"time"
)

func TestRestFor(t *testing.T) {
	for _, c := range []struct {
		hashing time.Duration
		duty    float64
		want    time.Duration
	}{
		{100 * time.Second, 1, 0},
		{100 * time.Second, 0.5, 100 * time.Second},
		{60 * time.Second, 0.6, 40 * time.Second}, // 60 of every 100 s hashing
		{100 * time.Second, 0.25, 300 * time.Second},
		{0, 0.6, 0},
		{100 * time.Second, 0, 0}, // refused at startup; no rest rather than a divide by zero
	} {
		if got := restFor(c.hashing, c.duty); got != c.want {
			t.Errorf("restFor(%s, %g) = %s, want %s", c.hashing, c.duty, got, c.want)
		}
	}
}

// The share the duty buys: retargeting scales every miner's rate by the
// same factor, so only the rates' ratio matters. peer-1 did 42.2% of the
// work at block 472,590.
func TestDutyShare(t *testing.T) {
	share := func(duty, mine, others float64) float64 { return duty * mine / (duty*mine + others) }
	if got := share(0.6, 42.2, 57.8); got < 0.30 || got > 0.31 {
		t.Errorf("--duty 0.6 gives %.3f, want about 0.305", got)
	}
	if got := share(1, 42.2, 57.8); got < 0.4219 || got > 0.4221 {
		t.Errorf("--duty 1 changes the share: %.3f", got)
	}
}
