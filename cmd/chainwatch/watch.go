package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

// Event names are part of this program's interface: receivers switch on
// them, so they're never renamed.
const (
	EventTipStalled      = "tip_stalled"
	EventTipResumed      = "tip_resumed"
	EventNodeBehind      = "node_behind"
	EventNodeSynced      = "node_synced"
	EventRPCUnreachable  = "rpc_unreachable"
	EventRPCRecovered    = "rpc_recovered"
	EventUnsafeToStop    = "validator_unsafe_to_stop"
	EventSafeToStopAgain = "validator_safe_to_stop"
)

// event is one alert. Fields are flattened into the JSON body.
type event struct {
	Name   string
	Node   string
	Height int64
	Key    string
	Fields map[string]any
}

func (e event) id() string {
	sum := sha256.Sum256([]byte(e.Name + "|" + e.Node + "|" + e.Key))
	return hex.EncodeToString(sum[:12])
}

func (e event) body(now string) map[string]any {
	b := map[string]any{"id": e.id(), "event": e.Name, "time": now}
	if e.Node != "" {
		b["node"] = e.Node
	}
	if e.Height > 0 {
		b["height"] = e.Height
	}
	for k, v := range e.Fields {
		b[k] = v
	}
	return b
}

type nodeCfg struct {
	Name string
	URL  string
}

type sample struct {
	cfg  nodeCfg
	view nodeView
	err  error
}

type nodeState struct {
	Failures    int    `json:"failures"`
	Unreachable bool   `json:"unreachable"`
	Behind      bool   `json:"behind"`
	LastHeight  int64  `json:"lastHeight,omitempty"`
	LastErr     string `json:"lastErr,omitempty"`
}

// diskState is saved after every poll with --state, so a restart does
// not repeat an alert that is still true.
type diskState struct {
	Nodes        map[string]*nodeState `json:"nodes"`
	TipHeight    int64                 `json:"tipHeight"`
	TipSince     time.Time             `json:"tipSince,omitempty"`
	Stalled      bool                  `json:"stalled"`
	NoTipSince   time.Time             `json:"noTipSince,omitempty"`
	UnsafeToStop map[string]bool       `json:"unsafeToStop,omitempty"`
}

type watcher struct {
	nodes            []nodeCfg
	tipFrom          string
	chainID          string
	stallAfter       time.Duration
	behindBlocks     int64
	unreachableAfter int
	unsafeAlerts     bool
	now              func() time.Time
	rpc              checker
	out              func(event)
	statePath        string
	st               diskState
}

func (w *watcher) poll(ctx context.Context) {
	samples := make([]sample, len(w.nodes))
	for i, n := range w.nodes {
		view, err := w.rpc.Status(ctx, n.URL)
		if err == nil && w.chainID != "" && view.Network != "" && view.Network != w.chainID {
			err = fmt.Errorf("chain id %q, want %q", view.Network, w.chainID)
		}
		samples[i] = sample{cfg: n, view: view, err: err}
		if err != nil && ctx.Err() == nil {
			log.Printf("status %s (%s): %v", n.Name, n.URL, err)
		}
	}
	if ctx.Err() != nil {
		return
	}
	w.observe(w.now(), samples)
	if w.unsafeAlerts {
		w.observePower(ctx, samples)
	}
	if w.statePath != "" {
		if err := saveState(w.statePath, w.st); err != nil {
			log.Printf("save state: %v", err)
		}
	}
}

// observe updates stall and per-node alerts from one round of samples.
func (w *watcher) observe(now time.Time, samples []sample) {
	if w.st.Nodes == nil {
		w.st.Nodes = map[string]*nodeState{}
	}
	tip, tipOK := pickTip(samples, w.tipFrom)
	w.observeStall(now, tip, tipOK, samples)
	for _, s := range samples {
		w.observeNode(s, tip, tipOK)
	}
}

func (w *watcher) emit(e event) {
	if e.Fields == nil {
		e.Fields = map[string]any{}
	}
	w.out(e)
}

