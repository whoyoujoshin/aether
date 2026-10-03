package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cosmossdk.io/math"

	"github.com/whoyoujoshin/aether/wallet"
)

const addr = "aether1watched"

// status is a miner at height h of a 40-block epoch, registered,
// eligible and on track unless the test changes it.
func status(h int64) *wallet.MinerStatus {
	idx, start, sel := wallet.EpochWindow(h, 40)
	return &wallet.MinerStatus{
		Address: addr,
		Height:  h,
		Epoch: wallet.EpochStatus{
			Index: idx, Length: 40, StartHeight: start, SelectionHeight: sel,
			BlocksUntilSelection: sel - h,
		},
		RegisteredConsensusKey: true,
		WorkThisEpoch:          5,
		Eligible:               true,
		SelectionRule:          wallet.SelectionTopKByWork,
		TopKSize:               1,
		Rank:                   1,
		OnTrack:                true,
	}
}

func snap(s *wallet.MinerStatus, subs ...wallet.PoWSubmission) snapshot {
	return snapshot{status: s, balance: math.NewInt(10_000_000), subs: subs}
}

func names(evs []Event) []string {
	var out []string
	for _, e := range evs {
		out = append(out, e.Name)
	}
	return out
}

func requireEvents(t *testing.T, got []Event, want ...string) {
	t.Helper()
	if strings.Join(names(got), ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", names(got), want)
	}
}

func TestFirstPollIsBaselineOnly(t *testing.T) {
	var st addrState
	cfg := config{lowBalance: math.ZeroInt(), atRiskBlocks: 5}
	s := status(10)
	s.ActiveValidator = true
	evs := step(cfg, addr, &st, snap(s, wallet.PoWSubmission{Hash: "OLD", Height: 9}))
	requireEvents(t, evs)
	if !st.Active || st.SubHeight != 9 {
		t.Fatalf("baseline not recorded: %+v", st)
	}
}

func TestSubmissionsReportedOnceOldestFirst(t *testing.T) {
	var st addrState
	cfg := config{lowBalance: math.ZeroInt()}
	step(cfg, addr, &st, snap(status(10), wallet.PoWSubmission{Hash: "A", Height: 9}))

	evs := step(cfg, addr, &st, snap(status(12),
		wallet.PoWSubmission{Hash: "C", Height: 12, Code: 5, RawLog: "stale block hash"},
		wallet.PoWSubmission{Hash: "B", Height: 11},
		wallet.PoWSubmission{Hash: "A", Height: 9},
	))
	requireEvents(t, evs, EventSubmissionConfirmed, EventSubmissionFailed)
	if evs[0].Fields["txHash"] != "B" || evs[1].Fields["rawLog"] != "stale block hash" {
		t.Fatalf("wrong submissions: %+v", evs)
	}

	// A second tx in the same block as C, seen a poll later, still counts.
	evs = step(cfg, addr, &st, snap(status(13),
		wallet.PoWSubmission{Hash: "D", Height: 12},
		wallet.PoWSubmission{Hash: "C", Height: 12, Code: 5},
		wallet.PoWSubmission{Hash: "B", Height: 11},
	))
	requireEvents(t, evs, EventSubmissionConfirmed)
	if evs[0].Fields["txHash"] != "D" {
		t.Fatalf("got %v", evs[0].Fields)
	}
	requireEvents(t, step(cfg, addr, &st, snap(status(14), wallet.PoWSubmission{Hash: "D", Height: 12})))
}

func TestSelectedAtEpochBoundary(t *testing.T) {
	var st addrState
	cfg := config{lowBalance: math.ZeroInt()}
	step(cfg, addr, &st, snap(status(30)))

	s := status(39) // the selection block itself already shows the new set
	s.ActiveValidator = true
	evs := step(cfg, addr, &st, snap(s))
	requireEvents(t, evs, EventValidatorSelected)
	f := evs[0].Fields
	if f["epoch"] != int64(1) || f["selectedAt"] != int64(39) || f["effectiveFrom"] != int64(41) {
		t.Fatalf("fields = %v", f)
	}

	// Seen from inside the next epoch instead, the numbers agree.
	st = addrState{}
	step(cfg, addr, &st, snap(status(30)))
	s = status(45)
	s.ActiveValidator = true
	evs = step(cfg, addr, &st, snap(s))
	if f := evs[0].Fields; f["epoch"] != int64(1) || f["selectedAt"] != int64(39) {
		t.Fatalf("fields = %v", f)
	}
	if evs[0].ID() == "" || evs[0].ID() != (Event{Name: EventValidatorSelected, Address: addr, key: "1"}).ID() {
		t.Fatal("ID isn't derived from the epoch")
	}
}

