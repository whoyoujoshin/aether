package pow

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"
)

// An epoch-stepped model of the seat rule (docs/SEAT-SIM.md). Every
// boundary calls the real computeSeats, so the model can't drift from
// consensus; what it adds is the 3-epoch work window, miners that never
// register a consensus key, and downtime removals between boundaries
// (CheckValidatorLiveness removes a validator at once, and it comes back
// only through a later boundary).
//
//	go test ./x/pow/ -run TestSeatSim -v
//
// The scenarios assert what the rule activating at 396,000 does, A4 to A6
// included, so a change to the rule shows up here first.

type simNet struct {
	registered map[string]bool
	history    []map[string]int64 // seat work per epoch, oldest first
	prev       []seat
	// eligibleOnly is the fix for A4/A5 from SeatHardeningHeight: the 1%
	// bar is measured against eligible work only (computeSeats'
	// eligibleTotal).
	eligibleOnly bool
	// reserve models a candidate fix for A6: a validator removed for
	// downtime keeps its power at the next boundary, as if it hadn't left.
	reserve bool
	removed map[string]int64
	frozen  int // boundaries at which nobody qualified
	// exitCap, if set, models a cap on downtime removals: at most this
	// share of the power is removed between two boundaries. Not in the
	// chain's code: a candidate to decide on.
	exitCap float64
}

// newSimNet starts with validators at the old rule's flat power, as the
// set stands at the activation height.
func newSimNet(validators ...string) *simNet {
	n := &simNet{registered: map[string]bool{}, removed: map[string]int64{}}
	for _, v := range validators {
		n.registered[v] = true
		n.prev = append(n.prev, seat{Addr: seatAddr(v), Power: ValidatorVotingPower})
	}
	return n
}

func (n *simNet) register(names ...string) {
	for _, m := range names {
		n.registered[m] = true
	}
}

// boundary adds an epoch's work and recomputes the set, as the end of
// height n×1440−1 does.
func (n *simNet) boundary(work map[string]int64) {
	n.history = append(n.history, work)
	window := map[string]int64{}
	from := max(0, len(n.history)-int(SeatWorkWindowEpochs))
	for _, w := range n.history[from:] {
		for m, v := range w {
			window[m] += v
		}
	}
	names := make([]string, 0, len(window))
	for m := range window {
		names = append(names, m)
	}
	sort.Strings(names)
	var cands []seatCandidate
	for _, m := range names {
		if window[m] == 0 {
			continue
		}
		cands = append(cands, seatCandidate{Addr: seatAddr(m), Work: math.NewInt(window[m]), Eligible: n.registered[m]})
	}
	prev := n.prev
	if n.reserve {
		for m, p := range n.removed {
			prev = append(prev, seat{Addr: seatAddr(m), Power: p})
		}
	}
	n.removed = map[string]int64{}
	seats, ok := computeSeats(cands, prev, 21, n.eligibleOnly)
	if !ok {
		n.frozen++
		return
	}
	n.prev = seats
}

// remove takes a validator out between boundaries, as a liveness removal
// does. It happens only while blocks keep coming: the rest of the set
// must hold more than 2/3 of the power for the victim to miss 31 of 60.
func (n *simNet) remove(name string) bool {
	if n.live(name) <= 2.0/3 {
		return false
	}
	if n.exitCap > 0 {
		var gone, total int64
		for _, p := range n.removed {
			gone += p
		}
		for _, s := range n.prev {
			total += s.Power
		}
		if float64(gone+share64(n.prev, name)) > n.exitCap*float64(total+gone) {
			return false
		}
	}
	for i, s := range n.prev {
		if strings.TrimSpace(string(s.Addr)) == name {
			n.removed[name] = s.Power
			n.prev = append(n.prev[:i], n.prev[i+1:]...)
			return true
		}
	}
	return false
}

// live is the share of power left signing with offline down.
func (n *simNet) live(offline ...string) float64 {
	var on, total int64
	for _, s := range n.prev {
		total += s.Power
		if !contains(offline, strings.TrimSpace(string(s.Addr))) {
			on += s.Power
		}
	}
	return float64(on) / float64(total)
}

func (n *simNet) share(name string) float64 { return share(n.prev, name) }

