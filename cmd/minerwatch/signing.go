package main

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
)

// Two alerts about the validator set rather than one address's mining:
//
//   - signing_low: a watched validator's precommits are missing from the
//     chain's commits. x/pow removes a validator that misses more than
//     half of the last 60 (1,440 from SeatHardeningHeight), so this
//     fires well before that, while a flaky link can still be fixed.
//   - power_concentrated: one validator holds more than a share of the
//     voting power (a third by default). Above a third, that validator
//     going offline stops the chain on its own: removal needs blocks.
//   - validator_new: a consensus address not seen before gains voting
//     power. Under the seat rule a newcomer's power rises at most 10
//     points of the total per boundary, so a takeover by fresh accounts
//     shows here boundaries before it reaches a third.
const (
	EventSigningLow             = "signing_low"
	EventSigningRecovered       = "signing_recovered"
	EventPowerConcentrated      = "power_concentrated"
	EventPowerConcentrationEase = "power_concentration_eased"
	EventValidatorNew           = "validator_new"
)

// signRing is one validator's last results, true for signed.
type signRing struct {
	results []bool
	next    int
	filled  int
}

func newSignRing(size int) *signRing { return &signRing{results: make([]bool, size)} }

func (r *signRing) add(signed bool) {
	r.results[r.next] = signed
	r.next = (r.next + 1) % len(r.results)
	if r.filled < len(r.results) {
		r.filled++
	}
}

func (r *signRing) missed() int {
	n := 0
	for i := 0; i < r.filled; i++ {
		if !r.results[i] {
			n++
		}
	}
	return n
}

// missLimit is how many misses in the window raise signing_low: below
// warnBelow of the window signed.
func missLimit(window int, warnBelow float64) int {
	limit := int(float64(window)*(1-warnBelow) + 0.5)
	if limit < 1 {
		limit = 1
	}
	return limit
}

// checkSigning reads the commits of the blocks since the last poll (at
// most the window) and alerts on each watched validator whose misses
// reach the limit, and again when it signs its way back under.
func (w *watcher) checkSigning(ctx context.Context, h int64, snaps []snapshot) {
	if w.cfg.signingWarnBelow <= 0 || w.cfg.signingWindow <= 0 {
		return
	}
	watched := map[string]string{} // consensus address -> miner address
	for i, a := range w.addresses {
		s := snaps[i].status
		if s == nil || s.ConsensusAddress == "" {
			continue
		}
		if !s.ActiveValidator {
			// Out of the set: its next stint starts a fresh window.
			delete(w.rings, s.ConsensusAddress)
			continue
		}
		watched[s.ConsensusAddress] = a
	}
	if len(watched) == 0 {
		w.signedThrough = h
		return
	}
	if w.rings == nil {
		w.rings = map[string]*signRing{}
	}
	from := w.signedThrough + 1
	if oldest := h - int64(w.cfg.signingWindow) + 1; from < oldest {
		from = oldest
	}
	for k := from; k <= h; k++ {
		signers, err := w.src.CommitSigners(ctx, k)
		if err != nil {
			log.Printf("commit signers at %d: %v", k, err)
			return // retried from here next poll
		}
		for cons := range watched {
			signed, inSet := signers[cons]
			if !inSet {
				continue // not in the set that committed k-1
			}
			r := w.rings[cons]
			if r == nil {
				r = newSignRing(w.cfg.signingWindow)
				w.rings[cons] = r
			}
			r.add(signed)
		}
		w.signedThrough = k
	}

	limit := missLimit(w.cfg.signingWindow, w.cfg.signingWarnBelow)
	for cons, a := range watched {
		r := w.rings[cons]
		if r == nil {
			continue
		}
		st := w.state.Addresses[a]
		if st == nil {
			continue
		}
		missed := r.missed()
		f := map[string]any{
			"consensusAddress": cons,
			"missed":           missed,
			"of":               r.filled,
			"window":           w.cfg.signingWindow,
			"warnAtMissed":     limit,
		}
		switch {
		case missed >= limit && !st.SigningLow:
			f["message"] = fmt.Sprintf("%s (validator %s) missed %d of its last %d commits. Its votes aren't reaching the other validators: check its node's network and peers before it's removed for downtime.",
				a, short(cons), missed, r.filled)
			w.out.deliver(ctx, Event{Name: EventSigningLow, Address: a, Height: h, key: fmt.Sprint(h), Fields: f})
			st.SigningLow = true
		case missed < limit && st.SigningLow:
			w.out.deliver(ctx, Event{Name: EventSigningRecovered, Address: a, Height: h, key: fmt.Sprint(h), Fields: f})
			st.SigningLow = false
		}
	}
}

