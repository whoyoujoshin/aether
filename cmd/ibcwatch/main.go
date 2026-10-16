// cmd/ibcwatch posts to Discord when an IBC light client is close to
// expiring, has expired, or can't be read, and again when it's fine.
//
// Each path has a client on both ends: Aether's client of the other chain
// (kept fresh by Helicase) and the other chain's client of Aether (kept
// fresh by cmd/outbound). An expired client can't be updated, and the
// path stops until it is revived by governance or replaced, so the
// warning has to come while there's time to act. Both kinds of expiry
// happened in October 2026 with nobody told.
//
//	IBCWATCH_DISCORD_WEBHOOK=https://discord.com/api/webhooks/... ibcwatch \
//	  --watch "Aether's client of Injective,http://127.0.0.1:26657,07-tendermint-2" \
//	  --watch "Injective's client of Aether,https://injective-testnet-rpc.polkachu.com:443,07-tendermint-510"
//
// It reads each client every --interval over the chain's RPC, gently: a
// public RPC rate-limits a client that polls every few seconds.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/relayer"
)

// target is one client to watch: where it lives and its ID there.
type target struct {
	Label    string
	RPC      string
	ClientID string
}

type targets []target

func (t *targets) String() string { return fmt.Sprint(*t) }

func (t *targets) Set(v string) error {
	parts := strings.Split(v, ",")
	if len(parts) != 3 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" || strings.TrimSpace(parts[2]) == "" {
		return fmt.Errorf("want <label>,<rpc>,<client-id>, got %q", v)
	}
	*t = append(*t, target{strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2])})
	return nil
}

func main() {
	var watch targets
	flag.Var(&watch, "watch", "a client to watch, as <label>,<rpc>,<client-id> (repeat for each)")
	webhook := flag.String("discord-webhook", "", "Discord webhook URL; defaults to $IBCWATCH_DISCORD_WEBHOOK (keep it out of unit files: anyone with it can post); empty: log only")
	interval := flag.Duration("interval", 10*time.Minute, "how often to read the clients")
	warnBefore := flag.Duration("warn-before", 24*time.Hour, "warn when a client expires sooner than this")
	unreachableAfter := flag.Int("unreachable-after", 3, "failed reads in a row before a client is reported unreadable")
	once := flag.Bool("once", false, "check once, print every client's state and exit, posting nothing")
	flag.Parse()
	if len(watch) == 0 {
		log.Fatal("nothing to watch: pass --watch <label>,<rpc>,<client-id>")
	}
	if *webhook == "" {
		*webhook = os.Getenv("IBCWATCH_DISCORD_WEBHOOK")
	}

	enc := app.MakeEncodingConfig()
	chains := map[string]*relayer.Chain{}
	check := func(t target) (time.Time, error) {
		c, ok := chains[t.RPC]
		if !ok {
			var err error
			if c, err = relayer.NewReadOnlyChain(t.Label, t.RPC, "", enc.Codec, enc.TxConfig); err != nil {
				return time.Time{}, err
			}
			chains[t.RPC] = c
		}
		return relayer.ClientExpiry(c, t.ClientID)
	}
	post := func(text string) error {
		log.Print(text)
		if *webhook == "" {
			return nil
		}
		return postDiscord(http.DefaultClient, *webhook, text)
	}

	if *once {
		// A check by hand never posts, whatever the environment holds.
		w := newWatcher(watch, check, func(text string) error { log.Print(text); return nil }, *warnBefore, *unreachableAfter)
		w.round()
		fmt.Println(w.summary())
		return
	}
	w := newWatcher(watch, check, post, *warnBefore, *unreachableAfter)
	w.round()
	if err := post("ibcwatch is watching " + w.summary()); err != nil {
		log.Printf("discord: %v", err)
	}
	for {
		time.Sleep(*interval)
		w.round()
	}
}

type level int

const (
	unknown level = iota
	fine
	expiring
	expired
	unreadable
)

type state struct {
	level   level
	expiry  time.Time
	fails   int
	lastErr string
}

type watcher struct {
	targets          []target
	check            func(target) (time.Time, error)
	post             func(string) error
	warnBefore       time.Duration
	unreachableAfter int
	now              func() time.Time
	states           []state
}

