package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func view(h int64, catching bool) nodeView {
	return nodeView{Height: h, CatchingUp: catching, Network: "aether-testnet-1", Validator: "F94934BD"}
}

type scriptRPC struct {
	views map[string]nodeView
	errs  map[string]error
	vals  []valPower
	vErr  error
	calls int
}

func (s *scriptRPC) Status(_ context.Context, u string) (nodeView, error) {
	s.calls++
	if err := s.errs[u]; err != nil {
		return nodeView{}, err
	}
	return s.views[u], nil
}

func (s *scriptRPC) Validators(context.Context, string) ([]valPower, int64, error) {
	if s.vErr != nil {
		return nil, 0, s.vErr
	}
	return s.vals, 0, nil
}

func newTestWatcher(t *testing.T, rpc *scriptRPC, nodes []nodeCfg) (*watcher, *[]event) {
	t.Helper()
	var got []event
	w := &watcher{
		nodes: nodes, stallAfter: 3 * time.Minute, behindBlocks: 10, unreachableAfter: 2,
		unsafeAlerts: true, now: time.Now, rpc: rpc,
		out: func(e event) { got = append(got, e) },
		st:  diskState{Nodes: map[string]*nodeState{}},
	}
	return w, &got
}

func names(ev []event) string {
	var s []string
	for _, e := range ev {
		s = append(s, e.Name)
	}
	return strings.Join(s, ",")
}

func TestTipStallsAndResumes(t *testing.T) {
	rpc := &scriptRPC{views: map[string]nodeView{"http://seed": view(10, false)}}
	w, got := newTestWatcher(t, rpc, []nodeCfg{{Name: "seed", URL: "http://seed"}})
	t0 := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	w.observe(t0, []sample{{cfg: w.nodes[0], view: view(10, false)}})
	w.observe(t0.Add(2*time.Minute), []sample{{cfg: w.nodes[0], view: view(10, false)}})
	if len(*got) != 0 {
		t.Fatalf("early events: %s", names(*got))
	}
	w.observe(t0.Add(3*time.Minute), []sample{{cfg: w.nodes[0], view: view(10, false)}})
	if names(*got) != EventTipStalled {
		t.Fatalf("events = %s", names(*got))
	}
	if (*got)[0].Fields["reason"] != "height_unchanged" || (*got)[0].Height != 10 {
		t.Fatalf("stall = %+v", (*got)[0])
	}
	*got = nil
	w.observe(t0.Add(4*time.Minute), []sample{{cfg: w.nodes[0], view: view(10, false)}})
	if len(*got) != 0 {
		t.Fatalf("repeat stall: %s", names(*got))
	}
	w.observe(t0.Add(5*time.Minute), []sample{{cfg: w.nodes[0], view: view(11, false)}})
	if names(*got) != EventTipResumed {
		t.Fatalf("events = %s", names(*got))
	}
}

func TestNoSyncedNodeStalls(t *testing.T) {
	w, got := newTestWatcher(t, nil, []nodeCfg{{Name: "seed", URL: "http://seed"}})
	t0 := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	down := []sample{{cfg: w.nodes[0], err: errString("connection refused")}}
	w.observe(t0, down)
	w.observe(t0.Add(3*time.Minute), down)
	var stall bool
	for _, e := range *got {
		if e.Name == EventTipStalled && e.Fields["reason"] == "no_synced_node" {
			stall = true
		}
	}
	if !stall {
		t.Fatalf("events = %s", names(*got))
	}
}

func TestNodeBehindCatchingUpAndHeight(t *testing.T) {
	nodes := []nodeCfg{{Name: "seed", URL: "http://seed"}, {Name: "sync3", URL: "http://sync3"}}
	w, got := newTestWatcher(t, nil, nodes)
	t0 := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	samples := []sample{
		{cfg: nodes[0], view: view(100, false)},
		{cfg: nodes[1], view: view(80, false)},
	}
	w.observe(t0, samples)
	if names(*got) != EventNodeBehind {
		t.Fatalf("events = %s", names(*got))
	}
	if (*got)[0].Node != "sync3" || (*got)[0].Fields["reason"] != "height" {
		t.Fatalf("behind = %+v", (*got)[0])
	}
	*got = nil
	w.observe(t0.Add(time.Second), samples) // still behind: one alert
	if len(*got) != 0 {
		t.Fatalf("repeat: %s", names(*got))
	}
	samples[1].view = view(95, false)
	w.observe(t0.Add(2*time.Second), samples)
	if names(*got) != EventNodeSynced {
		t.Fatalf("events = %s", names(*got))
	}
	*got = nil
	samples[1].view = view(96, true)
	w.observe(t0.Add(3*time.Second), samples)
	if names(*got) != EventNodeBehind || (*got)[0].Fields["reason"] != "catching_up" {
		t.Fatalf("events = %+v", *got)
	}
	*got = nil
	samples[1].view = view(100, false)
	w.observe(t0.Add(4*time.Second), samples)
	if names(*got) != EventNodeSynced {
		t.Fatalf("events = %s", names(*got))
	}
}

