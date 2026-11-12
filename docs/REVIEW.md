# Review before mainnet: scope, tools, findings, bounty

`docs/MAINNET.md` §5 replaces a full audit with three things: a focused
review of the code that is Aether's alone, the tools that review leans on,
and a public bug bounty from launch. This is where those meet: what to
review, how to run the tools, what they have found so far, and the bounty.

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

Go's built-in fuzzer, four targets:

| Target | What it feeds | Run for longer |
|---|---|---|
| `FuzzCheckAuxPow` (`x/pow`) | any bytes, decoded as an `AuxPowData`, through the whole AuxPoW check | `go test ./x/pow -run '^$' -fuzz '^FuzzCheckAuxPow$' -fuzztime 10m` |
| `FuzzAuxPowCommitment` (`x/pow`) | any bytes as a parent coinbase, through the parser the pool bridge also uses | `... -fuzz '^FuzzAuxPowCommitment$' ...` |
| `FuzzAetherMsgHandlers` (`app`) | any bytes, decoded as one of Aether's 42 message types, through its real handler on a fresh chain with every gate open; fails on a panic | `go test ./app -run '^$' -fuzz '^FuzzAetherMsgHandlers$' -fuzztime 10m` |
| `FuzzMoneySequences` (`app`) | a program of up to 64 steps (sends, also to module accounts; escrows created, released, refunded; treasury-spend proposals of either denom; deposits; votes; block rewards; time moving on through the app's EndBlocker) on a chain whose four accounts hold money and are active validators; every invariant after every step, and no panic | `go test ./app -run '^$' -fuzz '^FuzzMoneySequences$' -fuzztime 10m` |

Plain `go test` runs only their seeds, so CI stays fast. A failing input is
saved under `testdata/fuzz/` and from then on runs as a seed: commit it
with the fix.

The handler target starts from an empty chain, so most messages stop early
(no account, no funds): it finds panics in input handling. The sequence
target reaches the states where money moves: its seeds each tell a story
(an escrow left to expire; a treasury spend voted through and paid; a
vetoed proposal; money sent straight to module accounts), and
`TestMoneySequences_SeedsReachRealStates` checks each seed does real work
rather than being refused, and that a voted spend pays its recipient
exactly. Not covered yet: rotations and guardians (they need real ML-DSA
signatures), IBC packets, and mining with real proof-of-work.

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
| F2 | **Malformed AuxPoW input panics** (found by `FuzzCheckAuxPow` and `FuzzAuxPowCommitment` in their first second): a submission without a coinbase or chain branch dereferences nil, and a coinbase whose scriptSig length is 2^63 or more turns negative in `int` and slices out of range. Inside a transaction the SDK turns the panic into a failed transaction, so nothing halts by that path, but the pool bridge calls the same parser and would crash on a bad coinbase from a pool. | Medium | Refused with an error from `AuxPoWInputGuardHeight`: block 1 on fresh chains, 712,080 on the testnet ([UPGRADE-INPUT-GUARDS.md](UPGRADE-INPUT-GUARDS.md)), since the failure's code and gas change. The bridge always uses the strict parser. |
| F3 | **The AuxPoW coinbase branch had no length limit.** Every hash in it is hashed before the proof is refused: a 1 MB branch of one-byte hashes took about 0.1 s to check. At the mainnet price a block of four costs about 0.004 AETH and slows every validator by about 0.4 s. | Low | From the same height: at most 32 levels (its index is a uint32, so a longer branch names no position in any tree), and every branch hash 32 bytes. |
| F4 | **The pool bridge used the testnet's merged-mining height on every chain.** `cmd/auxpowd` checked proofs with `CheckAuxPow` (testnet heights) and refused work below 205,000. On `aether-1`, where merged mining is on from block 1, it would have handed out no work for the first 205,000 blocks (about 13 days) and checked proofs under the old byte order. | Medium (availability) | It reads the gate for the chain it talks to (`gates.Height(st.ChainID, …)`). |

| F5 | **One plain send could stop a fresh chain for good** (found by `FuzzMoneySequences` within seconds). Bank blocks no addresses here, and a module account is created only the first time its module uses it: after a fresh genesis pow, treasury, governance, transfer and interchainaccounts don't exist. A send of 1 uaeth to the treasury's address leaves an ordinary account there; that send's own fee reaches the fee collector, and the fee sweep at the end of the block panics ("account is not a module account"), on every node, again on every restart. The same send to pow's address makes every mining reward fail; to governance's, every proposal. | Critical (fresh chains) | From `ModuleAccountsActivationHeight` every module account is created, or a squatted one converted in place keeping its coins (`app/module_accounts.go`, generalising what x/escrow already did at its activation). Block 1 on fresh chains, before any transaction; 712,080 on the testnet, where rewards, fees and vouchers created these accounts long ago (check below), so it changes nothing there. |
| F6 | **A new node couldn't state-sync** (found by Gitty, 2026-11-09, joining the Sydney full node). `New()` mounts the height-gated stores (authz/feegrant, IBC, accountauth, escrow, inflowcap) from the last height saved on disk; a node about to restore a snapshot has saved nothing, so it mounted none of them, and the restore refused the snapshot's first one: `cannot import into non-IAVL store "accountauth"`. With block sync from genesis also failing (F7), new testnet nodes could only join by copying another node's data. | High (availability) | A node with nothing saved and `[statesync] enable = true` mounts every store (`app/statesync.go`), so it takes any snapshot from after the last store-adding height (511,920 on the testnet). Not a consensus change: nodes with data plan as before. `TestStateSync_FreshNodeRestoresEveryStore` restores a snapshot carrying every one of those stores into a fresh node and checks the app hash; without the fix it aborts as Sydney did. Ships in `v0.2.21-testnet`; confirmed on the testnet 2026-11-12 (Gitty: a fresh node on the release's `aetherd` restored sync3's snapshot at 716,600 and was caught up about a minute later, block and app hashes matching sync3's at 716,601, 716,655 and 716,660). |
| F7 | **The testnet can't be replayed from genesis with today's build** (Gitty, 2026-11-09): block sync on `b9a5957` stops at block 2 with `wrong Block.Header.AppHash`. The early blocks were made by builds whose rules weren't all kept behind height gates. | Open (testnet only) | New testnet nodes join by state sync (F6). Since block 1's app hash passed, `InitChain` matched and it is executing block 1 that differs, so the genesis file is an unlikely cause; still, `testnet/genesis.json` isn't byte-identical to the live nodes' file (Gitty, 2026-11-12: the repo's lacks `tail_emission`, a field removed without effect in `e968e67`, and uses the SDK's newer layout), so a replay should use the live file. Not yet investigated further; `scripts/replay/replay-testnet.sh` is the tool. On `aether-1` every rule change must stay height-gated so the chain can always be replayed from genesis. |

