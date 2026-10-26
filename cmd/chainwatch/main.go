// cmd/chainwatch watches CometBFT RPC endpoints and pushes height and
// halt alerts: the chain tip stopped advancing, a node fell behind or
// reports catching_up, an RPC stopped answering, or a validator now
// holds so much voting power that stopping it would drop the others to
// 2/3 or less.
//
// It is the sibling of cmd/minerwatch (submissions, seats, signing_low,
// power_concentrated) and is meant to run beside it on the seed. Alerts
// go to stdout as one JSON object per line and, when configured, to a
// JSON webhook and a Discord webhook. Webhook URLs and the HMAC secret
// come from flags or the environment, never from this source tree.
//
//	chainwatch --node seed=http://127.0.0.1:26657 --node sync3=http://sync3:26657 \
//	  --stall-after 3m --state /root/chainwatch.state
//	chainwatch --rpc http://127.0.0.1:26657 --safe-to-stop
//	chainwatch --rpc http://127.0.0.1:26657 --safe-to-stop --validator B7F08625
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	var nodes nodeList
	flag.Var(&nodes, "node", "node to watch, as name=http://host:26657 (repeat for each); the name is what alerts call it")
	rpcFlag := flag.String("rpc", "", "single RPC base URL; with --safe-to-stop this is the node to ask, and otherwise it is watched as the name \"rpc\"")
	tipFrom := flag.String("tip-from", "", "name of the node whose height is the network tip when it is synced; empty: the highest height among synced nodes")
	chainID := flag.String("chain-id", "", "require this CometBFT network in /status (empty: accept any)")
	interval := flag.Duration("interval", 30*time.Second, "how often to poll every node")
	stallAfter := flag.Duration("stall-after", 3*time.Minute, "tip_stalled when the tip does not advance for this long, or no node is synced for this long; 0: never")
	behindBlocks := flag.Int64("behind-blocks", 10, "node_behind when a synced node is more than this many blocks behind the tip; -1: only catching_up counts")
	unreachableAfter := flag.Int("unreachable-after", 3, "failed polls in a row before rpc_unreachable")
	unsafeAlerts := flag.Bool("unsafe-stop-alerts", true, "alert when a validator is unsafe to stop, and again when it becomes safe")
	webhook := flag.String("webhook", "", "POST each event here as JSON; empty: $CHAINWATCH_WEBHOOK, or stdout only")
	discord := flag.String("discord-webhook", "", "Discord webhook URL; empty: $CHAINWATCH_DISCORD_WEBHOOK; the URL is a secret")
	secret := flag.String("secret", "", "sign JSON webhook bodies with this HMAC key ("+SignatureHeader+"); empty: $CHAINWATCH_SECRET")
	statePath := flag.String("state", "", "remember what was reported in this file, so a restart does not repeat an alert that is still true")
	once := flag.Bool("once", false, "poll once and exit")
	quiet := flag.Bool("quiet", false, "do not print events to stdout")
	safeToStop := flag.Bool("safe-to-stop", false, "print whether each validator can be stopped without dropping the others to 2/3 or less, then exit")
	validator := flag.String("validator", "", "with --safe-to-stop, exit 0 only when this consensus address (or a unique 8+ hex prefix) is safe to stop")
	asJSON := flag.Bool("json", false, "with --safe-to-stop, print the report as JSON")
	flag.Parse()

	if *webhook == "" {
		*webhook = os.Getenv("CHAINWATCH_WEBHOOK")
	}
	if *discord == "" {
		*discord = os.Getenv("CHAINWATCH_DISCORD_WEBHOOK")
	}
	if *secret == "" {
		*secret = os.Getenv("CHAINWATCH_SECRET")
	}
	if *rpcFlag != "" {
		if err := nodes.Set("rpc=" + *rpcFlag); err != nil {
			log.Fatal(err)
		}
	}
	if len(nodes) == 0 {
		log.Fatal("nothing to watch: pass --node name=http://host:26657 or --rpc http://host:26657")
	}
	seen := map[string]bool{}
	for _, n := range nodes {
		if seen[n.Name] {
			log.Fatalf("duplicate node name %q", n.Name)
		}
		seen[n.Name] = true
	}
	if *tipFrom != "" && !seen[*tipFrom] {
		log.Fatalf("--tip-from %q is not one of the node names", *tipFrom)
	}
	if *unreachableAfter < 1 {
		*unreachableAfter = 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	rpc := newHTTPChecker()

	if *safeToStop {
		os.Exit(runSafeToStop(ctx, rpc, nodes, *validator, *asJSON))
	}

	st, err := loadState(*statePath)
	if err != nil {
		log.Fatalf("--state: %v", err)
	}
	del := &deliverer{
		webhook: *webhook,
		discord: *discord,
		secret:  *secret,
		client:  &http.Client{},
		backoff: []time.Duration{2 * time.Second, 10 * time.Second, 30 * time.Second},
		now:     time.Now,
	}
	if !*quiet {
		del.stdout = os.Stdout
	}
	if *webhook != "" && *secret == "" {
		log.Printf("webhook is set and CHAINWATCH_SECRET is empty: bodies are not signed")
	}
	w := &watcher{
		nodes: nodes, tipFrom: *tipFrom, chainID: *chainID,
		stallAfter: *stallAfter, behindBlocks: *behindBlocks,
		unreachableAfter: *unreachableAfter, unsafeAlerts: *unsafeAlerts,
		now: time.Now, rpc: rpc, out: func(e event) { del.deliver(ctx, e) },
		statePath: *statePath, st: st,
	}
	names := make([]string, len(nodes))
	for i, n := range nodes {
		names[i] = n.Name
	}
	log.Printf("watching %s every %s", strings.Join(names, ", "), *interval)
	for {
		w.poll(ctx)
		if *once || ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(*interval):
		}
	}
}