func newWatcher(t []target, check func(target) (time.Time, error), post func(string) error, warnBefore time.Duration, unreachableAfter int) *watcher {
	return &watcher{targets: t, check: check, post: post, warnBefore: warnBefore,
		unreachableAfter: max(unreachableAfter, 1), now: time.Now, states: make([]state, len(t))}
}

// round reads every client and posts each one whose level changed. The
// first round posts only what isn't fine; the summary covers the rest.
func (w *watcher) round() {
	now := w.now()
	for i, t := range w.targets {
		s := &w.states[i]
		before := s.level
		expiry, err := w.check(t)
		switch {
		case err != nil:
			s.fails++
			s.lastErr = err.Error()
			if s.fails >= w.unreachableAfter {
				s.level = unreadable
			}
		case !expiry.After(now):
			s.fails, s.expiry, s.level = 0, expiry, expired
		case expiry.Sub(now) < w.warnBefore:
			s.fails, s.expiry, s.level = 0, expiry, expiring
		default:
			s.fails, s.expiry, s.level = 0, expiry, fine
		}
		if s.level == before || (before == unknown && s.level == fine) {
			continue
		}
		if err := w.post(w.message(t, *s, before, now)); err != nil {
			log.Printf("discord: %v", err)
		}
	}
}

func (w *watcher) message(t target, s state, before level, now time.Time) string {
	name := fmt.Sprintf("**%s** (`%s` on %s)", t.Label, t.ClientID, t.RPC)
	switch s.level {
	case expiring:
		return fmt.Sprintf("⚠️ %s expires in %s (%s). Its relayer must update it before then, or the path stops.", name, until(s.expiry.Sub(now)), s.expiry.UTC().Format("2006-01-02 15:04 UTC"))
	case expired:
		return fmt.Sprintf("🔴 %s expired at %s. An expired client can't be updated: transfers on this path stop until it is revived or replaced.", name, s.expiry.UTC().Format("2006-01-02 15:04 UTC"))
	case unreadable:
		return fmt.Sprintf("❔ Can't read %s (%d tries in a row): %s", name, s.fails, truncate(s.lastErr, 300))
	default:
		if before == unreadable {
			return fmt.Sprintf("✅ %s can be read again; it expires in %s.", name, until(s.expiry.Sub(now)))
		}
		return fmt.Sprintf("✅ %s is fine again; it expires in %s.", name, until(s.expiry.Sub(now)))
	}
}

// summary is one line per client, for the startup post and --once.
func (w *watcher) summary() string {
	now := w.now()
	lines := []string{fmt.Sprintf("%d IBC clients:", len(w.targets))}
	for i, t := range w.targets {
		s := w.states[i]
		var what string
		switch {
		case s.level == unreadable || (s.level == unknown && s.lastErr != ""):
			what = "can't be read: " + truncate(s.lastErr, 120)
		case s.level == expired:
			what = "EXPIRED at " + s.expiry.UTC().Format("2006-01-02 15:04 UTC")
		default:
			what = "expires in " + until(s.expiry.Sub(now))
		}
		lines = append(lines, fmt.Sprintf("- %s (`%s`): %s", t.Label, t.ClientID, what))
	}
	return strings.Join(lines, "\n")
}

func until(d time.Duration) string {
	switch {
	case d >= 72*time.Hour:
		return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dm", max(int(d.Minutes()), 0))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// postDiscord sends text to a Discord webhook, mentioning nobody. It
// retries a rate limit (429) and a server error once each.
func postDiscord(c *http.Client, url, text string) error {
	body, _ := json.Marshal(map[string]any{
		"content":          truncate(text, 1990),
		"allowed_mentions": map[string]any{"parse": []string{}},
	})
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		resp, err := c.Post(url, "application/json", bytes.NewReader(body))
		if err != nil {
			last = err
		} else {
			resp.Body.Close()
			if resp.StatusCode < 300 {
				return nil
			}
			last = fmt.Errorf("HTTP %d", resp.StatusCode)
			if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
				return last
			}
		}
		time.Sleep(time.Duration(attempt+1) * 5 * time.Second)
	}
	return last
}