No invariant broke in any app test scenario. The one test that tripped one,
`TestFeesToTreasury_CreditsDirectSends`, raises the treasury ledger with no
coins behind it on purpose, to check the sweep never lowers it; it now
turns the halt off for that step.

### Checking the testnet's module accounts (F5)

On the seed (or any node), each line should print `code 0` (it exists and
is a module account); anything else means its address can still be
squatted until `ModuleAccountsActivationHeight` is set and reached:

```bash
for m in fee_collector:0a0d6665655f636f6c6c6563746f72 pow:0a03706f77 treasury:0a087472656173757279 \
         governance:0a0a676f7665726e616e6365 transfer:0a087472616e73666572 \
         interchainaccounts:0a12696e746572636861696e6163636f756e7473 escrow:0a06657363726f77; do
  printf '%s: ' "${m%%:*}"
  curl -s "localhost:26657/abci_query?path=%22/cosmos.auth.v1beta1.Query/ModuleAccountByName%22&data=0x${m#*:}" |
    jq -r '"code \(.result.response.code) \(.result.response.log)"'
done
```

**Checked on the testnet (Gitty, 2026-11-05, the seed at block 609,151):**
all seven printed `code 0`, so no module account's address can be taken
over by a send there, and F5 can't halt the testnet. The testnet's
`ModuleAccountsActivationHeight` and `AuxPoWInputGuardHeight` are set to
712,080 together, one validator swap ([UPGRADE-INPUT-GUARDS.md](UPGRADE-INPUT-GUARDS.md)).