func (w *watcher) observeStall(now time.Time, tip int64, tipOK bool, samples []sample) {
	if w.stallAfter <= 0 {
		return
	}
	if !tipOK {
		if w.st.NoTipSince.IsZero() {
			w.st.NoTipSince = now
		}
		if !w.st.Stalled && now.Sub(w.st.NoTipSince) >= w.stallAfter {
			w.st.Stalled = true
			w.emit(event{
				Name: EventTipStalled, Height: w.st.TipHeight, Key: "notip/" + strconv.FormatInt(w.st.TipHeight, 10),
				Fields: map[string]any{
					"reason":            "no_synced_node",
					"noNewBlockSeconds": int64(now.Sub(w.st.NoTipSince).Seconds()),
					"tipHeight":         w.st.TipHeight,
					"nodes":             summarize(samples),
					"message": fmt.Sprintf("No watched node is synced, for %s. The last tip seen was %d. A halt looks like this when every RPC is down or catching up; check the validator set before restarting anyone.",
						now.Sub(w.st.NoTipSince).Round(time.Second), w.st.TipHeight),
				},
			})
		}
		return
	}
	w.st.NoTipSince = time.Time{}
	if tip != w.st.TipHeight {
		prev := w.st.TipHeight
		was := w.st.Stalled
		w.st.TipHeight = tip
		w.st.TipSince = now
		w.st.Stalled = false
		if was {
			w.emit(event{
				Name: EventTipResumed, Height: tip, Key: strconv.FormatInt(tip, 10),
				Fields: map[string]any{
					"stalledAt": prev,
					"nodes":     summarize(samples),
					"message":   fmt.Sprintf("The chain tip moved from %d to %d.", prev, tip),
				},
			})
		}
		return
	}
	if w.st.TipSince.IsZero() {
		w.st.TipSince = now
	}
	if !w.st.Stalled && now.Sub(w.st.TipSince) >= w.stallAfter {
		w.st.Stalled = true
		w.emit(event{
			Name: EventTipStalled, Height: tip, Key: strconv.FormatInt(tip, 10),
			Fields: map[string]any{
				"reason":            "height_unchanged",
				"tipHeight":         tip,
				"noNewBlockSeconds": int64(now.Sub(w.st.TipSince).Seconds()),
				"nodes":             summarize(samples),
				"message": fmt.Sprintf("The chain tip is still %d after %s. Blocks are not advancing. With four validators the chain halts when two are down, or when one that holds a third or more goes down. See who signed the last block before restarting anyone.",
					tip, now.Sub(w.st.TipSince).Round(time.Second)),
			},
		})
	}
}

func (w *watcher) observeNode(s sample, tip int64, tipOK bool) {
	st := w.st.Nodes[s.cfg.Name]
	if st == nil {
		st = &nodeState{}
		w.st.Nodes[s.cfg.Name] = st
	}
	if s.err != nil {
		st.Failures++
		st.LastErr = s.err.Error()
		if st.Failures >= w.unreachableAfter && !st.Unreachable {
			st.Unreachable = true
			st.Behind = false
			w.emit(event{
				Name: EventRPCUnreachable, Node: s.cfg.Name, Height: st.LastHeight,
				Key: s.cfg.Name + "/" + strconv.FormatInt(st.LastHeight, 10),
				Fields: map[string]any{
					"url": s.cfg.URL, "error": st.LastErr, "consecutiveFailures": st.Failures,
					"message": fmt.Sprintf("%s RPC %s failed %d times in a row: %s", s.cfg.Name, s.cfg.URL, st.Failures, st.LastErr),
				},
			})
		}
		return
	}
	if st.Unreachable {
		w.emit(event{
			Name: EventRPCRecovered, Node: s.cfg.Name, Height: s.view.Height,
			Key: s.cfg.Name + "/" + strconv.FormatInt(s.view.Height, 10),
			Fields: map[string]any{
				"url": s.cfg.URL, "failedPolls": st.Failures,
				"message": fmt.Sprintf("%s RPC %s is answering again at height %d.", s.cfg.Name, s.cfg.URL, s.view.Height),
			},
		})
	}
	st.Failures = 0
	st.Unreachable = false
	st.LastErr = ""
	st.LastHeight = s.view.Height

	behind, reason := nodeBehind(s, tip, tipOK, w.behindBlocks)
	f := map[string]any{
		"url": s.cfg.URL, "nodeHeight": s.view.Height, "catchingUp": s.view.CatchingUp,
		"moniker": s.view.Moniker, "validator": s.view.Validator, "reason": reason,
	}
	if tipOK {
		f["tipHeight"] = tip
		f["behindBlocks"] = tip - s.view.Height
	}
	if !s.view.LatestTime.IsZero() {
		f["latestBlockTime"] = s.view.LatestTime.UTC().Format(time.RFC3339)
	}
	switch {
	case behind && !st.Behind:
		st.Behind = true
		f["message"] = behindMessage(s, tip, tipOK, reason, w.behindBlocks)
		w.emit(event{Name: EventNodeBehind, Node: s.cfg.Name, Height: s.view.Height, Key: s.cfg.Name + "/" + reason + "/" + strconv.FormatInt(s.view.Height, 10), Fields: f})
	case !behind && st.Behind:
		st.Behind = false
		f["message"] = fmt.Sprintf("%s is synced at height %d.", s.cfg.Name, s.view.Height)
		f["reason"] = "synced"
		w.emit(event{Name: EventNodeSynced, Node: s.cfg.Name, Height: s.view.Height, Key: s.cfg.Name + "/" + strconv.FormatInt(s.view.Height, 10), Fields: f})
	}
}

func behindMessage(s sample, tip int64, tipOK bool, reason string, behindBlocks int64) string {
	if reason == "catching_up" {
		if tipOK {
			return fmt.Sprintf("%s reports catching_up at height %d; the tip is %d (%d blocks ahead). It has not caught up, so treat it as not signing yet.",
				s.cfg.Name, s.view.Height, tip, tip-s.view.Height)
		}
		return fmt.Sprintf("%s reports catching_up at height %d. No synced node is available to compare it with.", s.cfg.Name, s.view.Height)
	}
	return fmt.Sprintf("%s is %d blocks behind the tip %d. This alert fires when a synced node is more than %d blocks behind.",
		s.cfg.Name, tip-s.view.Height, tip, behindBlocks)
}