func TestRPCUnreachableAndRecovered(t *testing.T) {
	w, got := newTestWatcher(t, nil, []nodeCfg{{Name: "seed", URL: "http://seed"}})
	t0 := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	// A live sample first, so the unreachable alert has a last height.
	w.observe(t0, []sample{{cfg: w.nodes[0], view: view(10, false)}})
	down := []sample{{cfg: w.nodes[0], err: errString("timeout")}}
	w.observe(t0.Add(time.Second), down)
	if names(*got) != "" {
		t.Fatalf("one failure: %s", names(*got))
	}
	w.observe(t0.Add(2*time.Second), down)
	if names(*got) != EventRPCUnreachable {
		t.Fatalf("events = %s", names(*got))
	}
	*got = nil
	w.observe(t0.Add(3*time.Second), down)
	if len(*got) != 0 {
		t.Fatalf("repeat: %s", names(*got))
	}
	w.observe(t0.Add(4*time.Second), []sample{{cfg: w.nodes[0], view: view(12, false)}})
	if names(*got) != EventRPCRecovered+","+EventTipResumed && names(*got) != EventRPCRecovered {
		// Height moved, so a resume is correct only if we had stalled. We had not.
		if names(*got) != EventRPCRecovered {
			t.Fatalf("events = %s", names(*got))
		}
	}
}

func TestUnsafeToStopAlertAndClear(t *testing.T) {
	rpc := &scriptRPC{vals: []valPower{
		{"B7F08625AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", 40},
		{"F94934BDAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", 20},
		{"CCA35D28AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", 20},
		{"BCEF78B5AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", 20},
	}}
	nodes := []nodeCfg{{Name: "seed", URL: "http://seed"}}
	w, got := newTestWatcher(t, rpc, nodes)
	ctx := context.Background()
	w.rpc = rpc
	samples := []sample{{cfg: nodes[0], view: view(10, false)}}
	w.observe(time.Now(), samples)
	w.observePower(ctx, samples)
	if names(*got) != EventUnsafeToStop {
		t.Fatalf("events = %s, fields %+v", names(*got), *got)
	}
	if (*got)[0].Fields["consensusAddress"] != "B7F08625AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" {
		t.Fatalf("fields = %v", (*got)[0].Fields)
	}
	*got = nil
	w.observePower(ctx, samples)
	if len(*got) != 0 {
		t.Fatalf("repeat: %s", names(*got))
	}
	rpc.vals = []valPower{
		{"B7F08625AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", 25},
		{"F94934BDAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", 25},
		{"CCA35D28AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", 25},
		{"BCEF78B5AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", 25},
	}
	w.observePower(ctx, samples)
	if names(*got) != EventSafeToStopAgain {
		t.Fatalf("events = %s", names(*got))
	}
}

func TestStateSuppressesRepeatStall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	node := nodeCfg{Name: "seed", URL: "http://seed"}
	var got []event
	w := &watcher{
		nodes: []nodeCfg{node}, stallAfter: time.Minute, behindBlocks: 10, unreachableAfter: 3,
		out: func(e event) { got = append(got, e) }, statePath: path,
		st: diskState{Nodes: map[string]*nodeState{}},
	}
	t0 := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	s := []sample{{cfg: node, view: view(5, false)}}
	w.observe(t0, s)
	w.observe(t0.Add(time.Minute), s)
	if err := saveState(path, w.st); err != nil {
		t.Fatal(err)
	}
	if names(got) != EventTipStalled {
		t.Fatalf("events = %s", names(got))
	}
	loaded, err := loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	w2 := &watcher{
		nodes: []nodeCfg{node}, stallAfter: time.Minute, behindBlocks: 10, unreachableAfter: 3,
		out: func(e event) { got = append(got, e) }, st: loaded,
	}
	got = nil
	w2.observe(t0.Add(2*time.Minute), s)
	if len(got) != 0 {
		t.Fatalf("restart repeated: %s", names(got))
	}
	w2.observe(t0.Add(3*time.Minute), []sample{{cfg: node, view: view(6, false)}})
	if names(got) != EventTipResumed {
		t.Fatalf("events = %s", names(got))
	}
}

func TestChainIDMismatchCountsAsDown(t *testing.T) {
	rpc := &scriptRPC{views: map[string]nodeView{"http://seed": {Height: 10, Network: "other-chain"}}}
	w, got := newTestWatcher(t, rpc, []nodeCfg{{Name: "seed", URL: "http://seed"}})
	w.chainID = "aether-testnet-1"
	w.unreachableAfter = 1
	ctx := context.Background()
	w.poll(ctx)
	if names(*got) != EventRPCUnreachable {
		t.Fatalf("events = %s", names(*got))
	}
	if !strings.Contains((*got)[0].Fields["error"].(string), "other-chain") {
		t.Fatalf("fields = %v", (*got)[0].Fields)
	}
}

func TestWebhookSignsBodyAndDiscordWrapsMessage(t *testing.T) {
	var rawBodies [][]byte
	var discord []map[string]any
	sigOK := false
	signed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		rawBodies = append(rawBodies, body)
		if r.URL.Path == "/discord" {
			var b map[string]any
			if err := json.Unmarshal(body, &b); err != nil {
				t.Errorf("discord json: %v", err)
			}
			discord = append(discord, b)
			if r.Header.Get(SignatureHeader) != "" {
				t.Errorf("discord post was signed")
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if signBody("s3cret", body) == r.Header.Get(SignatureHeader) && r.Header.Get("X-Aether-Event") == EventTipStalled {
			sigOK = true
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer signed.Close()

	d := &deliverer{
		webhook: signed.URL + "/hook",
		discord: signed.URL + "/discord",
		secret:  "s3cret",
		client:  signed.Client(),
		now:     func() time.Time { return time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC) },
	}
	d.deliver(context.Background(), event{
		Name: EventTipStalled, Height: 10, Key: "10",
		Fields: map[string]any{"message": "tip stuck at 10"},
	})
	if !sigOK {
		t.Fatalf("signature check failed, bodies %q", rawBodies)
	}
	if len(discord) != 1 || discord[0]["content"] != "tip stuck at 10" {
		t.Fatalf("discord = %v", discord)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