## 4. Bug bounty (small budget; tiers confirmed 2026-11-05)

Joshua confirmed the tiers on 2026-11-05. There is no budget for thousands of
dollars in cash, so the bounty pays mostly in credit and in AETH, and in
USDC only from a pool when there is one.

- **Where reports go:** GitHub's private vulnerability reporting on this
  repository (Security → Report a vulnerability), so a report and its fix
  stay private until released. `SECURITY.md` points to it; Joshua turned
  it on on 2026-11-05. It costs nothing.
- **Every valid report is credited** by name (or handle) in the release
  notes of the fix and on a hall-of-fame page, unless the reporter asks
  not to be.
- **AETH from the treasury.** The treasury fills itself from every block
  reward (2% of it, while validators take the rest of the 15% cut:
  `DistributeReward`) and from every fee (`app/fees.go`): at 5 AETH a block
  and ~15,000 blocks a day that is about 1,500 AETH a day, so a bounty paid
  from it costs no one cash. Each payout is a treasury-spend proposal, so it is
  public and validators approve it. The tiers, in AETH:

| Tier | Examples | Reward |
|---|---|---|
| Critical | mint or move value without a valid signature; get past the inflow cap; split or halt the chain from outside; take a seat without the work the seat rule asks | up to 2,000 AETH |
| High | freeze funds in escrow or the treasury; halt the chain with a passing vote; a key-holding service tricked into paying | up to 500 AETH |
| Medium | slow every block measurably for little cost; crash a public service from outside | up to 100 AETH |
| Low | wrong numbers in a query or the explorer with no loss | credit only |

- **USDC only from a pool, if one exists.** Should Joshua set some USDC
  aside later, it tops up Critical and High findings first, first come,
  until it runs out; the page says how much is left. Nothing is promised
  beyond what the pool holds.
- **A testnet bug hunt before mainnet** costs nothing and finds the same
  bugs earlier: the same tiers, paid in credit and in mainnet AETH from
  the treasury after launch. Written up as [BUG-HUNT.md](BUG-HUNT.md), with
  `SECURITY.md` pointing to GitHub's private reporting.
- **Rules:** test on the testnet or your own devnet, never on mainnet
  funds that aren't yours; no denial of service by volume, no social
  engineering, nothing against operators' machines; first report of each
  issue counts; disclosure after the fix ships, or 90 days.
- **Safe harbor:** research within these rules isn't pursued.

The findings above (F1 to F4) were found before the bounty and aren't
eligible.

## 5. Before launch

- ~~Pick `AuxPoWInputGuardHeight` and `ModuleAccountsActivationHeight` for
  the testnet~~ (712,080 after the module-account check above; the swap is
  [UPGRADE-INPUT-GUARDS.md](UPGRADE-INPUT-GUARDS.md)).
- ~~Run each fuzz target for an hour on a workstation; commit any crasher.~~
  Done 2026-11-10 at `9672a7b` (4 cores, two targets at a time with
  `-parallel 2`), no failures: `FuzzCheckAuxPow` 14.7 million inputs in
  1 h; `FuzzAetherMsgHandlers` 2.2 million in 1 h; `FuzzAuxPowCommitment`
  9.7 million in 57 min; `FuzzMoneySequences` 303,000 sequences in 57 min
  (every invariant checked after each step). The last two stopped 3
  minutes short at the session's background-task limit.
- Run the replay against the testnet's history once.
- A reviewer goes through §1's table, with these tools.
- Open the testnet bug hunt ([BUG-HUNT.md](BUG-HUNT.md)) once the tiers and
  start date are confirmed (private reporting on and the module accounts
  checked, 2026-11-05), and the bounty with launch.