func share64(seats []seat, name string) int64 {
	for _, s := range seats {
		if strings.TrimSpace(string(s.Addr)) == name {
			return s.Power
		}
	}
	return 0
}

func (n *simNet) size() int { return len(n.prev) }

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// steady is per-epoch work for each miner.
type steady map[string]int64

func (w steady) copy() map[string]int64 {
	out := map[string]int64{}
	for k, v := range w {
		out[k] = v
	}
	return out
}

var honest4 = []string{"honest-a", "honest-b", "honest-c", "honest-d"}

func honestWork(each int64) steady {
	w := steady{}
	for _, h := range honest4 {
		w[h] = each
	}
	return w
}

// warm runs the honest network alone for a full window, so every honest
// validator has three epochs of work behind it.
func warm(n *simNet, w steady) {
	for i := int64(0); i < SeatWorkWindowEpochs; i++ {
		n.boundary(w.copy())
	}
}

// firstAbove is the first epoch (1-based) at which shares passes x, or 0.
func firstAbove(shares []float64, x float64) int {
	for i, s := range shares {
		if s > x {
			return i + 1
		}
	}
	return 0
}

func pct(xs []float64) string {
	var b strings.Builder
	for _, x := range xs {
		fmt.Fprintf(&b, " %4.1f", 100*x)
	}
	return b.String()
}

// A1, the documented ramp: a newcomer with 90% of the hash, native,
// registered. It passes 1/3 at the fourth boundary and 2/3 at the
// seventh, as docs/SEAT-RULE.md says.
func TestSeatSim_A1_NativeRamp(t *testing.T) {
	n := newSimNet(honest4...)
	warm(n, honestWork(10))
	n.register("attacker")
	w := honestWork(10)
	w["attacker"] = 360 // 90% of each epoch's work
	var shares []float64
	for e := 0; e < 10; e++ {
		n.boundary(w.copy())
		shares = append(shares, n.share("attacker"))
	}
	t.Logf("A1 attacker share by epoch (%%):%s", pct(shares))
	require.Equal(t, 4, firstAbove(shares, 1.0/3))
	require.Equal(t, 7, firstAbove(shares, 2.0/3))
}

// A4, denominator capture. An honest pool merge-mines with 96% of the
// hash and never registers a consensus key. Its work still counts toward
// the total the 1% bar is measured against, so the four CPU validators
// fall below it. An attacker with ~1% of the hash and a key is then the
// only one that qualifies: its target is all the power, and it takes it
// at the entry limit's pace.
func TestSeatSim_A4_DenominatorCapture(t *testing.T) {
	run := func(eligibleOnly bool) []float64 {
		n := newSimNet(honest4...)
		n.eligibleOnly = eligibleOnly
		warm(n, honestWork(10))
		n.register("attacker")
		w := honestWork(10)
		w["pool"] = 1400   // unregistered: 96.2% of the hash
		w["attacker"] = 15 // registered: 1.03% of the hash, 27% of the registered hash
		var shares []float64
		for e := 0; e < 12; e++ {
			n.boundary(w.copy())
			shares = append(shares, n.share("attacker"))
		}
		return shares
	}
	now := run(false)
	t.Logf("A4 attacker share by epoch, as at 396,000 (%%):%s", pct(now))
	// The first boundary is lost while the pool's work fills the window;
	// from the second the attacker alone qualifies.
	require.Equal(t, 5, firstAbove(now, 1.0/3), "1% of the hash can halt the chain five boundaries after the pool arrives")
	require.Equal(t, 8, firstAbove(now, 2.0/3), "and controls it at the eighth")

	fixed := run(true)
	t.Logf("A4 attacker share by epoch, 1%% bar on eligible work only (%%):%s", pct(fixed))
	require.Zero(t, firstAbove(fixed, 1.0/3), "with the fix it holds what its registered work is worth")
	require.InDelta(t, 15.0/55, fixed[len(fixed)-1], 0.01)
}

