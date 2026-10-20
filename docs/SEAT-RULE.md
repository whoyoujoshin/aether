# The seat rule

**Status:** built. It switches on at block **396,000**
(`SeatRuleActivationHeight`, `x/pow/seats.go`), about 03:30 UTC on
22 October 2026 at 5.6-second blocks. The upgrade runbook is
`docs/UPGRADE-SEAT-RULE.md`. This is phase 0 of `docs/MAINNET.md`.

## Why

Under the current rule:
- A miner's work is the number of native submissions it got accepted.
- One accepted submission and a consensus key qualify it for a seat.
- With 21 or fewer qualified miners, every one is seated.
- Every seat has equal power.

With today's four miners, 9 accounts with one submission each in an epoch
(about 6% of the native hash) would hold 9 of 13 seats, over two thirds.
That is enough to sign headers that IBC light clients accept
(`docs/MAINNET.md` §1). Three accounts are enough to halt the chain.

## The rule

From the activation height, at each epoch boundary:

1. **Seat work is hashes proved.**
   - Every accepted submission adds the difficulty it had to meet to its
     miner's seat work for the epoch. A native submission adds the native
     difficulty recorded at the height it builds on.
   - An AuxPoW submission adds the AuxPoW difficulty, credited to the reward
     address it commits to, not the account that relayed it.
   - Both tracks measure difficulty in expected scrypt hashes (a hash must be
     below 2^256/difficulty on each), so they add up directly.
2. **The window is three epochs.** Seat work is summed over this epoch and
   the two before it (about 7 h at 6-second blocks). Work older than that is
   deleted.
3. **Qualifying.** An address needs a consensus key, no ban, and at least
   **1%** of all the window's work. The total includes work by addresses that
   don't qualify, so dust can't lower the bar.
4. **Seats and power.**
   - The top `TopKSize` (21) qualifying addresses by work are seated.
   - Each gets power in proportion to its share of the seated work,
     out of about 100,000,000 in total.
5. **Power moves at most 10% an epoch.**
   - Compare those target powers with the current ones, each as a share of
     the total. Count only increases: if they add up to more than 10%, every
     validator moves the same fraction of the way toward its target, so 10%
     moves in all.
   - Validators seated under the old rule count as holding equal power.
   - A validator that stops mining fades out at the same pace and leaves the
     set when its power rounds to zero.
6. **If nobody qualifies, the set stays as it is.**

The submission counts (`miner-leaderboard`, `minerwatch`) are kept as they
were. The rule doesn't use them.

## What it buys

| Attack | Old rule | Seat rule |
|---|---|---|
| Hold 2/3 of the power | ~6% of native hash for one epoch, split over 9 accounts | more than 2/3 of all hashes proved, for about 17 h (7 epochs) |
| Halt (hold 1/3) | ~2% of native hash, 3 accounts | more than 1/3 of all hashes, for about 10 h (4 epochs) |
| Split into accounts | gains seats | gains nothing; accounts under 1% or outside the top 21 lose their work |

**Arithmetic tests:** `x/pow/seats_internal_test.go`. They cover:
- 17 accounts hold exactly what one account with the same work would;
- dust doesn't qualify;
- a newcomer with 90% of the hash goes 10%, 20%, 30% and so on, passing
  1/3 in its fourth epoch and 2/3 in its seventh;
- pool-scale work doesn't overflow.

**Handler and epoch tests:** `x/pow/seats_test.go`.

The 10% limit also keeps at least 90% of the power in place between
epochs. IBC light clients (trust level 1/3) can then skip-verify across set
changes, which they couldn't when the whole set could change at once.

Until a pool merge-mines, the cost of a takeover is still the cost of
outmining the native miners, CPUs today. The rule makes that cost a real
majority, held long enough to be seen. Merged mining is what makes it
expensive. A pool's AuxPoW difficulty runs many orders of magnitude above
native difficulty, which is capped at `MaxDifficulty` (10⁸). Once a pool
merge-mines, it holds most of the power, and native miners fall under the
1% bar. That is the security model: seats follow the hash that protects
the chain.

## Left out on purpose

- **No cap on one validator's power.** With a cap, an honest miner's excess
  goes to everyone else, accounts an attacker split up included. One honest
  miner with 80% of the work and an attacker with 20% split eleven ways would
  leave the attacker with most of the power.
- **The beacon no longer picks seats.** Seats are the top 21 by work, so
  there is no draw to grind. The beacon still runs.
- **Governance stays one vote per validator,** weighted by tenure.

## Before activating it

- **Pools and escrow: settled by a bond floor** (`ValidatorBondFloor`,
  1,000 AETH, same height). Below the height a seated validator's rewards
  all stay in escrow while it is active (`AddEscrow`), so a seated pool
  couldn't pay its miners. From the height, a validator's rewards fill its
  escrow up to the floor and the rest is paid out at once
  (`escrowReward`). The floor stays locked while the validator is active,
  because its unlock height still moves forward with every reward. It is
  burned if the validator equivocates. A CPU validator reaches the floor in
  a few epochs, and a pool much faster. Raising the floor makes a seat cost
  more to misuse. Lowering it pays out sooner.
- **A pool that wants a seat** registers a consensus key for its reward
  address (`register-validator-pubkey`, signed by that address) and runs a
  validator for it.
- **The height** is 396,000, an epoch boundary. A node without a binary
  carrying it falls off the chain at the first submission accepted from
  then on, when seat work starts being recorded. The first boundary under
  the rule is the end of 397,439.
- **Watch the first boundary.** All four testnet validators are CPU miners,
  so their powers will follow their hash rates, moving at most 10% at the
  first boundary.
- **Miner status under the rule:**
  - **Where:** `get_miner_status` (agentmcp and the public `/mcp`),
    `/api/miner` and the `miners` service.
  - **What they report:** selectionRule `seat_work_power`, each address's
    seat work over the window and this epoch, rank and on-track by seat
    work, and its voting power from the validator set. Before the height
    they name the rule coming (`nextSelectionRule`,
    `nextRuleFromHeight`).
  - **How:** seat work is read from x/pow's store (a `/subspace` query of
    `seat_work/`), so no node needs a new query.
  - **minerwatch:** warns on no seat work this epoch, which counts merged
    work, and says the power falls rather than that the seat ends.
  - **Checked** on a devnet with the height patched to 40. At the first
    boundary under the rule, the validator's power went from 1,000,000 to
    100,000,000, and the status showed the same.
- **Follow-ups:**
  - a gRPC query for seat work;
  - a `minerwatch` alert when a newcomer's power grows;
  - the explorer's validator page showing power.
