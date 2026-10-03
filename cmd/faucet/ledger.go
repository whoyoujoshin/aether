package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"sort"
	"sync"
	"time"
)

// Where a drip's request came from. Part of /drips' and /stats'
// interface, so never renamed.
const (
	sourceWeb   = "web"   // a browser that solved a /challenge proof of work
	sourceAgent = "agent" // a request signed with a registered agent key
	sourceAPI   = "api"   // neither: a keyless call, held to the per-IP limit
)

const strandAether = "aether"

// drip is one address funded by one confirmed transaction.
type drip struct {
	Time        time.Time `json:"time"`
	TxHash      string    `json:"tx_hash"`
	Address     string    `json:"address"`
	AmountUaeth int64     `json:"amount_uaeth"`
	Strand      string    `json:"strand"`
	Source      string    `json:"source"`
	AgentID     string    `json:"agent_id,omitempty"`
	AgentName   string    `json:"agent_name,omitempty"`
	// NewWallet: the chain had no account at Address before this drip,
	// so the drip is the first transaction that address ever received.
	NewWallet bool `json:"new_wallet"`
}

// ledger is every confirmed drip, in memory for /stats and /drips and
// appended to a JSON-lines file so a restart keeps the history. A
// testnet faucet makes at most a few thousand drips a day, so keeping
// them all in memory is cheap.
type ledger struct {
	mu    sync.Mutex
	path  string // "" keeps the history in memory only
	drips []drip
}

// openLedger loads path's history, if any. Lines it can't parse (a
// torn last write, say) are skipped, not fatal.
func openLedger(path string) (*ledger, error) {
	l := &ledger{path: path}
	if path == "" {
		return l, nil
	}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var d drip
		if json.Unmarshal(sc.Bytes(), &d) == nil && d.Address != "" {
			l.drips = append(l.drips, d)
		}
	}
	return l, sc.Err()
}

// add records drips, file first, so memory never shows a drip the file
// lost.
func (l *ledger) add(ds []drip) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.path != "" {
		f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(f)
		for _, d := range ds {
			if err := enc.Encode(d); err != nil {
				f.Close()
				return err
			}
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	l.drips = append(l.drips, ds...)
	return nil
}

// recent is the newest n drips, newest first.
func (l *ledger) recent(n int) []drip {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n > len(l.drips) {
		n = len(l.drips)
	}
	out := make([]drip, 0, n)
	for i := len(l.drips) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, l.drips[i])
	}
	return out
}

type sourceCounts struct {
	Agent int `json:"agent"`
	Web   int `json:"web"`
	API   int `json:"api"`
}

func (c *sourceCounts) add(source string) {
	switch source {
	case sourceAgent:
		c.Agent++
	case sourceWeb:
		c.Web++
	default:
		c.API++
	}
}

type dayCounts struct {
	Date string `json:"date"` // YYYY-MM-DD, UTC
	sourceCounts
}

type agentCount struct {
	AgentID    string `json:"agent_id"`
	Name       string `json:"name"`
	NewWallets int    `json:"new_wallets"`
	Drips      int    `json:"drips"`
}

type ledgerStats struct {
	Drips           int       `json:"drips"`
	SentUaeth       int64     `json:"sent_uaeth"`
	UniqueWallets   int       `json:"unique_wallets"`
	NewWallets      int       `json:"new_wallets"`
	NewWalletsAgent int       `json:"new_wallets_by_agents"`
	Since           time.Time `json:"since,omitzero"`
	// Who asked over the last 30 days, counted in drips.
	Sources30d sourceCounts `json:"sources_30d"`
	// New wallets per UTC day, oldest first, ending today.
	NewWalletsDaily []dayCounts  `json:"new_wallets_daily"`
	TopAgents30d    []agentCount `json:"top_agents_30d"`
}

const (
	statsWindow = 30 * 24 * time.Hour
	statsDays   = 14
	topAgents   = 5
)

func (l *ledger) stats(now time.Time) ledgerStats {
	l.mu.Lock()
	defer l.mu.Unlock()
	now = now.UTC()
	var s ledgerStats
	wallets := map[string]bool{}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	first := today.AddDate(0, 0, -(statsDays - 1))
	s.NewWalletsDaily = make([]dayCounts, statsDays)
	for i := range s.NewWalletsDaily {
		s.NewWalletsDaily[i].Date = first.AddDate(0, 0, i).Format("2006-01-02")
	}
	agents := map[string]*agentCount{}
	for _, d := range l.drips {
		s.Drips++
		s.SentUaeth += d.AmountUaeth
		wallets[d.Address] = true
		if s.Since.IsZero() || d.Time.Before(s.Since) {
			s.Since = d.Time
		}
		if d.NewWallet {
			s.NewWallets++
			if d.Source == sourceAgent {
				s.NewWalletsAgent++
			}
			if day := d.Time.UTC(); !day.Before(first) {
				if i := int(day.Sub(first) / (24 * time.Hour)); i < statsDays {
					s.NewWalletsDaily[i].add(d.Source)
				}
			}
		}
		if now.Sub(d.Time) > statsWindow {
			continue
		}
		s.Sources30d.add(d.Source)
		if d.Source == sourceAgent && d.AgentID != "" {
			a := agents[d.AgentID]
			if a == nil {
				a = &agentCount{AgentID: d.AgentID, Name: d.AgentName}
				agents[d.AgentID] = a
			}
			a.Drips++
			if d.NewWallet {
				a.NewWallets++
			}
		}
	}
	s.UniqueWallets = len(wallets)
	for _, a := range agents {
		s.TopAgents30d = append(s.TopAgents30d, *a)
	}
	sort.Slice(s.TopAgents30d, func(i, j int) bool {
		a, b := s.TopAgents30d[i], s.TopAgents30d[j]
		if a.NewWallets != b.NewWallets {
			return a.NewWallets > b.NewWallets
		}
		if a.Drips != b.Drips {
			return a.Drips > b.Drips
		}
		return a.AgentID < b.AgentID
	})
	if len(s.TopAgents30d) > topAgents {
		s.TopAgents30d = s.TopAgents30d[:topAgents]
	}
	return s
}
