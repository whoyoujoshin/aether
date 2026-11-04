# Testnet bug hunt

Aether's mainnet plan opens with this: before `aether-rc-1` and `aether-1`,
the public testnet invites anyone to break it. The launch plan's first
phase ends after two weeks with no successful takeover and alerts that
were tested (`docs/MAINNET.md` §8). A bug found here costs nothing; the same
bug on mainnet costs real money.

Report privately (GitHub **Security → Report a vulnerability**, see
[SECURITY.md](../SECURITY.md)). The rules below apply to everyone taking
part.

**Not open yet.** Before it is announced:

1. Joshua turns on private vulnerability reporting (Settings → Code
   security → Private vulnerability reporting), which `SECURITY.md` relies
   on.
2. Gitty runs the module-account check on the seed ([REVIEW.md](REVIEW.md)
   §3). A missing module account would make F5 a way to halt the testnet,
   so it must come back clean first.
3. Joshua confirms the reward tiers below (proposed in REVIEW.md §4) and
   picks the start date. The two weeks start then.

## What to go after

The chain is stock Cosmos SDK v0.50, CometBFT v0.38 and ibc-go v8 around
code that is Aether's alone. That code is where to look
([REVIEW.md](REVIEW.md) §1 has the file list):

| Target | A win looks like | Read first |
|---|---|---|
| **Seats** (x/pow) | voting power beyond what your work earns (see "A takeover" below); a seat without 1% of the window's eligible work; an honest, online, signing validator removed | [SEAT-RULE.md](SEAT-RULE.md), [SEAT-SIM.md](SEAT-SIM.md) |
| **Signatures** (ML-DSA, `x/accountauth`) | value moved, or an account's key changed, without a valid ML-DSA signature by the key stored on it; a retired key accepted after a rotation | [PQ-KEY-ROTATION.md](PQ-KEY-ROTATION.md) |
| **Rewards and escrow** (x/pow, x/escrow) | AETH minted beyond the schedule; a bond released early or taken without cause; an escrow paid twice or to the wrong party | [ESCROW.md](ESCROW.md) |
| **IBC** (inflow cap, Helicase, Ligase) | a voucher's supply on Aether above its cap; a refund that mints more than was burned; anything but the five IBC relay messages through Helicase's unsigned path | [IBC-INFLOW-CAP.md](IBC-INFLOW-CAP.md), [HELICASE.md](HELICASE.md), [LIGASE.md](LIGASE.md) |
| **Governance and treasury** | a proposal passing without the votes; the treasury paying out without one; the treasury ledger above its bank balance | `x/governance`, `x/treasury` |
| **Consensus from outside** | a transaction or packet that halts or splits the chain; a node that computes a different app hash on the same blocks | [REVIEW.md](REVIEW.md) §2 (the replay) |
| **Services that hold keys** | the faucet, the hosted test wallet, the paywalls or the x402 facilitator tricked into paying, or into accepting a payment that wasn't made | [X402.md](X402.md), [TEST-WALLET.md](TEST-WALLET.md), [PAID-SERVICES.md](PAID-SERVICES.md) |

### A takeover

Under the seat rule, voting power follows proof-of-work: each address's
share of the work over the last three epochs, moving at most 10 points of
the total per epoch, with a 1% bar to be seated. **Out-mining everyone is the
rule working, not a bug**: with more than a third of the hash you earn more
than a third of the power, about four epochs later. A takeover is a
finding when it gets more than the work pays for:

- power above your share of the window's work (beyond the 10-point lag);
- a seat, or power kept, below the 1% bar;
- moving power faster than 10 points an epoch;
- getting an honest validator removed while it is online and signing;
- holding over a third (enough to halt) or over two thirds (control) with
  less than that share of the work.

