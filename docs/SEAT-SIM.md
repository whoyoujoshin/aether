# Seat rule simulation

`x/pow/seatsim_test.go` is an epoch-stepped model of the seat rule
([SEAT-RULE.md](SEAT-RULE.md)). It calls the real `computeSeats` at every
boundary, so it can't drift from consensus. On top of that it adds:

- the 3-epoch work window;
- miners that never register a consensus key;
- downtime removals between boundaries, which happen only while the rest of
  the set holds over 2/3, since blocks have to keep coming.

It's a test file, so it never goes into `aetherd`. Its scenarios assert
what the rule activating at 396,000 does, and what the hardening behind
`SeatHardeningHeight` changes ([SEAT-RULE.md](SEAT-RULE.md#hardening-built-behind-seathardeningheight)).
The A4 and A5 fix runs the chain's own code (`computeSeats`'
`eligibleTotal`).

```bash
go test ./x/pow/ -run TestSeatSim -v
```

Work is in abstract units per epoch: only the ratios matter. Every scenario
starts from the set as it stands at activation, with each validator at the
old rule's flat 1,000,000, and four honest CPU validators with equal work.

## Results (2026-10-20, at `561fc6c`)

### A1: the documented ramp

A registered newcomer with 90% of the hash, native. Its share at each
boundary:

| Boundary | 1 | 2 | 3 | 4 | 5 | 6 | 7 |
|---|---|---|---|---|---|---|---|
| Share | 10% | 20% | 30% | **40%** | 50% | 60% | **70%** |

It passes 1/3 at the 4th boundary and 2/3 at the 7th, as SEAT-RULE.md says.
The entry limit moves at most 10% of the total power per boundary.

### A4: denominator capture (confirmed)

An honest pool merge-mines with 96.2% of the hash and never registers a key.
The attacker has 1.03% of the hash, which is 27% of the registered hash.

| Boundary | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 |
|---|---|---|---|---|---|---|---|---|
| As at 396,000 | 0% | 10% | 20% | 30% | **40%** | 50% | 60% | **70%** |
| 1% bar on eligible work only | 10% | 20% | 27.3% | 27.3% | 27.3% | 27.3% | 27.3% | 27.3% |

- **As at 396,000:** the CPU validators fall under 1%, so the attacker is the
  only one that qualifies and its target is the whole set. It can halt the
  chain from the 5th boundary and controls it from the 8th. The first
  boundary is lost while the pool's work fills the window.
- **With the fix:** the attacker holds what its registered work is worth.

### A5: freeze (confirmed)

The same pool, with no attacker. One honest validator drops out for three
minutes.

- **As at 396,000:** all 7 boundaries froze, because nobody qualified. The
  removed validator never came back, and 3 validators were left.
- **With the fix:** no boundary froze, and all 4 validators came back.

### A6: downtime amplification (confirmed)

The attacker has 30% of the work, and four honest validators have 17.5%
each. The attacker knocks one honest validator offline at a time.

- **After each removal**, the attacker's share is 36.4%, then 46.2%, then
  63.2%. The last honest validator holds over 1/3, so taking it offline
  halts the chain rather than removing it.
- **At each boundary after that**, the victims mine on and come back at the
  entry limit's pace: 56.8%, 50.5%, 44.2%, 37.9%, then 31.6%. The attacker
  is back under 1/3 at the **5th** boundary, about 12 hours later.
- **With reserved power** (a removed validator keeps its power at the next
  boundary), the attacker is back at 30% at the first boundary. That fixes
  recovery, but not the jump within the epoch.

**Sweep.** The peak share an attacker reaches against four equal honest
validators, by its share of the work:

| Work share | 20% | 21% | 25% | 30% | 33% | 34% | 40% |
|---|---|---|---|---|---|---|---|
| Peak share | 33% | 52% | 57% | 63% | 66% | **100%** | 100% |

- **Over 20%:** one removal gives the attacker halt power.
- **From 34%:** the attacker holds over twice the last honest validator's
  power. That validator then holds under 1/3 and can be removed too, so the
  attacker holds the whole set.

### A6 under the hardening

The hardening's liveness window (1,440 blocks) makes each removal take
about 68 minutes, so about two rounds fit in an epoch. Its reserved power
returns the victims at the next boundary. The simulation looks for the
least share of the work that reaches control (over 2/3). In each round,
the attacker takes offline as many honest validators as it can while the
rest hold over 2/3.

| Control from | 4 honest validators | 10 honest validators |
|---|---|---|
| As at 396,000 (3-minute removals) | 34% | 17% |
| Hardened (68-minute removals, reserved power) | 34% | 34% |
| Hardened plus a 10% cap on removals per epoch (modelled only) | 61% | 62% |

- **The window and the reserve:** they bound the attack to one epoch and
  slow it down, but don't raise the bar. Two of four validators (33% in
  all) go offline together and blocks continue.
- **The cap:** this is what raises the bar.
- **Many small validators:** they make the attack cheaper under today's
  rule, down to 17%.

## Not modeled yet

These come from Grok's plan:

- the merged-mining track's own retarget and slots (A2, A3);
- timing games (A8);
- economics (A9, A10);
- the cost in dollars of each attack;
- natural downtime, such as peer-1's VPN drop-outs and the seed running out
  of memory, fed in from the live testnet after 396,000.