// checkValidatorSet reads the validator set once for power_concentrated
// and validator_new.
func (w *watcher) checkValidatorSet(ctx context.Context, h int64, snaps []snapshot) {
	if w.cfg.concentrationAbove <= 0 && !w.cfg.newValidators {
		return
	}
	powers, total, err := w.src.ValidatorSet(ctx, h)
	if err != nil {
		log.Printf("validator set at %d: %v", h, err)
		return
	}
	if total <= 0 {
		return
	}
	miners := map[string]string{}
	for i, a := range w.addresses {
		if s := snaps[i].status; s != nil && s.ConsensusAddress != "" {
			miners[s.ConsensusAddress] = a
		}
	}
	if w.cfg.newValidators {
		w.checkNewValidators(ctx, h, powers, total, miners)
	}
	if w.cfg.concentrationAbove > 0 {
		w.checkConcentration(ctx, h, powers, total, miners)
	}
}

// checkNewValidators alerts once for each consensus address that gains
// voting power without having been seen before. The first read of the
// set only records it.
func (w *watcher) checkNewValidators(ctx context.Context, h int64, powers map[string]int64, total int64, miners map[string]string) {
	first := w.state.Validators == nil
	if first {
		w.state.Validators = map[string]bool{}
	}
	cons := make([]string, 0, len(powers))
	for c, p := range powers {
		if p > 0 && !w.state.Validators[c] {
			cons = append(cons, c)
		}
	}
	sort.Strings(cons)
	for _, c := range cons {
		w.state.Validators[c] = true
		if first {
			continue
		}
		share := float64(powers[c]) / float64(total)
		who := "validator " + short(c)
		if a := miners[c]; a != "" {
			who = fmt.Sprintf("%s (validator %s)", a, short(c))
		}
		w.out.deliver(ctx, Event{Name: EventValidatorNew, Address: miners[c], Height: h, key: c, Fields: map[string]any{
			"consensusAddress": c,
			"votingPower":      powers[c],
			"totalVotingPower": total,
			"powerShare":       strconv.FormatFloat(share, 'f', 4, 64),
			"validators":       len(powers),
			"message": fmt.Sprintf("%s, not seen before, joined the validator set with %.1f%% of the voting power. Under the seat rule its power can rise at most 10 points of the total per boundary: check whose miner this is.",
				who, 100*share),
		}})
	}
}

// checkConcentration alerts when a validator's share of the voting
// power passes the threshold, and when it's back at or under it.
func (w *watcher) checkConcentration(ctx context.Context, h int64, powers map[string]int64, total int64, miners map[string]string) {
	if w.state.Concentrated == nil {
		w.state.Concentrated = map[string]bool{}
	}
	cons := make([]string, 0, len(powers))
	for c := range powers {
		cons = append(cons, c)
	}
	for c := range w.state.Concentrated {
		if _, ok := powers[c]; !ok {
			cons = append(cons, c) // left the set while concentrated
		}
	}
	sort.Strings(cons)
	for _, c := range cons {
		share := float64(powers[c]) / float64(total)
		f := map[string]any{
			"consensusAddress": c,
			"votingPower":      powers[c],
			"totalVotingPower": total,
			"powerShare":       strconv.FormatFloat(share, 'f', 4, 64),
			"threshold":        strconv.FormatFloat(w.cfg.concentrationAbove, 'f', 4, 64),
			"validators":       len(powers),
		}
		a := miners[c]
		over := share > w.cfg.concentrationAbove
		switch {
		case over && !w.state.Concentrated[c]:
			who := "validator " + short(c)
			if a != "" {
				who = fmt.Sprintf("%s (validator %s)", a, short(c))
			}
			f["message"] = fmt.Sprintf("%s holds %.1f%% of the voting power, over %.1f%%. If it goes offline the others hold under two thirds and the chain stops until it's back.",
				who, 100*share, 100*w.cfg.concentrationAbove)
			w.out.deliver(ctx, Event{Name: EventPowerConcentrated, Address: a, Height: h, key: c + "/" + fmt.Sprint(h), Fields: f})
			w.state.Concentrated[c] = true
		case !over && w.state.Concentrated[c]:
			w.out.deliver(ctx, Event{Name: EventPowerConcentrationEase, Address: a, Height: h, key: c + "/" + fmt.Sprint(h), Fields: f})
			delete(w.state.Concentrated, c)
		}
	}
}

// short is a consensus address's first 8 hex digits, as operators name
// validators.
func short(cons string) string {
	if len(cons) > 8 {
		return cons[:8]
	}
	return cons
}