func TestRemovalReasons(t *testing.T) {
	cfg := config{lowBalance: math.ZeroInt()}
	active := func(h int64) *wallet.MinerStatus { s := status(h); s.ActiveValidator = true; return s }

	for _, tc := range []struct {
		name   string
		next   *wallet.MinerStatus
		reason string
		also   []string
	}{
		{"boundary passed", status(41), RemovedNotReselected, nil},
		{"mid epoch", status(35), RemovedDowntime, nil},
		{"banned", func() *wallet.MinerStatus { s := status(35); s.Banned = true; return s }(), RemovedBanned, []string{EventMinerBanned}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var st addrState
			step(cfg, addr, &st, snap(active(30)))
			evs := step(cfg, addr, &st, snap(tc.next))
			requireEvents(t, evs, append(tc.also, EventValidatorRemoved)...)
			if got := evs[len(evs)-1].Fields["reason"]; got != tc.reason {
				t.Fatalf("reason = %v, want %s", got, tc.reason)
			}
		})
	}
}

func TestAtRiskOncePerEpochAndOnlyForRegistered(t *testing.T) {
	cfg := config{lowBalance: math.ZeroInt(), atRiskBlocks: 5}
	var st addrState
	step(cfg, addr, &st, snap(status(20)))

	notOnTrack := func(h int64) *wallet.MinerStatus {
		s := status(h)
		s.Rank, s.OnTrack = 2, false
		return s
	}
	requireEvents(t, step(cfg, addr, &st, snap(notOnTrack(30)))) // 9 blocks out: too early
	evs := step(cfg, addr, &st, snap(notOnTrack(35)))
	requireEvents(t, evs, EventSelectionAtRisk)
	if evs[0].Fields["rank"] != 2 || evs[0].Fields["blocksUntilSelection"] != int64(4) {
		t.Fatalf("fields = %v", evs[0].Fields)
	}
	requireEvents(t, step(cfg, addr, &st, snap(notOnTrack(37)))) // same epoch: once
	requireEvents(t, step(cfg, addr, &st, snap(notOnTrack(75))), EventSelectionAtRisk)

	// On track: nothing. No consensus key: not a candidate, nothing.
	st = addrState{}
	step(cfg, addr, &st, snap(status(20)))
	requireEvents(t, step(cfg, addr, &st, snap(status(36))))
	noKey := notOnTrack(37)
	noKey.RegisteredConsensusKey, noKey.Eligible = false, false
	requireEvents(t, step(cfg, addr, &st, snap(noKey)))

	// No work yet is at risk, with the reason.
	st = addrState{}
	step(cfg, addr, &st, snap(status(20)))
	idle := status(36)
	idle.WorkThisEpoch, idle.Eligible, idle.OnTrack, idle.Rank = 0, false, false, 0
	idle.NotEligibleBecause = []string{wallet.IneligibleNoWork}
	evs = step(cfg, addr, &st, snap(idle))
	requireEvents(t, evs, EventSelectionAtRisk)
	if _, ok := evs[0].Fields["notEligibleBecause"]; !ok {
		t.Fatal("reason missing")
	}
}

