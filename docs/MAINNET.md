# Mainnet readiness (plan)

**Status:** plan, 2026-10-19. Nothing here is built. Each item names what has to
change, why, and the decisions that are Joshua's.

## Why mainnet

On a testnet, nothing an agent buys costs anything. Testnet USDC is free and
testnet AETH has no price, so every payment so far is a demonstration. Agents
will use Aether for real work only when the payments are real. The chain, the
paywall, x402, the directory and the clients are all ready for that.

## The bar

Bitcoin, Litecoin and Dogecoin launched without an audit. They survived early
bugs because, at first, nothing on them was worth much: the August 2010 bug
that created 184 billion BTC was patched and the chain went on. Aether can
use the same grace period if it launches the same way, **small and with little
connected value**, and grows only as each layer proves itself.

So this plan does not ask for a full audit before launch. It asks for three
things:

1. Fix what makes taking over the chain cheap (§1).
2. Cap what a takeover or a bug could steal while the chain is young (§2).
3. Review the code that is Aether's alone, and pay a bounty for what that
   review misses (§5).

Everything else can be done in stages after launch.

## 1. Validator seats (blocker)

### How seats work now

`x/pow` picks the validators once an epoch:

- **Work is a count.** An accepted native submission adds 1 to its miner's
  work for the epoch (`AddMiningWork`, `x/pow/msg_server.go`). Each block
  takes at most one native submission, and difficulty aims at one every 60 s.
  That is about 144 a 1,440-block epoch at 6-second blocks.
- **Anyone with any work qualifies:** one accepted submission and a
  registered consensus key are enough. There is no minimum work and no
  deposit. Banned miners are excluded.
- **Seats:** up to `TopKSize` = 21, drawn from the qualified miners by the
  beacon seed with weight by work, **without replacement**
  (`SampleValidatorsWithBeacon`). With 21 or fewer qualified miners, every one
  of them is seated.
- **Every seat has equal power** (`ValidatorVotingPower`, 1,000,000).
- **AuxPoW does not count toward seats.** It earns rewards and has its own
  difficulty, but `submitAuxPoW` deliberately never calls `AddMiningWork`.
  Merged mining therefore adds no protection to the validator set as built.

### Why that is cheap to take over

Today four miners hold the four seats. Someone with 17 accounts, each with
one accepted native submission in an epoch, makes 21 qualified miners. At the
next epoch boundary all 21 are seated, so the newcomer holds 17 of 21 seats,
about 81% of the power. That takes about 17 of the epoch's ~144 native
submissions, **roughly 12% of the native hash rate**, held for about 2.4 hours.
Fewer will do: 9 accounts (about 6%) already hold 9 of 13 seats, over two
thirds, and 3 are enough to halt the chain. No majority is needed. Once there are more than 21 candidates the weighted
draw still favours many small identities, because once the few large honest
miners are drawn the remaining seats go to whoever is left.

Native hash is cheap to begin with. The validators mine on CPUs, and scrypt
at N=1024 is far faster on a GPU. The native header (128 bytes,
`headerToBytes`) is not Litecoin's 80-byte header, so Litecoin ASICs can't
mine it unmodified. A GPU kernel is enough.

With more than two thirds of the power, an attacker can:
- halt or censor the chain;
- once tenure ramps up, steer governance and treasury spends (30 days, `TenureRampDuration`);
- **sign headers that Aether's IBC counterparties accept.** Osmosis's and
  Injective's light clients check only the signatures of two thirds of the
  power. A forged header can prove a packet that never happened, and so
  release whatever the counterparty holds in escrow for Aether's channel.

With more than a third, they can halt the chain.

Honest full nodes would refuse invalid state, and equivocation is banned and
its escrow burned. Neither protects light clients, and light clients are
where bridged money lives.

### What to change

No change to the seat rule makes seats *expensive* while native hash is
small. The changes below make a takeover need a real majority, and time.
Only the last makes it costly.