// A5, the freeze. The same unregistered pool, no attacker: nobody
// qualifies, so every boundary leaves the set as it is. A downtime
// removal then never comes back, and the set only shrinks.
func TestSeatSim_A5_NoQualifierFreeze(t *testing.T) {
	run := func(eligibleOnly bool) *simNet {
		n := newSimNet(honest4...)
		n.eligibleOnly = eligibleOnly
		warm(n, honestWork(10))
		w := honestWork(10)
		w["pool"] = 5000
		n.boundary(w.copy())
		require.True(t, n.remove("honest-d"), "honest-d drops out for three minutes")
		for e := 0; e < 6; e++ {
			n.boundary(w.copy())
		}
		return n
	}
	now := run(false)
	t.Logf("A5 as at 396,000: %d of 7 boundaries froze; %d validators left", now.frozen, now.size())
	require.Equal(t, 7, now.frozen)
	require.Equal(t, 3, now.size(), "the removed validator never returns")

	fixed := run(true)
	t.Logf("A5 with the fix: %d boundaries froze; %d validators", fixed.frozen, fixed.size())
	require.Zero(t, fixed.frozen)
	require.Equal(t, 4, fixed.size())
}

// a6Net is an attacker with attackerWork per epoch against four honest
// validators with honestEach, converged to work-proportional powers.
func a6Net(attackerWork, honestEach int64, reserve bool) (*simNet, steady) {
	n := newSimNet(append([]string{"attacker"}, honest4...)...)
	n.reserve = reserve
	w := honestWork(honestEach)
	w["attacker"] = attackerWork
	for e := 0; e < 12; e++ {
		n.boundary(w.copy())
	}
	return n, w
}

// knockOut has the attacker take honest validators offline one at a time
// for as long as each removal still happens, and returns its share after
// each.
func knockOut(n *simNet) []float64 {
	var peaks []float64
	for _, victim := range honest4 {
		if !n.remove(victim) {
			break
		}
		peaks = append(peaks, n.share("attacker"))
	}
	return peaks
}

// A6, downtime amplification. An attacker with 30% of the work, four
// honest validators with 17.5% each. The attacker knocks one honest
// validator offline at a time (three minutes is enough): while the rest
// hold over 2/3 blocks continue, the victim is removed, and every other
// share jumps with no damping. Removed validators come back only through
// later boundaries, at the entry limit's pace.
func TestSeatSim_A6_DowntimeAmplification(t *testing.T) {
	run := func(reserve bool) (peaks []float64, recovery []float64) {
		n, w := a6Net(60, 35, reserve) // 30% and 4 × 17.5%
		for e := 0; e < 12; e++ {
			n.boundary(w.copy()) // converge to work-proportional powers
		}
		require.InDelta(t, 0.30, n.share("attacker"), 0.005)
		peaks = knockOut(n)
		for e := 0; e < 8; e++ {
			n.boundary(w.copy()) // the victims mine on and come back
			recovery = append(recovery, n.share("attacker"))
		}
		return peaks, recovery
	}
	peaks, recovery := run(false)
	t.Logf("A6 attacker share after each removal (%%):%s", pct(peaks))
	t.Logf("A6 attacker share at each boundary after (%%):%s", pct(recovery))
	require.Greater(t, peaks[0], 1.0/3, "one removal gives a 30% attacker the power to halt")
	require.Len(t, peaks, 3, "the last honest validator holds over 1/3: taking it offline halts the chain instead")
	require.InDelta(t, 0.632, peaks[2], 0.001)
	back := 0
	for i, s := range recovery {
		if s <= 1.0/3 {
			back = i + 1
			break
		}
	}
	t.Logf("A6 boundaries until the attacker is back under 1/3: %d", back)
	require.Greater(t, back, 1, "recovery takes several boundaries")

	_, reserved := run(true)
	t.Logf("A6 with reserved power, share at each boundary after (%%):%s", pct(reserved))
	require.InDelta(t, 0.30, reserved[0], 0.005, "reserving a removed validator's power undoes the jump at the next boundary")
}

// A6 sweep: the least share of the work from which knocking honest
// validators out one by one ends in control (over 2/3), against four
// equal honest validators.
func TestSeatSim_A6_ControlThreshold(t *testing.T) {
	control := 0
	var rows []string
	for a := int64(20); a <= 50; a++ {
		n, _ := a6Net(a*4, 100-a, false) // attacker a%, each honest (100-a)/4 %
		peaks := knockOut(n)
		peak := 0.0
		if len(peaks) > 0 {
			peak = peaks[len(peaks)-1]
		}
		rows = append(rows, fmt.Sprintf("%d%%→%.0f%%", a, 100*peak))
		if control == 0 && peak > 2.0/3 {
			control = int(a)
		}
	}
	t.Logf("A6 attacker work share → peak share after removals: %s", strings.Join(rows, " "))
	t.Logf("A6 smallest share of the work that ends in control: %d%%", control)
	// The last honest validator can be removed only while it holds under
	// 1/3, i.e. while the attacker holds over twice its power.
	require.Equal(t, 34, control)
}

