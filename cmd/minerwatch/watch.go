package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"cosmossdk.io/math"

	"github.com/whoyoujoshin/aether/wallet"
)

// Event names are part of this program's interface: receivers switch
// on them, so they're never renamed.
const (
	EventSubmissionConfirmed = "pow_submission_confirmed"
	EventSubmissionFailed    = "pow_submission_failed"
	EventValidatorSelected   = "validator_selected"
	EventValidatorRemoved    = "validator_removed"
	EventMinerBanned         = "miner_banned"
	EventSelectionAtRisk     = "selection_at_risk"
	EventBalanceLow          = "balance_low"
	EventBalanceRecovered    = "balance_recovered"
	EventNodeUnreachable     = "node_unreachable"
	EventNodeRecovered       = "node_recovered"
	EventChainStalled        = "chain_stalled"
	EventChainResumed        = "chain_resumed"
)

// Why validator_removed fired.
const (
	RemovedNotReselected = "not_reselected" // the epoch ended and it wasn't picked again
	RemovedDowntime      = "downtime"       // dropped mid-epoch for missing too many blocks
	RemovedBanned        = "banned"         // equivocation: banned for good
)

// Event is one thing that happened to a watched address. Fields are
// flattened into the JSON body next to the common ones.
type Event struct {
	Name    string
	Address string
	Height  int64
	Fields  map[string]any
	// key makes ID unique per occurrence (a tx hash, an epoch...).
	key string
}

// ID is stable across restarts, so a receiver can drop a repeat.
func (e Event) ID() string {
	h := sha256.Sum256([]byte(e.Name + "|" + e.Address + "|" + e.key))
	return hex.EncodeToString(h[:12])
}

func (e Event) Body(now string) map[string]any {
	b := map[string]any{"id": e.ID(), "event": e.Name, "time": now}
	if e.Address != "" {
		b["address"] = e.Address
	}
	if e.Height > 0 {
		b["height"] = e.Height
	}
	for k, v := range e.Fields {
		b[k] = v
	}
	return b
}

type config struct {
	lowBalance   math.Int // uaeth; zero: no balance alerts
	atRiskBlocks int64    // warn this many blocks before selection; 0: never
}

// snapshot is everything one poll learns about an address.
type snapshot struct {
	status  *wallet.MinerStatus
	balance math.Int // uaeth
	subs    []wallet.PoWSubmission
}

// addrState is what the last poll saw, kept (and saved with --state)
// so the next one reports only what changed.
type addrState struct {
	Initialized     bool     `json:"initialized"`
	Height          int64    `json:"height"`
	SelectionHeight int64    `json:"selectionHeight"`
	Active          bool     `json:"active"`
	Banned          bool     `json:"banned"`
	LowBalance      bool     `json:"lowBalance"`
	AtRiskEpoch     int64    `json:"atRiskEpoch"`
	SubHeight       int64    `json:"subHeight"`
	SubHashes       []string `json:"subHashes,omitempty"`
}

