package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"cosmossdk.io/math"
)

const cons = "B7F08625AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

// validatorStatus is status(h) for a seated validator with a consensus key.
func validatorStatus(h int64) snapshot {
	s := status(h)
	s.ActiveValidator = true
	s.ConsensusAddress = cons
	return snap(s)
}

// signingWatcher watches addr with a 10-commit window, warning at 2
// misses, and prints events to out.
func signingWatcher(src *fakeSource, out *strings.Builder, cfg config) *watcher {
	cfg.lowBalance = math.ZeroInt()
	return &watcher{
		cfg:              cfg,
		addresses:        []string{addr},
		src:              src,
		out:              &deliverer{stdout: out, now: time.Now},
		endpoint:         "node:9090",
		state:            stateFile{Addresses: map[string]*addrState{}},
		unreachableAfter: 3,
		now:              time.Now,
	}
}

// delivered is each printed event's name and body, in order.
func delivered(t *testing.T, out *strings.Builder) ([]string, []map[string]any) {
	t.Helper()
	var names []string
	var bodies []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var b map[string]any
		if err := json.Unmarshal([]byte(line), &b); err != nil {
			t.Fatalf("bad line %q: %v", line, err)
		}
		names = append(names, b["event"].(string))
		bodies = append(bodies, b)
	}
	out.Reset()
	return names, bodies
}

func commits(from, to int64, signed bool, c map[int64]map[string]bool) {
	for h := from; h <= to; h++ {
		c[h] = map[string]bool{cons: signed, "OTHER": true}
	}
}

func TestSigningLowAndRecovered(t *testing.T) {
	c := map[int64]map[string]bool{}
	commits(1, 10, true, c)
	commits(11, 12, false, c) // two missed: the limit for 80% of 10
	commits(13, 30, true, c)
	src := &fakeSource{height: 10, snaps: map[string]snapshot{addr: validatorStatus(10)}, commits: c}
	var out strings.Builder
	w := signingWatcher(src, &out, config{signingWindow: 10, signingWarnBelow: 0.8})
	ctx := context.Background()

	w.poll(ctx) // reads the window back: all signed
	if names, _ := delivered(t, &out); len(names) != 0 {
		t.Fatalf("events = %v, want none", names)
	}

	src.height, src.snaps[addr] = 11, validatorStatus(11)
	w.poll(ctx)
	if names, _ := delivered(t, &out); len(names) != 0 {
		t.Fatalf("one miss: events = %v, want none", names)
	}

	src.height, src.snaps[addr] = 13, validatorStatus(13)
	w.poll(ctx)
	names, bodies := delivered(t, &out)
	if strings.Join(names, ",") != EventSigningLow {
		t.Fatalf("events = %v, want signing_low", names)
	}
	if bodies[0]["missed"] != float64(2) || bodies[0]["consensusAddress"] != cons || !strings.Contains(bodies[0]["message"].(string), "B7F08625") {
		t.Fatalf("body = %v", bodies[0])
	}

	src.height, src.snaps[addr] = 20, validatorStatus(20)
	w.poll(ctx) // 11 and 12 still in the last 10: no repeat
	if names, _ := delivered(t, &out); len(names) != 0 {
		t.Fatalf("still low: events = %v, want none", names)
	}

	src.height, src.snaps[addr] = 22, validatorStatus(22)
	w.poll(ctx) // window 13..22, all signed
	if names, _ := delivered(t, &out); strings.Join(names, ",") != EventSigningRecovered {
		t.Fatalf("events = %v, want signing_recovered", names)
	}
}

// Commits from before a validator was in the set don't count against it,
// and leaving the set starts its window over.
func TestSigningCountsOnlyWhileInTheSet(t *testing.T) {
	c := map[int64]map[string]bool{}
	for h := int64(1); h <= 10; h++ {
		c[h] = map[string]bool{"OTHER": true} // not in the set yet
	}
	commits(11, 11, true, c)
	src := &fakeSource{height: 11, snaps: map[string]snapshot{addr: validatorStatus(11)}, commits: c}
	var out strings.Builder
	w := signingWatcher(src, &out, config{signingWindow: 10, signingWarnBelow: 0.8})
	ctx := context.Background()
	w.poll(ctx)
	if names, _ := delivered(t, &out); len(names) != 0 {
		t.Fatalf("events = %v, want none", names)
	}
	if r := w.rings[cons]; r == nil || r.filled != 1 {
		t.Fatalf("ring = %+v, want one result", w.rings[cons])
	}

	gone := validatorStatus(12)
	gone.status.ActiveValidator = false
	src.height, src.snaps[addr] = 12, gone
	commits(12, 12, false, c)
	w.poll(ctx)
	delivered(t, &out)
	if _, ok := w.rings[cons]; ok {
		t.Fatal("out of the set: window kept")
	}
}

func TestPowerConcentratedAndEased(t *testing.T) {
	src := &fakeSource{height: 10, snaps: map[string]snapshot{addr: validatorStatus(10)},
		vals: map[string]int64{cons: 45, "SEED": 20, "SYNC3": 20, "SYNC4": 15}}
	var out strings.Builder
	w := signingWatcher(src, &out, config{concentrationAbove: 1.0 / 3})
	ctx := context.Background()

	w.poll(ctx)
	names, bodies := delivered(t, &out)
	if strings.Join(names, ",") != EventPowerConcentrated {
		t.Fatalf("events = %v, want power_concentrated", names)
	}
	if bodies[0]["address"] != addr || bodies[0]["consensusAddress"] != cons || bodies[0]["powerShare"] != "0.4500" {
		t.Fatalf("body = %v", bodies[0])
	}

	src.height, src.snaps[addr] = 11, validatorStatus(11)
	w.poll(ctx) // still over: no repeat
	if names, _ := delivered(t, &out); len(names) != 0 {
		t.Fatalf("events = %v, want none", names)
	}

	src.height, src.snaps[addr] = 12, validatorStatus(12)
	src.vals[cons] = 25 // 25 of 80: under a third
	w.poll(ctx)
	if names, _ := delivered(t, &out); strings.Join(names, ",") != EventPowerConcentrationEase {
		t.Fatalf("events = %v, want power_concentration_eased", names)
	}

	// A validator that isn't watched is named by its consensus address.
	src.height, src.snaps[addr] = 13, validatorStatus(13)
	src.vals["SEED"] = 200
	w.poll(ctx)
	names, bodies = delivered(t, &out)
	if strings.Join(names, ",") != EventPowerConcentrated || bodies[0]["address"] != nil || bodies[0]["consensusAddress"] != "SEED" {
		t.Fatalf("events = %v, body = %v", names, bodies)
	}
}

func TestMissLimit(t *testing.T) {
	for _, tc := range []struct {
		window int
		below  float64
		want   int
	}{{60, 0.8, 12}, {10, 0.8, 2}, {1440, 0.8, 288}, {60, 0.99, 1}} {
		if got := missLimit(tc.window, tc.below); got != tc.want {
			t.Errorf("missLimit(%d, %v) = %d, want %d", tc.window, tc.below, got, tc.want)
		}
	}
}