// honestN is n honest validators' names.
func honestN(n int) []string {
	var out []string
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf("honest-%02d", i))
	}
	return out
}

// removalRound takes offline, all at once, as many of the honest
// validators still seated as it can while the rest keep over 2/3 (so
// blocks continue and every victim is removed), and removes them.
func removalRound(n *simNet, honest []string) bool {
	var victims []string
	for _, h := range honest {
		if n.share(h) == 0 {
			continue
		}
		if n.live(append(victims, h)...) > 2.0/3 {
			victims = append(victims, h)
		}
	}
	for _, v := range victims {
		n.remove(v)
	}
	return len(victims) > 0
}

// a6Peak is the highest share an attacker with attackerPct of the work
// reaches against nHonest equal validators, with at most roundsPerEpoch
// removal rounds between boundaries, over several epochs.
func a6Peak(attackerPct, nHonest, roundsPerEpoch int, hardened bool, exitCap float64) float64 {
	honest := honestN(nHonest)
	n := newSimNet(append([]string{"attacker"}, honest...)...)
	n.reserve = hardened
	n.eligibleOnly = hardened
	n.exitCap = exitCap
	w := steady{"attacker": int64(attackerPct * nHonest)}
	for _, h := range honest {
		w[h] = int64(100 - attackerPct)
	}
	for e := 0; e < 12; e++ {
		n.boundary(w.copy())
	}
	peak := n.share("attacker")
	for e := 0; e < 4; e++ {
		for r := 0; r < roundsPerEpoch && removalRound(n, honest); r++ {
			peak = max(peak, n.share("attacker"))
		}
		n.boundary(w.copy())
	}
	return peak
}

// A6 under the hardening. A removal now takes 721 of 1,440 blocks
// missed (~68 minutes), so about two rounds fit in an epoch, and a
// victim whose miner mines on is back at the next boundary. That bounds
// how long a gain lasts and slows it down, but doesn't raise the bar:
// an attacker takes several validators offline at once (under 1/3 in
// all, so blocks continue) and needs only two rounds. A cap on how much
// power downtime can remove per epoch (modelled here, not in the chain)
// is what raises it.
func TestSeatSim_A6_Hardened(t *testing.T) {
	type row struct {
		nHonest, rounds int
		hardened        bool
		exitCap         float64
	}
	threshold := func(r row) int {
		for a := 15; a <= 80; a++ {
			if a6Peak(a, r.nHonest, r.rounds, r.hardened, r.exitCap) > 2.0/3 {
				return a
			}
		}
		return 0
	}
	label := func(r row) string {
		switch {
		case r.exitCap > 0:
			return fmt.Sprintf("hardened + %.0f%% removal cap", 100*r.exitCap)
		case r.hardened:
			return "hardened (68-minute removals, reserve)"
		}
		return "as at 396,000 (3-minute removals)"
	}
	got := map[row]int{}
	for _, r := range []row{
		{4, 40, false, 0}, {4, 2, true, 0}, {4, 2, true, 0.1},
		{10, 40, false, 0}, {10, 2, true, 0}, {10, 2, true, 0.1},
	} {
		got[r] = threshold(r)
		t.Logf("A6 %2d honest validators, %-40s control from %d%% of the work", r.nHonest, label(r)+":", got[r])
	}
	require.Equal(t, 34, got[row{4, 40, false, 0}])
	require.Equal(t, 34, got[row{4, 2, true, 0}], "the window and the reserve don't raise the bar")
	require.Equal(t, 17, got[row{10, 40, false, 0}], "many small validators make it cheaper")
	require.Equal(t, 34, got[row{10, 2, true, 0}])
	require.Greater(t, got[row{4, 2, true, 0.1}], 50, "a removal cap does")
	require.Greater(t, got[row{10, 2, true, 0.1}], 50)
}
