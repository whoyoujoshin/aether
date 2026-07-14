# Aether Randomness Beacon & Validator Bonding — Design Spec v0.1
**Status:** Draft — pre-implementation, needs external cryptographic review before any mainnet path
**Scope:** Resolves the multi-validator design question (epoch-based, hashrate-derived validator set) and the grinding-resistance question (randomness beacon) identified in prior design sessions.

---

## 1. Why this document exists

Earlier design passes proposed "Top-K miners by recent work + VRF sampling" for validator selection. Two real gaps were identified in that proposal:

1. **The VRF seed itself was grindable** — a single block hash, produced by the same miners being selected from, gives an attacker a way to bias their own selection odds by choosing which valid nonce to reveal.
2. **The bonding/slashing mechanism was named but not specified** — "forfeit escrow on proven equivocation" requires an actual evidence-submission and verification subsystem, not just a policy statement.

This document resolves (1) concretely and scopes (2) into buildable pieces. It also surfaces a third issue, found while researching implementation options: **the standard, practically-available randomness-beacon primitive (VDF) is not post-quantum secure**, which is a direct tension with Aether's core identity. That tension is addressed head-on below rather than glossed over.

---

## 2. The post-quantum VDF gap — addressed honestly

**What a Verifiable Delay Function (VDF) is for:** it forces a seed to be computed through genuinely sequential work (not parallelizable), so no one — however much hardware they have — can pre-compute many candidate seeds and pick the one that favors them. This is the real fix for the grinding problem identified earlier.

**The gap:** the practical, published, implemented constructions (Wesolowski 2018, Pietrzak 2019 — both have working Go implementations) derive their sequential-work guarantee from repeated squaring in a group of unknown order (an RSA group or a class group). That hardness assumption is exactly the kind of problem Shor's algorithm solves in polynomial time on a sufficiently large quantum computer. **A chain built around quantum-resistant signatures would be using a classically-quantum-breakable primitive for validator-selection randomness — a real, if delayed, contradiction of the chain's own premise.**

The alternative — genuinely post-quantum VDF constructions based on isogenies — exists only as recent academic research (Chávez-Saab et al., 2021), has no mature or audited Go implementation, and isogeny-based cryptography has had serious breaks in closely related schemes (SIDH, 2022) within the last few years. Treating this as "just use the post-quantum one instead" would trade a known, delayed risk for an unaudited, immediate one. That's not a responsible substitution.

### Recommended resolution: a hash-chain sequential-work beacon, not a name-brand VDF

Rather than reaching for a cryptographic name that doesn't actually fit Aether's threat model, the honest engineering answer is a **hash-chain-based delay** (sometimes called a "weak VDF" or proof-of-sequential-work in the literature — this is closer to what Chia's *early* prototypes used before their mature class-group VDF):

- Take the epoch's accumulated block-hash chain.
- Run it through N sequential rounds of a hash function (e.g., BLAKE3 or SHA-3), where each round's input is the previous round's output — genuinely sequential by construction, no shortcut exists other than doing the hashing.
- The resulting value is the epoch seed.

**Why this is the right tradeoff for Aether specifically, not just a fallback:**
- Its security reduces to hash-function preimage/collision resistance, not integer factorization or class-group structure. Grover's algorithm gives only a quadratic speedup against hash functions (doubling the hash output size restores the original security margin) — nowhere near Shor's exponential break of RSA/class-group assumptions. This is *meaningfully* more consistent with a chain that's already planning to be quantum-resistant elsewhere.
- It does **not** have the asymptotically-efficient (polylogarithmic) verification that makes something a "true" VDF in the strict cryptographic sense — verification here means re-running the hash chain, which costs real time, not an instant proof. That's a genuine cost, not a rounding error, and needs to be weighed against how often verification actually needs to happen (likely: once per epoch, by validators, not something end users wait on).
- It's simple enough to implement, test, and reason about without depending on unaudited academic code.

**This should be labeled internally and externally as exactly what it is** — a sequential-hashing delay mechanism chosen for its quantum-resistance-compatible security assumptions, not marketed as "a VDF" in the way Chia or Ethereum research uses that term. Precision here matters; overclaiming a term with established academic meaning would be a credibility risk once real cryptographers look at the chain.

---

## 3. Phased build plan

Consistent with prior guidance: sequence the complexity, don't design and ship it all simultaneously.

### Phase 1 — Buildable now: deterministic epoch-based Top-K
- No VRF, no hash-chain beacon, no escrow yet.
- Validator set recomputed every epoch from the top-K addresses by recent mining output (tracked as real on-chain state, not inferred after the fact).
- Ships a working multi-validator devnet. Grindable at the margin — acceptable at this phase, since the goal is proving the epoch/`ValidatorUpdates` mechanics work at all.

### Phase 2 — Bonding & evidence
- Newly-mined rewards held in escrow for a cooldown period (sourced from mining output, not external capital — preserves fair-launch principles all the way through).
- Real evidence-submission mechanism for equivocation, adapted from the pattern in Cosmos SDK's `x/evidence` module rather than invented from scratch.
- This is a full subsystem in its own right and deserves its own design pass once Phase 1 is live and real block-timing data exists to inform it.

### Phase 3 — Grinding resistance via sequential-hashing beacon
- Implement the hash-chain epoch seed described in §2.
- Feed the resulting seed into sampling the final validator set from the Top-K pool.
- This is the phase that should get external cryptographic review before being considered production-ready — not because the mechanism is exotic, but because *any* consensus-critical randomness source deserves adversarial review before real value depends on it.

### Explicitly deferred, not forgotten
- "Loyalty scoring" (decay-weighted historical participation) — treat as a v2+ tuning parameter once Phase 1-3 are live and real usage patterns exist to calibrate against. Designing decay curves against a hypothetical network is guesswork; against a real one, it's an engineering task.
- Isogeny-based or other genuinely post-quantum VDF constructions — revisit if/when a mature, audited Go (or FFI-able) implementation exists. Track this as a live research question, not a closed one.

---

## 4. Open questions requiring explicit decisions before Phase 1 code

1. **Epoch length** — proposed starting point 1440 blocks (~24h at 60s blocks). Needs validation against expected mining participation patterns once real hash rate data exists.
2. **Top-K size** — proposed starting point K=21–31 (standard BFT-performance sweet spot across existing chains). Needs revisiting once real validator hardware/bandwidth assumptions are known.
3. **What counts as "recent mining output"** for Top-K ranking — raw block count in the epoch? Difficulty-weighted work? This affects Sybil resistance (many small miners vs. fewer high-output ones) and needs explicit specification, not left implicit.
4. **Minimum participation threshold** — should there be a floor below which an address can't qualify for Top-K at all, to reduce Sybil-spam attempts at gaming the ranking?

---

## 5. What "done" looks like for this design track

- [ ] Phase 1 epoch-based Top-K implemented, tested on devnet, `ValidatorUpdates` mechanics proven against real CometBFT epoch-transition delay (N+2 activation)
- [ ] Phase 2 evidence/escrow subsystem specified in its own design doc, informed by real Phase 1 devnet data
- [ ] Phase 3 hash-chain beacon implemented, clearly labeled as a sequential-hashing delay mechanism (not marketed as "VDF")
- [ ] External cryptographic review completed on Phase 2 + Phase 3 before any public testnet claims production-readiness
- [ ] Loyalty scoring and/or genuinely post-quantum VDF migration tracked as explicit, revisited research items — not silently dropped