func runSafeToStop(ctx context.Context, rpc checker, nodes []nodeCfg, validator string, asJSON bool) int {
	var last error
	for _, n := range nodes {
		vals, height, err := rpc.Validators(ctx, n.URL)
		if err != nil {
			last = fmt.Errorf("%s (%s): %w", n.Name, n.URL, err)
			log.Print(last)
			continue
		}
		st, err := rpc.Status(ctx, n.URL)
		chainID := ""
		if err == nil {
			if height == 0 {
				height = st.Height
			}
			chainID = st.Network
		}
		report, err := BuildReport(vals, height, chainID, n.Name+" "+n.URL)
		if err != nil {
			log.Printf("%s: %v", n.Name, err)
			return 1
		}
		if validator != "" {
			choice, err := report.Find(validator)
			if err != nil {
				log.Print(err)
				return 1
			}
			// Still print the whole set so the operator sees the others.
			writeReport(report, asJSON)
			if choice.SafeToStop {
				return 0
			}
			return 2
		}
		writeReport(report, asJSON)
		if report.SafeToStopAnyOne {
			return 0
		}
		return 2
	}
	if last != nil {
		log.Printf("no RPC answered: %v", last)
	}
	return 1
}

func writeReport(r StopReport, asJSON bool) {
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(r); err != nil {
			log.Print(err)
		}
		return
	}
	fmt.Fprint(os.Stdout, FormatReport(r))
}

// nodeList is the repeatable --node flag.
type nodeList []nodeCfg

func (n *nodeList) String() string { return fmt.Sprint(*n) }

func (n *nodeList) Set(v string) error {
	name, raw, ok := strings.Cut(v, "=")
	name, raw = strings.TrimSpace(name), strings.TrimSpace(raw)
	if !ok || name == "" || raw == "" {
		return fmt.Errorf("--node wants name=http://host:26657, got %q", v)
	}
	if !strings.Contains(raw, "://") {
		return fmt.Errorf("--node %s: URL %q needs a scheme", name, raw)
	}
	*n = append(*n, nodeCfg{Name: name, URL: strings.TrimRight(raw, "/")})
	return nil
}
