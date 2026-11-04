# Review before mainnet: scope, tools, findings, bounty

`docs/MAINNET.md` §5 replaces a full audit with three things: a focused
review of the code that is Aether's alone, the tools that review leans on,
and a public bug bounty from launch. This is where those meet: what to
review, how to run the tools, what they have found so far, and the bounty
as proposed.

## 1. Scope

Most of the chain is stock: Cosmos SDK v0.50.14, CometBFT v0.38.26 and
ibc-go v8.8.0, audited and run on many mainnets, and ML-DSA from
`cloudflare/circl`. The review covers what is Aether's:

| Code | What to look for |
|---|---|
| `x/pow` | seat selection (`docs/SEAT-RULE.md`), rewards and minting, the bond escrow and its burn, bans, the randomness beacon, AuxPoW parsing (untrusted bytes, `auxpow.go`) |
| `app/post_quantum.go`, `app/rotation_ante.go`, `x/accountauth` | any path that moves value without an ML-DSA signature from the account's stored key; rotation, guardians, watchtowers |
| `app/helicase.go` | the unsigned relay path admits only the five IBC message types, only through the proposer, only within the per-block limit |
| `app/ligase.go`, `x/escrow` | memo parsing, the two-strands rule, escrow expiry |
| `x/inflowcap`, `app/inflowcap.go` | every way to raise a voucher's supply past its cap, the in-flight records, the settlement backstop (`docs/IBC-INFLOW-CAP.md`, red team in PRs #171 and #172) |
| `x/governance`, `x/treasury`, `app/fees.go` | tenure-weighted power, quorum, treasury spends of any denom, the fee sweep |
| `app/mining_fee.go`, `app/tipping.go` | the fee-free path for a lone native mining find; mempool priority |
| `gates` | every height gate opens at block 1 on `aether-1` and `aether-rc-*`; nothing testnet-only leaks onto them |

Out of scope: the stock modules themselves, the testnet's own history, and
the off-chain services (explorer, agentmcp, paywalls, faucet), except where
one holds a key.

## 2. Tools

### Fuzzing

Go's built-in fuzzer, three targets:

| Target | What it feeds | Run for longer |
|---|---|---|
| `FuzzCheckAuxPow` (`x/pow`) | any bytes, decoded as an `AuxPowData`, through the whole AuxPoW check | `go test ./x/pow -run '^$' -fuzz '^FuzzCheckAuxPow$' -fuzztime 10m` |
| `FuzzAuxPowCommitment` (`x/pow`) | any bytes as a parent coinbase, through the parser the pool bridge also uses | `... -fuzz '^FuzzAuxPowCommitment$' ...` |
| `FuzzAetherMsgHandlers` (`app`) | any bytes, decoded as one of Aether's 42 message types, through its real handler on a fresh chain with every gate open; fails on a panic | `go test ./app -run '^$' -fuzz '^FuzzAetherMsgHandlers$' -fuzztime 10m` |

Plain `go test` runs only their seeds, so CI stays fast. A failing input is
saved under `testdata/fuzz/` and from then on runs as a seed: commit it
with the fix.

The handler target starts from an empty chain, so most messages stop early
(no account, no funds). It finds panics in input handling; it doesn't reach
deep states. Seeding it with messages from the app tests is the next step.

### Invariants

`app/invariants.go`: what must hold after every block, whatever its
transactions did. Each one only reads state.

| Invariant | Holds when |
|---|---|
| `bank-total-supply` | the bank's supply of each denom is the sum of every balance (the SDK's own check) |
| `pow-escrow-covered` | x/pow's module account holds at least every validator's escrowed rewards |
| `escrow-covered` | x/escrow's module account holds at least every open escrow, every denom |
| `treasury-ledger-backed` | the treasury's spend ledger is no higher than its uaeth bank balance |
| `governance-deposits-covered` | x/governance holds at least the deposits of every proposal in its deposit or voting period |
| `inflowcap-in-flight-totals` | each voucher's in-flight total equals the sum of its recorded sends |

Where they run:

- **In every app test.** The `app` package's tests set
  `AETHER_INVARIANTS_EVERY=1` and `AETHER_INVARIANTS_HALT=yes`, so every
  block of every scenario is checked. `TestInvariants_EachCatchesItsBreak`
  breaks each one on purpose and checks that only it fires.
- **On a node, when asked.** `AETHER_INVARIANTS_EVERY=N` checks at the end
  of every Nth block and logs what breaks; `AETHER_INVARIANTS_HALT=yes` also
  stops the node at that block. Off by default: on a big state the bank's
  check reads every balance. Whether it runs never changes a block's
  results, so a validator may run it, but a replay or devnet is the place.

The one the bank's supply leaves out, "supply equals minted minus burned",
needs counters the chain doesn't keep. Every mint is a block reward in
`x/pow` and every burn is a forfeited bond or a governance deposit, so the
review should read those three places.

### Replaying the testnet

`scripts/replay/replay-testnet.sh` syncs a fresh full node from the
testnet's genesis with one build, checking the invariants as it goes, and
stops at the first of:

- `RESULT invariant <h> ...`: an invariant broke at block h;
- `RESULT apphash <h> ...`: this build executed block h differently from
  the chain (nondeterminism, or a gate at the wrong height);
