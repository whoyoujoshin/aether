// cmd/minerwatch/main.go
//
// Pushes what happens to a miner or validator as it happens, so an
// agent (or its operator) doesn't poll /api/miner or scrape powminer's
// logs: a PoW submission lands in a block (or fails there), the address
// is picked into the validator set or dropped from it (and why), an
// epoch is about to close without it on track, its balance runs low, or
// the node stops answering or its chain stops making blocks.
//
// Each event is printed to stdout as one JSON object per line and, with
// --webhook, POSTed there, signed like agentmcp's owner alerts:
// X-Aether-Signature is hex HMAC-SHA256(secret, body).
//
//	go run ./cmd/minerwatch --address aether1... --webhook https://example.com/hook --low-balance "5 AETH"
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/wallet"
)

func init() {
	app.SetAddressPrefixes()
}

const submissionsPerPoll = 25

// source is the chain as the watcher sees it; a fake in tests.
type source interface {
	LatestHeight(ctx context.Context) (int64, error)
	Snapshot(ctx context.Context, address string) (snapshot, error)
}

type chainSource struct{ c *wallet.Client }

func (s chainSource) LatestHeight(context.Context) (int64, error) { return s.c.GetLatestHeight() }

func (s chainSource) Snapshot(ctx context.Context, address string) (snapshot, error) {
	st, err := s.c.MinerStatus(ctx, address)
	if err != nil {
		return snapshot{}, err
	}
	bal, err := s.c.GetBalance(address)
	if err != nil {
		return snapshot{}, fmt.Errorf("balance of %s: %w", address, err)
	}
	subs, err := s.c.PoWSubmissions(ctx, address, submissionsPerPoll)
	if err != nil {
		return snapshot{}, err
	}
	return snapshot{status: st, balance: bal.AmountOf("uaeth"), subs: subs}, nil
}

// stateFile is saved after every poll with --state, so a restart
// reports what happened while it was down instead of starting over.
type stateFile struct {
	Addresses map[string]*addrState `json:"addresses"`
}

type watcher struct {
	cfg              config
	addresses        []string
	src              source
	out              *deliverer
	endpoint         string
	statePath        string
	state            stateFile
	unreachableAfter int
	stallAfter       time.Duration // 0: no stall alerts
	now              func() time.Time

	lastHeight  int64
	failures    int
	unreachable bool

	tipHeight int64 // the node's latest height, and since when
	tipSince  time.Time
	stalled   bool
}

// checkStall notices a node that answers but whose chain stopped: no
// new block for stallAfter.
func (w *watcher) checkStall(ctx context.Context, h int64) {
	now := w.now()
	if h != w.tipHeight {
		if w.stalled {
			w.out.deliver(ctx, Event{Name: EventChainResumed, Height: h, key: fmt.Sprint(h),
				Fields: map[string]any{"node": w.endpoint, "stalledAt": w.tipHeight, "stalledSeconds": int64(now.Sub(w.tipSince).Seconds())}})
		}
		w.tipHeight, w.tipSince, w.stalled = h, now, false
		return
	}
	if w.stallAfter > 0 && !w.stalled && now.Sub(w.tipSince) >= w.stallAfter {
		w.stalled = true
		w.out.deliver(ctx, Event{Name: EventChainStalled, Height: h, key: fmt.Sprint(h),
			Fields: map[string]any{"node": w.endpoint, "noNewBlockSeconds": int64(now.Sub(w.tipSince).Seconds())}})
	}
}

func (w *watcher) poll(ctx context.Context) {
	h, err := w.src.LatestHeight(ctx)
	if err == nil {
		w.checkStall(ctx, h)
	}
	if err == nil && h == w.lastHeight {
		return // no new block, nothing can have changed
	}
	snaps := make([]snapshot, len(w.addresses))
	for i, a := range w.addresses {
		if err != nil {
			break
		}
		snaps[i], err = w.src.Snapshot(ctx, a)
	}
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		w.failures++
		log.Printf("poll %s: %v", w.endpoint, err)
		if w.failures >= w.unreachableAfter && !w.unreachable {
			w.unreachable = true
			w.out.deliver(ctx, Event{Name: EventNodeUnreachable, Height: w.lastHeight, key: fmt.Sprint(w.lastHeight),
				Fields: map[string]any{"node": w.endpoint, "error": err.Error(), "consecutiveFailures": w.failures}})
		}
		return
	}
	if w.unreachable {
		w.out.deliver(ctx, Event{Name: EventNodeRecovered, Height: h, key: fmt.Sprint(h),
			Fields: map[string]any{"node": w.endpoint, "failedPolls": w.failures}})
	}
	w.failures, w.unreachable = 0, false

	for i, a := range w.addresses {
		st := w.state.Addresses[a]
		if st == nil {
			st = &addrState{}
			w.state.Addresses[a] = st
		}
		for _, e := range step(w.cfg, a, st, snaps[i]) {
			w.out.deliver(ctx, e)
		}
	}
	w.lastHeight = h
	if w.statePath != "" {
		if err := saveState(w.statePath, w.state); err != nil {
			log.Printf("save state: %v", err)
		}
	}
}