func TestNoWorkHalfwayOncePerEpoch(t *testing.T) {
	cfg := config{lowBalance: math.ZeroInt(), noWorkAt: 0.5}
	idle := func(h int64) *wallet.MinerStatus {
		s := status(h)
		s.ActiveValidator = true
		s.WorkThisEpoch, s.Eligible, s.OnTrack, s.Rank = 0, false, false, 0
		s.NotEligibleBecause = []string{wallet.IneligibleNoWork}
		return s
	}
	var st addrState
	requireEvents(t, step(cfg, addr, &st, snap(idle(5))))  // 6 of 40 blocks in: too early
	requireEvents(t, step(cfg, addr, &st, snap(idle(18)))) // 19 of 40
	evs := step(cfg, addr, &st, snap(idle(19)))            // halfway
	requireEvents(t, evs, EventNoWorkThisEpoch)
	if evs[0].Fields["blocksUntilSelection"] != int64(20) || evs[0].Fields["activeValidator"] != true {
		t.Fatalf("fields = %v", evs[0].Fields)
	}
	if msg, _ := evs[0].Fields["message"].(string); !strings.Contains(msg, "leaves the validator set at height 39") {
		t.Fatalf("message = %q", msg)
	}
	requireEvents(t, step(cfg, addr, &st, snap(idle(30)))) // same epoch: once
	requireEvents(t, step(cfg, addr, &st, snap(idle(60))), EventNoWorkThisEpoch)

	// Work this epoch, no consensus key, or banned: nothing.
	st = addrState{}
	requireEvents(t, step(cfg, addr, &st, snap(status(25))))
	noKey := idle(26)
	noKey.RegisteredConsensusKey, noKey.ActiveValidator = false, false
	st = addrState{}
	requireEvents(t, step(cfg, addr, &st, snap(noKey)))
	banned := idle(27)
	banned.Banned = true
	st = addrState{}
	requireEvents(t, step(cfg, addr, &st, snap(banned)))

	// Off by default in config{}: the zero value never warns.
	st = addrState{}
	requireEvents(t, step(config{lowBalance: math.ZeroInt()}, addr, &st, snap(idle(30))))
}

func TestBalanceAlertsWithHysteresis(t *testing.T) {
	cfg := config{lowBalance: math.NewInt(5_000_000)}
	var st addrState
	low := snap(status(10))
	low.balance = math.NewInt(1_000_000)

	evs := step(cfg, addr, &st, low) // already low when the watch starts: say so
	requireEvents(t, evs, EventBalanceLow)
	if evs[0].Fields["balance"] != "1 AETH" {
		t.Fatalf("fields = %v", evs[0].Fields)
	}
	low.status = status(11)
	requireEvents(t, step(cfg, addr, &st, low))
	requireEvents(t, step(cfg, addr, &st, snap(status(12))), EventBalanceRecovered)
	requireEvents(t, step(cfg, addr, &st, snap(status(13))))
}

// fakeSource answers from a script, or fails while down is set.
type fakeSource struct {
	height int64
	snaps  map[string]snapshot
	down   bool
}

func (f *fakeSource) LatestHeight(context.Context) (int64, error) {
	if f.down {
		return 0, errors.New("connection refused")
	}
	return f.height, nil
}

func (f *fakeSource) Snapshot(_ context.Context, a string) (snapshot, error) {
	return f.snaps[a], nil
}

type hook struct {
	mu     sync.Mutex
	bodies []map[string]any
	sigs   []string
	fail   int // answer 500 this many times first
}