- `RESULT target <h>`: it reached `TARGET` with nothing broken.

At each height that adds a store it restarts the node once, as operators
did. Checked on a local devnet (2026-11-04): a replay to its target with
the invariants checked every block, a build broken on purpose at block 10
reported as `RESULT invariant 10: treasury-ledger-backed`, and a build
writing one extra value at block 8 reported as `RESULT apphash 8`. Not yet
run against the testnet's history: that needs a workstation or spare server
with the chain's ~6 GB of blocks, a peer that serves them from genesis, and
days of CPU. Never run it on a validator.

```bash
BIN=./aetherd PEERS=dfa6aae4b7bfd5b0eb1e22fabbae3e83a475b938@157.245.252.221:26656 \
  EVERY=100 bash scripts/replay/replay-testnet.sh
```

## 3. Findings

From the first fuzzing and invariant runs (2026-11-04).

| # | Finding | Severity | Fix |
|---|---|---|---|
| F1 | **A passed governance proposal whose message panics stops the chain.** `x/governance` runs a passed proposal's message in EndBlock, where no transaction's panic recovery applies. The SDK's own x/gov recovers there (`safeExecuteHandler`); Aether's didn't. Any message whose handler panics on its input would stop every node at that block. No such message is known to be reachable this way today (F2's AuxPoW path needs a proposal to commit to a block from the last 60, which it can't know days ahead), and it needs a passing vote, so it is a latent hazard, not an outside attack. | Medium | `safeExecute` in `x/governance/keeper.go`: the proposal lands as `EXECUTION_FAILED` and its writes are dropped. No height: a panic there never happened on the testnet (it would have halted), so history replays the same. |
| F2 | **Malformed AuxPoW input panics** (found by `FuzzCheckAuxPow` and `FuzzAuxPowCommitment` in their first second): a submission without a coinbase or chain branch dereferences nil, and a coinbase whose scriptSig length is 2^63 or more turns negative in `int` and slices out of range. Inside a transaction the SDK turns the panic into a failed transaction, so nothing halts by that path, but the pool bridge calls the same parser and would crash on a bad coinbase from a pool. | Medium | Refused with an error from `AuxPoWInputGuardHeight`: block 1 on fresh chains, a placeholder (50,000,000) on the testnet until a height is chosen, since the failure's code and gas change. The bridge always uses the strict parser. |
| F3 | **The AuxPoW coinbase branch had no length limit.** Every hash in it is hashed before the proof is refused: a 1 MB branch of one-byte hashes took about 0.1 s to check. At the mainnet price a block of four costs about 0.004 AETH and slows every validator by about 0.4 s. | Low | From the same height: at most 32 levels (its index is a uint32, so a longer branch names no position in any tree), and every branch hash 32 bytes. |
| F4 | **The pool bridge used the testnet's merged-mining height on every chain.** `cmd/auxpowd` checked proofs with `CheckAuxPow` (testnet heights) and refused work below 205,000. On `aether-1`, where merged mining is on from block 1, it would have handed out no work for the first 205,000 blocks (about 13 days) and checked proofs under the old byte order. | Medium (availability) | It reads the gate for the chain it talks to (`gates.Height(st.ChainID, …)`). |

No invariant broke in any app test scenario. The one test that tripped one,
`TestFeesToTreasury_CreditsDirectSends`, raises the treasury ledger with no
coins behind it on purpose, to check the sweep never lowers it; it now
turns the halt off for that step.

## 4. Bug bounty (proposed)

Joshua decides the amounts and the budget; this is the shape.

- **Where reports go:** GitHub's private vulnerability reporting on this
  repository (Security → Report a vulnerability), so a report and its fix
  stay private until released. Turn it on in the repository settings
  before launch, and add a `SECURITY.md` pointing to it.
- **Paid in USDC** on Aether or Injective, from a bounty fund held apart
  from the treasury.
- **Tiers** (proposed amounts, scaled to a small launch):

| Tier | Examples | Proposed |
|---|---|---|
| Critical | mint or move value without a valid signature; get past the inflow cap; split or halt the chain from outside; take a seat without the work the seat rule asks | up to 2,000 USDC |
| High | freeze funds in escrow or the treasury; halt the chain with a passing vote; a key-holding service tricked into paying | up to 500 USDC |
| Medium | slow every block measurably for little cost; crash a public service from outside | up to 150 USDC |
| Low | wrong numbers in a query or the explorer with no loss | up to 50 USDC |

- **Rules:** test on the testnet or your own devnet, never on mainnet
  funds that aren't yours; no denial of service by volume, no social
  engineering, nothing against operators' machines; first report of each
  issue is paid; disclosure after the fix ships, or 90 days.
- **Safe harbor:** research within these rules isn't pursued.

The findings above (F1 to F4) were found before the bounty and aren't
eligible.

## 5. Before launch

- Merge the guard and fixes; pick `AuxPoWInputGuardHeight` for the testnet
  with the next validator swap.
- Run each fuzz target for an hour on a workstation; commit any crasher.
- Run the replay against the testnet's history once.
- A reviewer goes through §1's table, with these tools.
- Open the bounty with launch.