// step compares snap with what st last saw, returns the events in
// between and updates st. The first snapshot only sets the baseline
// (history from before the watch started isn't replayed), apart from a
// balance that's already low.
func step(cfg config, address string, st *addrState, snap snapshot) []Event {
	s := snap.status
	var out []Event
	ev := func(name, key string, fields map[string]any) {
		out = append(out, Event{Name: name, Address: address, Height: s.Height, Fields: fields, key: key})
	}

	if !st.Initialized {
		*st = addrState{Initialized: true, AtRiskEpoch: -1, Active: s.ActiveValidator, Banned: s.Banned}
		st.SubHeight, st.SubHashes = newestSubmissions(snap.subs)
	} else {
		for _, sub := range newSubmissions(st, snap.subs) {
			f := map[string]any{"txHash": sub.Hash, "submissionHeight": sub.Height}
			name := EventSubmissionConfirmed
			if sub.Code != 0 {
				name = EventSubmissionFailed
				f["code"] = sub.Code
				f["codespace"] = sub.Codespace
				f["rawLog"] = sub.RawLog
			}
			ev(name, sub.Hash, f)
		}
		st.SubHeight, st.SubHashes = advanceSubmissions(st, snap.subs)

		if s.Banned && !st.Banned {
			ev(EventMinerBanned, fmt.Sprint(s.Height), nil)
		}
		// The set changes in an epoch's last block (its selection
		// height), for the next epoch; CometBFT applies it two blocks on.
		lastSelection, serving := s.Epoch.StartHeight-1, s.Epoch.Index
		if s.Height == s.Epoch.SelectionHeight {
			lastSelection, serving = s.Height, s.Epoch.Index+1
		}
		switch {
		case s.ActiveValidator && !st.Active:
			ev(EventValidatorSelected, fmt.Sprint(serving), map[string]any{
				"epoch":         serving,
				"selectedAt":    lastSelection,
				"effectiveFrom": lastSelection + 2,
				"selectionRule": wallet.SelectionRuleAt(lastSelection),
			})
		case !s.ActiveValidator && st.Active:
			reason := RemovedDowntime
			switch {
			case s.Banned:
				reason = RemovedBanned
			case s.Height >= st.SelectionHeight:
				// An epoch boundary passed since the last poll.
				reason = RemovedNotReselected
			}
			ev(EventValidatorRemoved, fmt.Sprintf("%d/%d", serving, s.Height), map[string]any{
				"reason": reason,
				"epoch":  serving,
			})
		}
	}

	if cfg.atRiskBlocks > 0 && s.RegisteredConsensusKey && !s.Banned &&
		s.Epoch.BlocksUntilSelection <= cfg.atRiskBlocks && st.AtRiskEpoch != s.Epoch.Index {
		atRisk := !s.Eligible || (s.SelectionRule == wallet.SelectionTopKByWork && !s.OnTrack)
		if atRisk {
			f := map[string]any{
				"epoch":                    s.Epoch.Index,
				"selectionHeight":          s.Epoch.SelectionHeight,
				"blocksUntilSelection":     s.Epoch.BlocksUntilSelection,
				"estSecondsUntilSelection": s.Epoch.EstSecondsUntilSelection,
				"selectionRule":            s.SelectionRule,
				"workThisEpoch":            s.WorkThisEpoch,
				"eligible":                 s.Eligible,
				"topKSize":                 s.TopKSize,
			}
			if len(s.NotEligibleBecause) > 0 {
				f["notEligibleBecause"] = s.NotEligibleBecause
			}
			if s.Rank > 0 {
				f["rank"] = s.Rank
			}
			ev(EventSelectionAtRisk, fmt.Sprint(s.Epoch.Index), f)
			st.AtRiskEpoch = s.Epoch.Index
		}
	}

	if cfg.lowBalance.IsPositive() && !snap.balance.IsNil() {
		low := snap.balance.LT(cfg.lowBalance)
		f := map[string]any{
			"balanceUaeth":   snap.balance.String(),
			"balance":        wallet.FormatAeth(snap.balance) + " AETH",
			"thresholdUaeth": cfg.lowBalance.String(),
		}
		switch {
		case low && !st.LowBalance:
			ev(EventBalanceLow, fmt.Sprint(s.Height), f)
		case !low && st.LowBalance:
			ev(EventBalanceRecovered, fmt.Sprint(s.Height), f)
		}
		st.LowBalance = low
	}

	st.Height = s.Height
	st.SelectionHeight = s.Epoch.SelectionHeight
	st.Active = s.ActiveValidator
	st.Banned = s.Banned
	return out
}

// newSubmissions are those in subs not yet reported, oldest first.
func newSubmissions(st *addrState, subs []wallet.PoWSubmission) []wallet.PoWSubmission {
	seen := make(map[string]bool, len(st.SubHashes))
	for _, h := range st.SubHashes {
		seen[h] = true
	}
	var out []wallet.PoWSubmission
	for _, s := range subs {
		if s.Height > st.SubHeight || (s.Height == st.SubHeight && !seen[s.Hash]) {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Height < out[j].Height })
	return out
}

// advanceSubmissions moves the high-water mark past subs.
func advanceSubmissions(st *addrState, subs []wallet.PoWSubmission) (int64, []string) {
	h, hashes := newestSubmissions(subs)
	switch {
	case h > st.SubHeight:
		return h, hashes
	case h == st.SubHeight:
		return h, union(st.SubHashes, hashes)
	}
	return st.SubHeight, st.SubHashes
}

func newestSubmissions(subs []wallet.PoWSubmission) (int64, []string) {
	var h int64
	var hashes []string
	for _, s := range subs {
		switch {
		case s.Height > h:
			h, hashes = s.Height, []string{s.Hash}
		case s.Height == h:
			hashes = append(hashes, s.Hash)
		}
	}
	return h, hashes
}

func union(a, b []string) []string {
	seen := make(map[string]bool, len(a))
	out := append([]string{}, a...)
	for _, x := range a {
		seen[x] = true
	}
	for _, x := range b {
		if !seen[x] {
			out = append(out, x)
		}
	}
	return out
}