Earning power by hashing is allowed. If you go over a third, **keep your
validator signing**: halting the testnet by stopping a validator you earned
is out of bounds (it proves nothing the rule doesn't already say).

## Getting started

| | |
|---|---|
| Chain | `aether-testnet-1` |
| RPC / gRPC | `https://rpc.157-245-252-221.sslip.io` / `grpc.157-245-252-221.sslip.io:443` |
| Explorer | `https://explorer.157-245-252-221.sslip.io` (validators, seat work, IBC, accounts) |
| Faucet | `https://faucet.157-245-252-221.sslip.io/request` (README, "Faucet") |
| Mining | the explorer's `/mine` page, the desktop app's Mine tab, or `cmd/powminer` ([MINE.md](MINE.md)) |
| Running a validator | README, "Registering as a validator"; [OPERATOR.md](OPERATOR.md) for the steps |
| Binaries | the latest `v*-testnet` release |

Most findings are cheaper to prove on your own devnet ([DEVNET.md](DEVNET.md)).
A chain ID starting `aether-rc-` opens every activation gate at block 1,
as mainnet will; the testnet's own heights apply on any other ID. The
fuzz targets, the invariants and the replay script are in
[REVIEW.md](REVIEW.md) §2.

## Rules

- Test on the testnet or your own devnet. Never touch funds that aren't
  yours, and give back testnet funds a bug let you take.
- **No denial of service by volume:** no flooding the RPC, the faucet, the
  test wallet or the paywalls, and no spamming blocks. A single
  transaction or packet that does disproportionate harm is in scope;
  send it once, then report.
- Nothing against the operators' machines, accounts or people: no
  scanning or attacking the servers, no social engineering, no phishing.
- Don't halt the testnet on purpose. If something you try might halt it,
  show it on a devnet and report that instead.
- The first report of an issue counts. Reports of the same root cause
  are one finding.
- Keep it private until the fix ships, or 90 days from the report.
- **Safe harbor:** research that keeps to these rules isn't pursued.

## Not eligible

- **Known findings:** F1 to F5 ([REVIEW.md](REVIEW.md) §3), including on the
  testnet below their activation heights: F2/F3 (malformed AuxPoW) and F5
  (module accounts) are fixed behind `AuxPoWInputGuardHeight` and
  `ModuleAccountsActivationHeight`, which the testnet turns on with its next
  validator swap.
- **Documented limits:** the known weaknesses in
  [SEAT-RULE.md](SEAT-RULE.md) and [SEAT-SIM.md](SEAT-SIM.md) (A4 to A6,
  hardened at block 460,800), the inflow cap's known limits
  ([IBC-INFLOW-CAP.md](IBC-INFLOW-CAP.md)), and rotation's
  ([PQ-KEY-ROTATION.md](PQ-KEY-ROTATION.md), "Limits and follow-ups").
- How the testnet is run rather than how the code works: four validators,
  two people, one of them on a home PC, test keyrings on some services.
  Mainnet's answers are in [MAINNET.md](MAINNET.md) §3.
- Bugs in the Cosmos SDK, CometBFT, ibc-go or other dependencies. Report
  those upstream; tell us too if Aether is affected, and we'll credit it.
- Rate limits, missing headers, or a page that looks wrong with no loss
  (the Low tier: credit only).

## Rewards

The bounty is small by design: credit for every valid report, and AETH
from the treasury once mainnet runs (proposed tiers, [REVIEW.md](REVIEW.md)
§4).

| Tier | Examples | Testnet hunt |
|---|---|---|
| Critical | value without a valid signature; past the inflow cap; halt or split the chain from outside; a takeover as defined above | credit, and up to 2,000 AETH on mainnet |
| High | funds frozen in escrow or the treasury; a key-holding service tricked into paying | credit, and up to 500 AETH on mainnet |
| Medium | every block slowed measurably for little cost; a public service crashed from outside | credit, and up to 100 AETH on mainnet |
| Low | wrong numbers in a query or the explorer, with no loss | credit |

Mainnet AETH is paid from the treasury by a treasury-spend proposal after
launch, so each payout is public and the validators approve it. Testnet
AETH and testnet USDC have no value and aren't a reward.

Credit is your name or handle in the release notes of the fix and in the
list below, unless you ask us to leave you out.

## Alerts

The phase ends only once the alerts are shown to fire. `cmd/minerwatch`
reports, as signed webhooks:

- `validator_new`: a consensus address not seen before gains voting
  power;
- `power_concentrated`: any validator holds more than a third, and
  `power_concentration_eased` when that ends;
- `chain_stalled`, `node_unreachable` and `signing_low`.

The testnet's seed runs it with its webhook to the operators' Discord.

**Staged takeover (devnet, 2026-11-04).** An `aether-rc-takeover` devnet
with 20-block epochs: two honest operators from `cmd/rcgenesis`, each
mining at a quarter of the time, and an attacker who joined later. The
attacker opened a miner account (1 AETH sent to it), registered its own
consensus key and mined flat out. All of it shared four cores with three
nodes, so the attacker's share of the window's work at each boundary came
out between 37% and 50%, not the two thirds intended.
minerwatch watched one honest operator's node with
`--concentration-above 0.3334 --new-validators`.

| Height | Attacker's power | minerwatch |
|---|---|---|
| 26 | 0 | attacker starts mining |
| 81 | 10.0% | `validator_new`: "validator 6F65714F, not seen before, joined the validator set with 10.0% of the voting power" |
| 101 | 17.8% | |
| 121 | 27.3% | |
| 141 | 37.3% | `power_concentrated`: "validator 6F65714F holds 37.3% of the voting power, over 33.3%" |
| 161 | 47.3% | |
| 181 | 39.9% | (its window share was 39.9% at that boundary) |

Power moved at most 10 points of the total per epoch, as the rule says,
and followed the work both ways: the newcomer entered at 10%, the honest
operator it overtook fell 74.9 → 64.9 → 54.9 → 44.9 → 35.6%, and when the
attacker's share of the work fell back, so did its power. At every boundary the
new powers were exactly what the rule computes from the window's work
(`aetherd q pow seat-work --height <boundary − 1>`, plus any find in the
boundary block itself). Each alert fired at the boundary
where the change took effect: `validator_new` three epochs before the
attacker could halt the chain alone, and `power_concentrated` as soon as it
could. At the testnet's 1,440-block epochs (about 2.4 hours) that is about
seven hours' warning before a newcomer with most of the hash can halt
the chain alone. The
script is `scripts/rc/staged-takeover.sh`.

## Credits

None yet.