| | Change | Effect |
|---|---|---|
| a | **Power in proportion to work** over a trailing window (e.g. 3 epochs), instead of equal power per seat | Splitting into many accounts gains nothing. Control needs more than 2/3 of native work, a halt more than 1/3 |
| b | **Minimum work to qualify** (e.g. 1% of the window's work) | Dust accounts don't take seats or crowd the draw |
| c | **Entry limit:** newcomers together gain at most a set share of power per epoch (e.g. 10%) | A takeover takes several epochs and is visible. Add an alert for a newcomer's power to `minerwatch` |
| d | **Count merged-mined work toward seats**, weighted by difficulty and normalised between the two tracks | An attacker has to rent a real share of Litecoin and Dogecoin hash. This is the Dogecoin lesson (2014) |

**Built:** all four, as one rule behind `SeatRuleActivationHeight`. See
`docs/SEAT-RULE.md` for the details, the tests and what is left before
activating it. Both tracks measure difficulty in expected scrypt hashes, so
d needs no separate scaling: each submission counts the difficulty it met.
There is deliberately no cap on one validator's power, because a cap hands
an honest miner's excess to everyone else, sybils included.

**The testnet has the same weakness today.** Fixing it there is a consensus
change behind an activation height, and it is the place to prove the new rule:
run it for at least two weeks and invite outside miners to try to take seats,
with a bounty.

`docs/PQ-CHECKPOINTS.md` doesn't help here: checkpoints are signed by the
same set.

## 2. Limit what IBC can lose

A takeover or a bug can lose, at most, the value that IBC counterparties hold
in escrow for Aether. Keep that small while the chain is young:

- **One USDC path at launch.** Either Noble (Circle's issuance chain for Cosmos)
  or Injective. Pin it in `wallet` the way `TestnetUSDC` is pinned. Check the
  chosen chain's rules for opening a channel first.
- **An inflow cap on Aether's side.** Refuse a received packet that would take
  the USDC on Aether over a cap (refused packets are refunded on the sender's
  chain). That bounds the counterparty's escrow, so it bounds what a forged
  header could release. An example is the rate-limiting middleware in
  `cosmos/ibc-apps`; check it is compatible with ibc-go v8 first. Start the
  cap low and raise it by governance as the chain earns trust.
- **Osmosis later.** The AETH/OSMO pool and the swap contract come after the
  USDC path has run with its cap for a while.
- **Light-client upkeep stays an operations duty.** A light client that
  expires stops the path. That happened to Osmosis's client of Aether on
  testnet. `ibcwatch` is ready; on mainnet its alerts go to someone on call.

## 3. Validators and keys

- **Independent operators.** BFT tolerates fewer than a third faulty, so seven
  operators survive two failures. Today two people run four nodes. Mainnet
  needs operators who don't share a person, a host or a network.
- **No validator on a home PC.** peer-1 was voted out twice because of a VPN
  on DardenPC. Validators run on servers with sentry nodes in front.
- **Consensus keys in a remote signer** (tmkms, or horcrux for a threshold
  key), with double-sign protection. The rule stays: one key, one place.
- **Service keys leave the validators.** On the testnet seed, the faucet,
  sandbox funder, service payee and decoy keys are all in `keyring-backend
  test`, which is unencrypted on disk. Mainnet money needs an encrypted
  keyring on a machine that is not a validator. "Never build on a validator"
  stays too.

## 4. The mainnet genesis and build

- **Activation heights.** Every gate is a compile-time constant set to a
  testnet height: the submission cap (51,007), IBC and accountauth (122,000),
  escrow, Helicase and Ligase (161,000), ASERT, the beacon and merged-mining
  rules (205,000). A fresh chain built from today's code would run the old
  rules until those heights. Mainnet needs the gates chosen by chain ID (all at
  height 1 on mainnet, unchanged on `aether-testnet-1`), with tests that run
  both. Then run a full devnet from genesis with the mainnet gates.
- **Parameters to review:**
  - `EpochLength` 1,440. Its comment says about 24 h, which assumed 60-second
    blocks; it is about 2.4 h at 6 s.
  - `TopKSize` 21.
  - `BondCooldown` 4,320.
  - The native and AuxPoW difficulty bounds: `MaxDifficulty` is 10⁸ expected
    hashes, under a second for one GPU.
  - The block-time target.
- **Supply.** `docs/genesis.template.json` gives no one a balance: every AETH
  is mined. The reward is 5 AETH a submission in year 1, kept at 66% each year
  for 8 years, then 0.20 AETH for good. Of each reward, 15% is a cut: 13/15 of
  it goes to active validators and 2/15 to the treasury. Keep the fair launch;
  it is the simplest story and the cleanest legally.
- **Fees.**
  - **Minimum gas price.** The testnet's is zero; mainnet needs one to stop
    spam.
  - **Accept USDC for gas.** Validators can list USDC in
    `minimum-gas-prices` (a fee in any listed denom passes), so an agent
    holding only USDC can still pay its gas.
  - **Where fees go.** They land in the fee collector, which nothing spends;
    route them to the treasury or the validators.
- **Chain ID:** `aether-1`.

## 5. Review instead of audit

Most of the chain is stock Cosmos SDK, CometBFT and ibc-go, which have been
audited and run on mainnets for years. ML-DSA comes from `cloudflare/circl`.
The review covers what is Aether's alone:

| Code | What to look for |
|---|---|
| `x/pow` | seat selection (§1), rewards and minting, escrow and burn, bans, the beacon, AuxPoW parsing (untrusted bytes) |
| `app/post_quantum.go`, `x/accountauth` | any path that moves value without an ML-DSA signature |
| `app/helicase.go` | that the unsigned relay path admits only the five IBC message types, only through the proposer, and only within the per-block limit |
| `app/ligase.go`, `x/escrow` | memo parsing, the two-strands rule, escrow expiry |
| `x/governance`, `x/treasury` | tenure-weighted power, quorum, treasury spends |

Method:
- Fuzz the message handlers and the AuxPoW parser.
- Add invariants: supply equals minted minus burned; escrow balances match
  the module account.
- Replay the testnet from genesis to catch nondeterminism.
- Run a public bug bounty from launch, paid in USDC, with the top tier
  reserved for consensus and IBC.

## 6. Services and clients

- **The clients default to the testnet.** They need a mainnet profile: chain
  ID, the USDC path and denom, endpoints. That applies to agentmcp, the
  TypeScript and Python clients, the explorer and the MCP Registry entry.
  `docs/API-STABILITY.md` applies to both networks.
- **The paid services earn and spend real money.**
  - Inference pays OpenRouter in dollars and is paid in USDC, so its price
    must cover its cost. The daily cap stays.
  - The payee is a real wallet (§3).
- **No faucet and no free test wallets** on mainnet. A small promotional
  budget is a separate decision. The benchmark, the quest, the decoys and the
  hosted test wallet stay on the testnet.
- **Mandates, receipts and reputation carry over unchanged.** Reputation
  counts payments only on its own network.

## 7. Legal

A token that trades carries legal risk a testnet doesn't. Before launch, take
an hour with a lawyer who knows crypto. Ask about the token's status where
Joshua lives, the hosted services that take payments, and what not to say in
announcements. A fair launch with no sale and no premine is the strongest
position, and the plan keeps it.

## 8. Launch order

| Phase | Where | What | Exit when |
|---|---|---|---|
| 0 | testnet | the seat fix (§1) behind an activation height; invite takeover attempts | two weeks with no successful takeover; alerts tested |
| 1 | `aether-rc-1`, thrown away after | mainnet build, gates from height 1, outside operators, reproducible binaries, genesis rehearsal | a clean launch and a week of running |
| 2 | `aether-1` | mining, AETH transfers, services priced in AETH; IBC closed | a few weeks without a consensus bug |
| 3 | `aether-1` | the one USDC channel with its inflow cap; services in USDC | the cap raised by governance in steps |
| 4 | `aether-1` | Osmosis channel, the AETH/OSMO pool, more counterparties | — |

In phase 2 a bug costs only AETH. That is the grace period the early chains
had.

## Decisions for Joshua

| Decision | Options | Recommendation |
|---|---|---|
| Seat rule | a + b + c, or a + b + c + d | a + b + c at launch; decide on d after asking the large Litecoin and Dogecoin pools whether they would run validators |
| Supply | fair launch (as now) or an allocation | fair launch |
| First USDC path | Noble or Injective | whichever chain lets Aether open a channel sooner; check both |
| Inflow cap | an amount | low at launch, e.g. 10,000 USDC, raised by governance |
| Fees | a minimum price, USDC accepted, where fees go | accept USDC; fees to the treasury |
| Operators | who, and how many | seven or more before phase 2 |
| Legal | — | one consultation before phase 2 |