func loadState(path string) (stateFile, error) {
	st := stateFile{Addresses: map[string]*addrState{}}
	if path == "" {
		return st, nil
	}
	bz, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(bz, &st); err != nil {
		return st, fmt.Errorf("%s: %w", path, err)
	}
	if st.Addresses == nil {
		st.Addresses = map[string]*addrState{}
	}
	return st, nil
}

func saveState(path string, st stateFile) error {
	bz, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".minerwatch-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(bz); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func parseAddresses(list string, args []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, a := range append(strings.Split(list, ","), args...) {
		a = strings.TrimSpace(a)
		if a == "" || seen[a] {
			continue
		}
		if _, err := sdk.AccAddressFromBech32(a); err != nil {
			return nil, fmt.Errorf("invalid address %q: %w", a, err)
		}
		seen[a] = true
		out = append(out, a)
	}
	if len(out) == 0 {
		return nil, errors.New("no address to watch: pass --address aether1... (comma-separated for several)")
	}
	return out, nil
}

func main() {
	addressList := flag.String("address", "", "address(es) to watch, comma-separated (or pass them as arguments)")
	grpcEndpoint := flag.String("grpc", "grpc.157-245-252-221.sslip.io:443", "node gRPC endpoint (defaults to the public testnet, over TLS)")
	webhook := flag.String("webhook", "", "POST each event here as JSON; empty: stdout only")
	secret := flag.String("secret", "", "sign webhook bodies with this HMAC key ("+SignatureHeader+"); defaults to $MINERWATCH_SECRET")
	interval := flag.Duration("interval", 30*time.Second, "how often to check for a new block")
	lowBalance := flag.String("low-balance", "", `alert when an address's balance falls below this, e.g. "5 AETH"; empty: no balance alerts`)
	atRiskBlocks := flag.Int64("at-risk-blocks", 20, "warn this many blocks before the validator set is picked if a registered miner isn't on track; 0: never")
	unreachableAfter := flag.Int("unreachable-after", 3, "failed polls in a row before node_unreachable")
	stallAfter := flag.Duration("stall-after", 5*time.Minute, "chain_stalled when the node answers but has made no new block for this long; 0: never")
	statePath := flag.String("state", "", "remember what was reported in this file, so a restart catches up instead of starting fresh")
	once := flag.Bool("once", false, "poll once and exit (with --state, suits a cron job)")
	quiet := flag.Bool("quiet", false, "don't print events to stdout")
	flag.Parse()

	addresses, err := parseAddresses(*addressList, flag.Args())
	if err != nil {
		log.Fatal(err)
	}
	cfg := config{lowBalance: math.ZeroInt(), atRiskBlocks: *atRiskBlocks}
	if *lowBalance != "" {
		if cfg.lowBalance, err = wallet.ParseAmount(*lowBalance); err != nil {
			log.Fatalf("--low-balance: %v", err)
		}
	}
	if *secret == "" {
		*secret = os.Getenv("MINERWATCH_SECRET")
	}
	if *unreachableAfter < 1 {
		*unreachableAfter = 1
	}
	state, err := loadState(*statePath)
	if err != nil {
		log.Fatalf("--state: %v", err)
	}
	client, err := wallet.NewClient(*grpcEndpoint)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	out := &deliverer{
		webhook: *webhook,
		secret:  *secret,
		client:  &http.Client{},
		backoff: []time.Duration{2 * time.Second, 10 * time.Second, 30 * time.Second},
		now:     time.Now,
	}
	if !*quiet {
		out.stdout = os.Stdout
	}
	w := &watcher{
		cfg:              cfg,
		addresses:        addresses,
		src:              chainSource{client},
		out:              out,
		endpoint:         *grpcEndpoint,
		statePath:        *statePath,
		state:            state,
		unreachableAfter: *unreachableAfter,
		stallAfter:       *stallAfter,
		now:              time.Now,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("watching %s via %s every %s", strings.Join(addresses, ", "), *grpcEndpoint, *interval)
	for {
		w.poll(ctx)
		if *once {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(*interval):
		}
	}
}
