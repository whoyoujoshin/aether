# Merged-mining pool trial (M4)

**Status:** plan. Concrete steps and open questions.
**Related:** `docs/MERGED-MINING-PLAN.md` (M4), `docs/SEAT-RULE.md`.

## Goal

One real Litecoin/Dogecoin AuxPoW pool points a share of its hash at
Aether on the testnet, gets paid, and (if it wants a seat) registers a
consensus key. The trial calibrates the seat-rule simulation's B2/B3
scenarios and shows whether a pool will run a validator.

## How a pool joins

1. The pool runs `cmd/auxpowd` beside its Litecoin node (or a public
   endpoint, if one is offered). `auxpowd` speaks the Namecoin/Dogecoin
   merged-mining RPC (`createauxblock` / `submitauxblock`).
2. The pool's stratum or job builder treats Aether as a third aux
   chain. The parent is still Litecoin. The coinbase commits to
   Aether's `aux_block_hash` at the nonce `auxpowd` returns.
3. When a Litecoin block (or a share that meets Aether's aux target)
   is found, the pool submits the standard `CAuxPow` serialization to
   `submitauxblock`. `auxpowd` checks it, signs `MsgSubmitPoW` with its
   own ML-DSA key, and broadcasts. The chain pays the reward address
   the template was bound to, not the bridge key.
4. Seat work is credited to that reward address, at the AuxPoW
   difficulty of the accepted submission (`creditSeatWork` in
   `x/pow/seats.go`).

### Pool software that supports a third aux chain

| Software | Status | Notes |
|---|---|---|
| yiimp (tpruvot) | Proven on a devnet (MERGED-MINING-PLAN M3) | Needed one local patch for Litecoin Core 0.21 `mweb` rule. Configures Aether as an aux coin pointed at `auxpowd`. |
| ckpool / ckdb | Not tested | Supports aux chains via its merged-mining config. Would need a trial. |
| Custom stratum (what M3 used first) | Proven | A small program that builds a Litecoin block with the commitment and submits both ways. |
| NiceHash / generic stratum | Unknown | Most public stratum endpoints do not expose a third-aux config. A pool would have to run its own. |

A pool that already merge-mines Dogecoin produces Litecoin parents.
Those parents are what Aether checks. No Dogecoin-specific work is
required.

## Reward address and seat work

- The template binds `reward_address`. The chain pays that address and
  credits its seat work, whoever submits the proof.
- To be **seated**, that address must register a consensus key
  (`register-validator-pubkey`, signed by the address) and run a
  validator for it. Work from an address with no key counts toward
  rewards but, from `SeatHardeningHeight`, does not count toward the
  1% bar (`eligibleTotal`).
- Before 460,800, unregistered work still inflates the denominator
  (A4). The trial should not open until the hardening is live, or the
  pool must register a key from the first proof.

## Bond floor and paying miners

- From `SeatRuleActivationHeight` (396,000), a seated validator's
  rewards fill an escrow up to `ValidatorBondFloor` (1,000 AETH) and
  the rest is paid out at once (`escrowReward` in `x/pow/seats.go`).
- A pool that is seated therefore:
  1. Earns rewards to its reward address.
  2. Has the first 1,000 AETH locked while it is active.
  3. Receives everything above the floor immediately, and can pay its
     miners from that.
- A pool that is **not** seated (no consensus key) is paid in full on
  every proof, with no escrow. It earns, but it does not sit in the
  set, and from the hardening its work does not affect the 1% bar.
- The floor is burned if the validator equivocates. The pool's
  operator should understand that before registering a key.
- Time to fill the floor: a CPU validator takes a few epochs; a pool
  with real aux hash fills it in the first proofs. After that, payouts
  are liquid.

## Concrete steps

1. **Wait for 460,800** (seat hardening) or have the pool register a
   key before its first proof. Do not run an unregistered pool against
   the pre-hardening rule.
2. **Stand up `auxpowd`** on a host that is not a validator. Point it
   at a testnet RPC/gRPC. Give it an ML-DSA key with enough AETH for
   fees (zero is the default; confirm the testnet still has zero min
   gas).
3. **Configure one pool.** Prefer yiimp, since M3 already ran it.
   Litecoin as the parent coin, Aether as aux, `auxpowd` as the aux
   daemon. Apply the `mweb` patch if the pool's Litecoin Core is 0.21.
4. **Mine.** Run `cpuminer` or the pool's own miners into the stratum.
   Confirm:
   - Litecoin (or regtest) accepts the parent blocks;
   - Aether accepts the aux proofs and pays the reward address;
   - native submissions still land (one native and one aux slot per
     block);
   - seat work for the reward address grows (`/api/miner` or a
     subspace query of `seat_work/`).
5. **Register a consensus key** for the reward address and run a
   validator. Confirm it enters the set at the next boundary, at a
   power proportional to its work, moving at most 10% per epoch.
6. **Pay miners.** After the bond floor fills, confirm the excess is
   paid out and can be split by the pool's normal payout logic.
7. **Record.** Hash contributed, proofs accepted, seat-work share,
   power at each boundary, any refused or stale proofs. Feed the
   numbers into the seat-rule simulation's B2 scenario.

## Open questions

- Will a real Litecoin/Dogecoin pool operator run a validator, or only
  take the reward? The seat model depends on the answer.
- What share of their hash will they point at Aether? The aux target
  is independent; they can point 100% or a fraction.
- Does yiimp (or ckpool) need further patches for Litecoin Core 0.21+
  beyond the `mweb` rule?
- Fee policy: if mainnet has a nonzero min gas price, who pays the
  `auxpowd` submission fee, and in which denom?
- Bond floor: is 1,000 AETH the right number for a pool, or should
  governance raise it before mainnet so a seated pool has more to
  lose on equivocation?
- Public `auxpowd` endpoint: offered or not? A public one is easier
  for small pools and a larger trust surface.
- Monitoring: who watches the pool's validator, and what happens if
  it goes offline for more than the 1,440-block liveness window?
