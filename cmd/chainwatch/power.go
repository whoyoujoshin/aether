package main

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// CometBFT commits a block only when the precommits for it hold strictly
// more than two thirds of the voting power. A validator can be stopped
// when the others still hold strictly more than two thirds: with four
// validators, one that holds a third or more halts the chain on its own,
// because the rest are at or under two thirds and no further block (and
// so no downtime removal) can happen until it is back.

const stopRule = "CometBFT commits a block only when precommits hold strictly more than 2/3 of the voting power. A validator is safe to stop when the others still hold strictly more than 2/3."

// moreThanTwoThirds reports whether part/total is strictly greater than 2/3.
func moreThanTwoThirds(part, total int64) bool {
	if part <= 0 || total <= 0 {
		return false
	}
	left := new(big.Int).Mul(big.NewInt(part), big.NewInt(3))
	right := new(big.Int).Mul(big.NewInt(total), big.NewInt(2))
	return left.Cmp(right) > 0
}

// SafeToStop reports whether the validator set can still commit after the
// voting power `power` is removed from `total`.
func SafeToStop(total, power int64) bool {
	if total <= 0 || power < 0 || power > total {
		return false
	}
	return moreThanTwoThirds(total-power, total)
}

// HasQuorum reports whether signed voting power is enough to commit.
func HasQuorum(signed, total int64) bool {
	return moreThanTwoThirds(signed, total)
}

// valPower is one validator in a CometBFT set.
type valPower struct {
	Address string
	Power   int64
}

// StopChoice is one validator in a safe-to-stop report.
type StopChoice struct {
	Address        string `json:"address"`
	VotingPower    int64  `json:"votingPower"`
	Share          string `json:"share"`
	RemainingPower int64  `json:"remainingPower"`
	RemainingShare string `json:"remainingShare"`
	SafeToStop     bool   `json:"safeToStop"`
	Message        string `json:"message"`
}

// StopReport is the answer to "can any single validator be taken offline?".
type StopReport struct {
	Height           int64        `json:"height,omitempty"`
	ChainID          string       `json:"chainId,omitempty"`
	Source           string       `json:"source,omitempty"`
	TotalVotingPower int64        `json:"totalVotingPower"`
	Rule             string       `json:"rule"`
	Validators       []StopChoice `json:"validators"`
	// SafeToStopAnyOne is true when every validator, taken one at a time,
	// leaves the others with strictly more than 2/3.
	SafeToStopAnyOne bool `json:"safeToStopAnyOne"`
}

// BuildReport sorts validators by address and marks each one.
func BuildReport(vals []valPower, height int64, chainID, source string) (StopReport, error) {
	if len(vals) == 0 {
		return StopReport{}, fmt.Errorf("validator set is empty")
	}
	var total int64
	seen := map[string]int64{}
	for _, v := range vals {
		addr := strings.ToUpper(strings.TrimSpace(v.Address))
		if addr == "" {
			return StopReport{}, fmt.Errorf("validator with empty address")
		}
		if v.Power < 0 {
			return StopReport{}, fmt.Errorf("validator %s has negative voting power", addr)
		}
		seen[addr] += v.Power
		total += v.Power
	}
	if total <= 0 {
		return StopReport{}, fmt.Errorf("total voting power is zero")
	}
	addrs := make([]string, 0, len(seen))
	for a := range seen {
		addrs = append(addrs, a)
	}
	sort.Strings(addrs)
	out := StopReport{
		Height: height, ChainID: chainID, Source: source,
		TotalVotingPower: total, Rule: stopRule, SafeToStopAnyOne: true,
	}
	for _, a := range addrs {
		p := seen[a]
		rem := total - p
		ok := SafeToStop(total, p)
		if !ok {
			out.SafeToStopAnyOne = false
		}
		var msg string
		if ok {
			msg = fmt.Sprintf("%s holds %s of the voting power. Stopping it leaves %s, still more than 2/3, so the others can keep committing.",
				shortAddr(a), pct(p, total), pct(rem, total))
		} else {
			msg = fmt.Sprintf("%s holds %s of the voting power. Stopping it leaves %s, which is not more than 2/3, so the chain halts until it is back.",
				shortAddr(a), pct(p, total), pct(rem, total))
		}
		out.Validators = append(out.Validators, StopChoice{
			Address: a, VotingPower: p,
			Share: shareString(p, total), RemainingPower: rem,
			RemainingShare: shareString(rem, total), SafeToStop: ok, Message: msg,
		})
	}
	return out, nil
}

// Find returns the choice whose address equals want, or whose address
// starts with want when want is at least 8 hex digits and only one
// validator matches. Operators name validators by the first 8 digits.
func (r StopReport) Find(want string) (StopChoice, error) {
	want = strings.ToUpper(strings.TrimSpace(want))
	if want == "" {
		return StopChoice{}, fmt.Errorf("empty validator address")
	}
	var hits []StopChoice
	for _, c := range r.Validators {
		if c.Address == want || (len(want) >= 8 && strings.HasPrefix(c.Address, want)) {
			hits = append(hits, c)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return StopChoice{}, fmt.Errorf("validator %s is not in the set", want)
	default:
		return StopChoice{}, fmt.Errorf("validator prefix %s matches %d validators", want, len(hits))
	}
}

// FormatReport is the text an operator reads before touching a node.
func FormatReport(r StopReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "height %d  total voting power %d\n", r.Height, r.TotalVotingPower)
	if r.ChainID != "" {
		fmt.Fprintf(&b, "chain %s\n", r.ChainID)
	}
	if r.Source != "" {
		fmt.Fprintf(&b, "source %s\n", r.Source)
	}
	for _, c := range r.Validators {
		verdict := "safe to stop"
		if !c.SafeToStop {
			verdict = "UNSAFE to stop"
		}
		fmt.Fprintf(&b, "%s  power %d (%s)  %s: others would hold %s\n",
			c.Address, c.VotingPower, pct(c.VotingPower, r.TotalVotingPower), verdict, pct(c.RemainingPower, r.TotalVotingPower))
	}
	if r.SafeToStopAnyOne {
		fmt.Fprintf(&b, "any single validator can be stopped; the others would still hold more than 2/3\n")
	} else {
		fmt.Fprintf(&b, "at least one validator cannot be stopped without dropping the others to 2/3 or less\n")
	}
	fmt.Fprintf(&b, "%s\n", r.Rule)
	return b.String()
}

func shortAddr(a string) string {
	if len(a) > 8 {
		return a[:8]
	}
	return a
}

func shareString(part, total int64) string {
	if total <= 0 {
		return "0.0000"
	}
	return new(big.Rat).SetFrac(big.NewInt(part), big.NewInt(total)).FloatString(4)
}

func pct(part, total int64) string {
	if total <= 0 {
		return "0.0%"
	}
	// One decimal, rounded, so 1/3 prints as 33.3%.
	v := new(big.Int).Mul(big.NewInt(part), big.NewInt(1000))
	v.Quo(v, big.NewInt(total)) // tenths of a percent, truncated
	// Round from the next digit.
	rem := new(big.Int).Mul(big.NewInt(part), big.NewInt(10000))
	rem.Quo(rem, big.NewInt(total))
	tenths := new(big.Int).Set(v)
	next := new(big.Int).Mod(rem, big.NewInt(10))
	if next.Cmp(big.NewInt(5)) >= 0 {
		tenths.Add(tenths, big.NewInt(1))
	}
	whole := new(big.Int).Quo(tenths, big.NewInt(10))
	frac := new(big.Int).Mod(tenths, big.NewInt(10))
	return fmt.Sprintf("%s.%s%%", whole.String(), frac.String())
}