func nodeBehind(s sample, tip int64, tipOK bool, behindBlocks int64) (bool, string) {
	if s.err != nil {
		return false, ""
	}
	if s.view.CatchingUp {
		return true, "catching_up"
	}
	if !tipOK || behindBlocks < 0 {
		return false, ""
	}
	if tip-s.view.Height > behindBlocks {
		return true, "height"
	}
	return false, ""
}

// behindMessage's height branch currently has a nonsense "alert after"
// calculation. I'll fix that in the function itself — wait, I already
// wrote a bad expression. Let me fix behindMessage properly below by
// rewriting... I'll replace the function in a follow-up edit if I leave
// this comment. Actually I'll fix it now by not leaving the bad version.
// The function above is wrong. I'll overwrite the file section with search_replace.

func (w *watcher) observePower(ctx context.Context, samples []sample) {
	src, ok := pickValSource(samples, w.tipFrom)
	if !ok {
		return
	}
	vals, height, err := w.rpc.Validators(ctx, src.URL)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("validators %s (%s): %v", src.Name, src.URL, err)
		}
		return
	}
	report, err := BuildReport(vals, height, "", src.Name)
	if err != nil {
		log.Printf("validator set from %s: %v", src.Name, err)
		return
	}
	if w.st.UnsafeToStop == nil {
		w.st.UnsafeToStop = map[string]bool{}
	}
	seen := map[string]bool{}
	for _, c := range report.Validators {
		seen[c.Address] = true
		f := map[string]any{
			"consensusAddress": c.Address,
			"votingPower":      c.VotingPower,
			"totalVotingPower": report.TotalVotingPower,
			"powerShare":       c.Share,
			"remainingShare":   c.RemainingShare,
			"source":           src.Name,
			"message":          c.Message,
		}
		switch {
		case !c.SafeToStop && !w.st.UnsafeToStop[c.Address]:
			w.st.UnsafeToStop[c.Address] = true
			w.emit(event{Name: EventUnsafeToStop, Height: height, Key: c.Address + "/" + strconv.FormatInt(height, 10), Fields: f})
		case c.SafeToStop && w.st.UnsafeToStop[c.Address]:
			delete(w.st.UnsafeToStop, c.Address)
			w.emit(event{Name: EventSafeToStopAgain, Height: height, Key: c.Address + "/" + strconv.FormatInt(height, 10), Fields: f})
		}
	}
	// A validator that left the set while flagged is safe to take down
	// in the sense that it no longer has power. Say so once.
	var gone []string
	for addr := range w.st.UnsafeToStop {
		if !seen[addr] {
			gone = append(gone, addr)
		}
	}
	sort.Strings(gone)
	for _, addr := range gone {
		delete(w.st.UnsafeToStop, addr)
		w.emit(event{
			Name: EventSafeToStopAgain, Height: height, Key: addr + "/left/" + strconv.FormatInt(height, 10),
			Fields: map[string]any{
				"consensusAddress": addr, "source": src.Name,
				"message": fmt.Sprintf("%s is no longer in the validator set, so stopping its node does not remove voting power.", shortAddr(addr)),
			},
		})
	}
}

func pickTip(samples []sample, tipFrom string) (int64, bool) {
	if tipFrom != "" {
		for _, s := range samples {
			if s.cfg.Name == tipFrom && s.err == nil && !s.view.CatchingUp && s.view.Height > 0 {
				return s.view.Height, true
			}
		}
	}
	var max int64
	ok := false
	for _, s := range samples {
		if s.err != nil || s.view.CatchingUp || s.view.Height <= 0 {
			continue
		}
		if !ok || s.view.Height > max {
			max = s.view.Height
			ok = true
		}
	}
	return max, ok
}

func pickValSource(samples []sample, tipFrom string) (nodeCfg, bool) {
	if tipFrom != "" {
		for _, s := range samples {
			if s.cfg.Name == tipFrom && s.err == nil {
				return s.cfg, true
			}
		}
	}
	for _, s := range samples {
		if s.err == nil && !s.view.CatchingUp {
			return s.cfg, true
		}
	}
	for _, s := range samples {
		if s.err == nil {
			return s.cfg, true
		}
	}
	return nodeCfg{}, false
}

func summarize(samples []sample) []map[string]any {
	out := make([]map[string]any, 0, len(samples))
	for _, s := range samples {
		m := map[string]any{"name": s.cfg.Name}
		if s.err != nil {
			m["error"] = s.err.Error()
		} else {
			m["height"] = s.view.Height
			m["catchingUp"] = s.view.CatchingUp
		}
		out = append(out, m)
	}
	return out
}

func loadState(path string) (diskState, error) {
	st := diskState{Nodes: map[string]*nodeState{}}
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
	if st.Nodes == nil {
		st.Nodes = map[string]*nodeState{}
	}
	return st, nil
}

func saveState(path string, st diskState) error {
	bz, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".chainwatch-*")
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