func (h *hook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.fail > 0 {
		h.fail--
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	bz, _ := io.ReadAll(r.Body)
	var b map[string]any
	json.Unmarshal(bz, &b)
	if sign("s3cret", bz) != r.Header.Get(SignatureHeader) {
		b["badSignature"] = true
	}
	if r.Header.Get("X-Aether-Event") != b["event"] {
		b["badEventHeader"] = true
	}
	h.bodies = append(h.bodies, b)
}

func TestWatcherDeliversSignedAndReportsNodeOutage(t *testing.T) {
	h := &hook{fail: 1}
	srv := httptest.NewServer(h)
	defer srv.Close()

	src := &fakeSource{height: 10, snaps: map[string]snapshot{addr: snap(status(10))}}
	var stdout strings.Builder
	w := &watcher{
		cfg:       config{lowBalance: math.ZeroInt()},
		addresses: []string{addr},
		src:       src,
		out: &deliverer{webhook: srv.URL, secret: "s3cret", stdout: &stdout, client: srv.Client(),
			backoff: []time.Duration{time.Millisecond}, now: time.Now},
		endpoint:         "node:9090",
		statePath:        filepath.Join(t.TempDir(), "state.json"),
		state:            stateFile{Addresses: map[string]*addrState{}},
		unreachableAfter: 2,
		now:              time.Now,
	}
	ctx := context.Background()
	w.poll(ctx)

	s := status(12)
	src.height, src.snaps[addr] = 12, snap(s, wallet.PoWSubmission{Hash: "TX1", Height: 12})
	w.poll(ctx) // the first delivery gets a 500 and is retried
	w.poll(ctx) // same height: nothing new

	src.down = true
	w.poll(ctx)
	w.poll(ctx)
	w.poll(ctx) // only one outage alert
	src.down = false
	src.height = 13
	src.snaps[addr] = snap(status(13), wallet.PoWSubmission{Hash: "TX1", Height: 12})
	w.poll(ctx)

	var got []string
	for _, b := range h.bodies {
		if b["badSignature"] == true || b["badEventHeader"] == true {
			t.Fatalf("bad delivery: %v", b)
		}
		got = append(got, b["event"].(string))
	}
	want := []string{EventSubmissionConfirmed, EventNodeUnreachable, EventNodeRecovered}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("webhook got %v, want %v", got, want)
	}
	if b := h.bodies[0]; b["txHash"] != "TX1" || b["address"] != addr || b["height"] != float64(12) || b["id"] == "" {
		t.Fatalf("body = %v", b)
	}
	if b := h.bodies[1]; b["node"] != "node:9090" || b["consecutiveFailures"] != float64(2) {
		t.Fatalf("body = %v", b)
	}
	if lines := strings.Count(stdout.String(), "\n"); lines != 3 {
		t.Fatalf("stdout has %d lines:\n%s", lines, stdout.String())
	}

	// A restart from the saved state doesn't repeat TX1.
	saved, err := loadState(w.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if st := saved.Addresses[addr]; st == nil || st.SubHeight != 12 {
		t.Fatalf("state = %+v", saved.Addresses[addr])
	}
	requireEvents(t, step(w.cfg, addr, saved.Addresses[addr], snap(status(14), wallet.PoWSubmission{Hash: "TX1", Height: 12})))
}

func TestParseAddresses(t *testing.T) {
	a := "aether10pt75nhcvjr8dhs4su3sfcama67u32ju54ugusnyc00c0tlhages897tfw"
	got, err := parseAddresses(a+", "+a, []string{a})
	if err != nil || len(got) != 1 {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := parseAddresses("", nil); err == nil {
		t.Fatal("no address accepted")
	}
	if _, err := parseAddresses("cosmos1xyz", nil); err == nil {
		t.Fatal("bad address accepted")
	}
}

func TestChainStalledAndResumed(t *testing.T) {
	clock := time.Unix(1_700_000_000, 0)
	src := &fakeSource{height: 10, snaps: map[string]snapshot{addr: snap(status(10))}}
	var stdout strings.Builder
	w := &watcher{
		cfg:        config{lowBalance: math.ZeroInt()},
		addresses:  []string{addr},
		src:        src,
		out:        &deliverer{stdout: &stdout, now: func() time.Time { return clock }},
		endpoint:   "node:9090",
		state:      stateFile{Addresses: map[string]*addrState{}},
		stallAfter: 5 * time.Minute,
		now:        func() time.Time { return clock },
	}
	ctx := context.Background()
	w.poll(ctx)
	for i := 0; i < 12; i++ { // an hour at the same height: one alert
		clock = clock.Add(5 * time.Minute)
		w.poll(ctx)
	}
	src.height, src.snaps[addr] = 11, snap(status(11))
	clock = clock.Add(time.Minute)
	w.poll(ctx)

	var got []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		var b map[string]any
		if err := json.Unmarshal([]byte(line), &b); err != nil {
			t.Fatal(err)
		}
		got = append(got, b)
	}
	if len(got) != 2 || got[0]["event"] != EventChainStalled || got[1]["event"] != EventChainResumed {
		t.Fatalf("got %v", got)
	}
	if got[0]["noNewBlockSeconds"] != float64(300) || got[1]["stalledAt"] != float64(10) || got[1]["stalledSeconds"] != float64(3660) {
		t.Fatalf("got %v", got)
	}
}
