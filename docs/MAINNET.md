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

- **One USDC path at launch: Injective.** Noble is out: Circle stopped
  minting USDC there on 13 October 2026 and pauses its contract on
  12 January 2027. Injective's USDC is pinned in `wallet`
  (`InjectiveMainnetUSDCBaseDenom`, `MainnetUSDC(channel)`); check
  Injective's rules for opening a channel first. Genesis lists no inflow
  entry: the channel is listed by governance once it's open and checked,
  since channel IDs are first come, first served (`docs/IBC-INFLOW-CAP.md`,
  Listing on aether-1). That takes one voting period, 7 days by default.
- **An inflow cap on Aether's side.** Refuse a received packet that would take
  the USDC on Aether over a cap (refused packets are refunded on the sender's
  chain). That bounds the counterparty's escrow, so it bounds what a forged
  header could release. The design is `docs/IBC-INFLOW-CAP.md` and the code
  is `x/inflowcap`: a small Aether
  middleware with a fixed cap on the voucher's supply, failing closed (the
  `cosmos/ibc-apps` rate limiter takes only percentages and can't be set
  while supply is 0). The cap is chain-wide (all holders together, not per
  person) and is **5,000 USDC** at launch (decided 2026-10-22), raised by
  governance as the chain earns trust.
- **The launch pool.** Joshua plans to put 500 USDC of his own into a
  pool at launch to give AETH a market. On Osmosis that USDC never comes
  onto Aether, so it doesn't count against the cap; only AETH crosses, and
  AETH leaving isn't capped. That would mean opening the Osmosis channel
  earlier than phase 4 (still to decide).
- **Osmosis later.** The AETH/OSMO pool and the swap contract come after the
  USDC path has run with its cap for a while.
- **Light-client upkeep stays an operations duty.** A light client that
  expires stops the path. That happened to Osmosis's client of Aether on
  testnet. `ibcwatch` is ready; on mainnet its alerts go to someone on call.

## 3. Validators and keys

- **Independent operators.** BFT tolerates fewer than a third faulty, so seven
  operators survive two failures. Today two people run four nodes. Mainnet
  needs operators who don't share a person, a host or a network.
- **What an operator is (draft for Joshua).** Seats on Aether aren't handed
  out: under the seat rule a validator holds power only while its miner proves
  at least 1% of the window's work. So an operator is a miner and a validator
  together, and the launch can't invite seven; it can only make seven able
  to join. A proposed definition:
  - **One operator, one controller.** A person or organization whose
    validator and miner no other operator controls, pays for or can reach:
    no shared people, keys, cloud account or home network.
  - **Spread out.** No two operators in the same data center, and no more
    than a third of the power on one cloud provider.
  - **Mines its own seat.** Holds at least 1% of the window's work from its
    own hash or a pool it runs (native work only on `aether-1` for now:
    merged work earns its reward but no seat, `FreshMergedSeatWorkHeight`).
  - **Runs it properly.** A server, not a home PC; a sentry node in front;
    the consensus key in a remote signer with double-sign protection; the
    5-second `timeout_commit`; alerts that reach a person.
  - **Is reachable.** A contact who can swap a binary before an upgrade
    height, and who has done one on `aether-rc-1` (phase 1).
  - **Launch bar:** seven operators meeting all of this, with no one of them
    above a third of the power, before phase 2. The seat rule has no
    per-validator cap on purpose, so this is a launch condition to watch,
    not a rule the chain enforces.
- **The operator kit: built** (`docs/OPERATOR.md`, 2026-11-01). The guide
  from nothing to a seat; `cmd/rcgenesis`, which builds the genesis from
  each operator's name, consensus key and miner address (miner accounts at
  zero balance, every operator at equal power); and
  `scripts/rc/build-aetherd.sh`, a reproducible build whose sha256 every
  operator compares. Rehearsed with two operators on an `aether-rc-ops`
  devnet: the first operator mined from an empty account, registered its
  key from its first reward and took its genesis seat over.
- **No validator on a home PC.** peer-1 was voted out twice because of a VPN
  on DardenPC. Validators run on servers with sentry nodes in front.
- **Consensus keys in a remote signer** (tmkms, or horcrux for a threshold
  key), with double-sign protection. The rule stays: one key, one place.
- **Service keys leave the validators.** On the testnet seed, the faucet,
  sandbox funder, service payee and decoy keys are all in `keyring-backend
  test`, which is unencrypted on disk. Mainnet money needs an encrypted
  keyring on a machine that is not a validator. "Never build on a validator"
  stays too. **Built (AET-26):** services open a `file` keyring
  unattended from a passphrase file (`AETHER_KEYRING_PASSPHRASE_FILE`, a
  systemd encrypted credential), `wallet copy-key` moves ML-DSA keys into
  one (the SDK's `keys export` can't), and `docs/SERVICE-KEYS.md` moves the
  testnet seed's keys: the hot ones encrypted, the payee off the server.

## 4. The mainnet genesis and build

- **Activation heights: built** (package `gates`).
  - **Where the gates open:** every gate is a testnet height, such as the
    submission cap (51,007), IBC and accountauth (122,000), escrow,
    Helicase and Ligase (161,000), ASERT, the beacon and merged mining
    (205,000), and the seat rule (396,000). Each is now read through
    `gates.Height(chainID, h)`.
  - **On a fresh chain** every gate opens at block 1. A fresh chain is
    `aether-1`, or any chain ID starting `aether-rc-`, such as
    `aether-rc-1` or a devnet named `aether-rc-dev`.
  - **On every other chain ID** the testnet heights apply: on
    `aether-testnet-1`, on other devnets, and in unit tests.
  - **Two changes apply on fresh chains only:**
    - A module the genesis file leaves out starts from its defaults. On
      the testnet, IBC's genesis ran at its activation block instead.
    - A miner that registers a genesis validator's key takes that
      validator over, so one key never holds two seats.
  - **Tests:** `app/fresh_chain_test.go`. A fresh chain wires every module
    from the first start, has every store in block 1, keeps IBC's params,
    and keeps committing after a restart. The testnet's chain ID wires
    none of them on a fresh database.
  - **Devnet:** a run on `aether-rc-dev` with the real build had IBC and
    ICA params from genesis. The seat rule held the validator at
    100,000,000 from the first boundary.
- **Parameters to review:**
  - `EpochLength` 1,440. Its comment says about 24 h, which assumed 60-second
    blocks; it is about 2.4 h at 6 s.
  - `TopKSize` 21.
  - `BondCooldown` 4,320.
  - The native and AuxPoW difficulty bounds: `MaxDifficulty` is 10⁸ expected
    hashes, under a second for one GPU.
  - The block-time target.
  - **The reward schedule ran about ten times too fast: fixed for fresh
    chains (found 2026-11-02, built 2026-11-03; Joshua chose time).**
    `ComputeScheduledBlockReward` counted years in block heights,
    `SecondsPerYear / TargetBlockTime` = 525,960 blocks, which assumes a
    block every 60 s. Blocks come every 5.7 s, so each "year" lasted about
    35 days and the eight steps from 5 AETH to the 0.20 AETH tail about
    nine months. (`TargetBlockTime` is really the target time between
    accepted submissions, which ASERT holds at one a minute; the schedule
    borrowed it as a block time.) On `aether-1` and `aether-rc-*` a year is
    now 365.25 days of block time after the genesis time
    (`x/pow/reward_schedule.go`): InitGenesis records the genesis time
    (or the genesis file's `reward_schedule_start`, so an exported chain
    keeps its start), and the export writes it. Block time is consensus
    data, the power-weighted median of the last commit's vote times, so
    every node agrees and moving it takes a third of the power. The
    testnet keeps the height count: its second step falls at height
    525,960, around 2026-10-30 (3.3 AETH a find instead of 5), unless its
    genesis stored a fixed reward. Tests: `app/reward_schedule_test.go`
    (on both fresh chain IDs, 600,000 blocks and a day after genesis is
    still 5 AETH; a year after, 3.3; eight, the tail; the testnet's chain
    ID unchanged) and `x/pow/reward_schedule_test.go` (a start from the
    genesis file is kept and exported); both fail with the change undone.
  - **The escrow lock outlasts the evidence window: set by `rcgenesis`.**
    `BondCooldown`'s 4,320 blocks were meant as 3 days (48 hours of
    evidence plus a day) but are about 7 hours at 5.7 s, so a validator
    that double-signed could take its escrow out before the evidence
    arrived. `cmd/rcgenesis` writes `bond_cooldown` 43,200 (3 days at 6 s,
    60 hours even at 5 s) and evidence `max_age_num_blocks` 28,800 with
    `max_age_duration` 48 h; CometBFT drops evidence only once it is past
    both, so the window stays 48 hours. The testnet's values are
    unchanged.
- **Supply.** `docs/genesis.template.json` gives no one a balance: every AETH
  is mined. The reward is 5 AETH a submission in year 1, kept at 66% each year
  for 8 years, then 0.20 AETH for good. Of each reward, 15% is a cut: 13/15 of
  it goes to active validators and 2/15 to the treasury. Keep the fair launch;
  it is the simplest story and the cleanest legally.
- **Fees.**
  - **Minimum gas price.** The testnet's is zero; mainnet needs one to stop
    spam.
  - **Accept USDC for gas.** No consensus code: each validator lists USDC
    in `minimum-gas-prices` next to AETH, and a fee passes if it covers any
    one listed denom. For example `0.0001uaeth,0.0001ibc/<USDC voucher>`
    asks 40 of either for a 400,000-gas transaction. The voucher denom is
    known once governance lists the Injective channel
    (`docs/IBC-INFLOW-CAP.md`, Listing on aether-1). Checked on an
    `aether-rc-dev` devnet: with both denoms listed, a fee of 50 in a
    voucher passed, 30 was refused ("insufficient fees … required: 40"),
    and 40uaeth passed.
  - **Where fees go: built** (`app/fees.go`, `FeesToTreasuryActivationHeight`).
    At the end of every block everything the fee collector holds, in every
    denom, moves to the treasury module account; the uaeth part is credited
    to the treasury's ledger, so governance can spend it. A
    `fees_to_treasury` event carries the amount. Uaeth sent straight to
    the treasury's address (which the ledger never saw, so governance
    couldn't spend it) is credited the same way, with a
    `treasury_ledger_credited` event; the ledger is only raised. The treasury's
    `Balance` query lists every denom it holds (`real_bank_balances`). On
    from block 1 on fresh chains; at 511,920 on the
    testnet, where the fee collector holds 0.016374 AETH from since genesis
    that the first swept block moves. It adds no store, but it changes
    every block, so validators need a binary carrying the height first. It
    is on the review's list.
  - **The price itself: decided 2026-11-03 (Joshua): 0.0001uaeth, plus
    0.0005 per gas in the USDC voucher once a USDC path opens, with
    tipping** (node configuration, so it can change without a release:
    `minimum-gas-prices = "0.0001uaeth,0.0005ibc/<USDC voucher>"`).
    It was 0.0005 before tipping; with tipping the minimum no longer has
    to keep spam out on its own, so it can be as low as gas is meant to
    be (Joshua, 2026-11-03: as minimal as possible, for agents, bots and
    people). Measured on an
    `aether-rc-gas` devnet (2026-11-02), gas used and size of each
    transaction:

    | Transaction | Gas used | Bytes |
    |---|---|---|
    | Send, account's first (stores its key) | 199,209–214,404 | 3,973–3,979 |
    | Send to a new account | 169,027 | 3,981 |
    | Send to an existing account (a directory listing is one) | 156,934 | 4,178 with a 200-byte memo |
    | Multi-send to 10 | 390,314 | 4,759 |
    | Escrow create / release | 178,481 / 149,936 | 4,016 / 3,901 |
    | Treasury-spend proposal / vote | 193,373 / 137,716 | 3,993 / 3,898 |
    | Register a consensus key | 163,632 | |
    | Mining submission (no fee at any price) | 287,331 | |

    A fee is checked against the gas **limit**, not the gas used, and the
    CLI, faucet, listings and agentmcp all ask 400,000. At 0.0005uaeth:
    - a send costs 200 uaeth (0.0002 AETH) at the default limit, or about
      85 with `--gas auto`; one find's 4.25 AETH pays for about 21,000;
    - registering a consensus key costs 200 uaeth, as on the rehearsal;
    - in USDC at 0.0025, a send at the default limit costs 1,000 base units
      ($0.001).

    At the proposed 0.0001uaeth, a fifth of all that: a send costs 40 uaeth
    (0.00004 AETH) at the default limit or about 17 with `--gas auto`, one
    find pays for about 106,000, and in USDC at 0.0005 a send costs 200
    base units ($0.0002). Filling every 4 MB block at the minimum would
    cost about 10 AETH an hour; anyone who tips goes ahead of it.

    **Spam.** Every transaction is about 4 KB (an ML-DSA-44 signature is
    2,420 bytes and a public key 1,312), and the genesis template leaves
    `max_gas` unlimited (`-1`) with `max_bytes` at 22,020,096, so a block
    holds about 5,600 sends. Filling every block at 0.0005uaeth costs
    about 0.44 AETH a block, about 280 AETH an hour at 5.7 s blocks: close
    to the whole year-1 native issuance (one 5 AETH reward a minute, 300
    an hour). At 0.0001 it would be 56 AETH an hour, a fifth of it; at
    0.001, 560. In USDC at 0.0025 it is about $2.20 a block. The mempool
    is first come, first served (no fee priority), so a spammer who fills
    blocks delays everyone rather than being outbid. **Tipping changes
    that (built 2026-11-03, below).**
  - **Tipping: built (`app/tipping.go`, 2026-11-03).** Paying more than
    the minimum price is a tip, like Solana's priority fee or Ethereum's
    tip: when blocks are full, the higher fee per gas goes first; when
    they aren't, the minimum is enough. Fees, tips included, go to the
    treasury. aetherd runs the SDK's priority mempool on every node, and a
    transaction's priority is its fee per gas as a multiple of the node's
    minimum for that denom (the SDK's own priority is an integer price,
    0 for anything under 1 uaeth a gas, so it couldn't see a tip). A lone
    native mining find with valid proof-of-work goes first, so spam can't
    hold it past its 60-block recency window. It is the proposer's choice,
    not a block rule, so mixed binaries agree. To tip, pay more: `--fees
    80uaeth` on a 400,000-gas transaction is twice a 0.0001 minimum.
    - **Tests:** `app/tipping_test.go` (sends paying the minimum arrive
      first, then a send paying 10x, then a fee-free find; a proposal with
      room for two takes the find and the tip, and with room for all puts
      them first; the block accepts all seven). It fails with the SDK's
      mempool, with the SDK's integer priority, and with the find at the
      bottom. `app/tipping_internal_test.go` covers the priority in AETH
      and USDC and the mempool size.
    - **Devnet (`aether-rc-tip`, real aetherd, 0.0001uaeth, 64 KB blocks
      of 15 sends):** 60 sends paying the minimum went in first, then one
      paying 10x. The tipped send was the first transaction of the next
      block (646); the 60 took four blocks (646 to 650).
    - **Limit:** CometBFT's own mempool refuses everything while it is
      full, tip or not (0.38 can't evict by fee). Operators give it several
      blocks' worth (`docs/OPERATOR.md`: `size = 20000`). A spammer who
      keeps even that full pays for every transaction that lands, at the
      minimum, the same as filling blocks.
    - **Not built:** a fee that rises with congestion (Ethereum's base fee,
      Skip's x/feemarket), which raises prices just when they should stay
      flat; and paying tips to the block's proposer (Ethereum, Solana),
      which changes where fees go, a consensus change.
  - **Every tool pays it (built 2026-11-03).** aetherd serves
    `cosmos.base.node.v1beta1` Config (the node's `minimum-gas-prices`), and
    every tool that sends asks its node for the price and pays it on its
    gas limit, rounded up (`wallet.FeeSetting`, `--gas-price auto` by
    default, `--tip` to pay a multiple): agentmcp (payments, escrows, pull
    grants, the test wallet), the paywall (listings, prepaid payouts, pull
    collections), the `wallet` CLI (sends, rotations), the faucet, auxpowd
    and the desktop wallet; the TypeScript and Python clients
    (`AetherClient` `gasPrice`/`tip`, `gas_price`/`tip`) for sends, sellers
    and x402 buyers. An x402 exact payment's fee is in uaeth, the
    facilitator's `feeDenom`. A node that doesn't serve the query (the
    testnet's, until they run this build) is taken to ask nothing, which is
    what they ask. Native mining submissions stay fee-free. The fee rule is
    shared (`clients/testdata/fees.json`, checked in Go, TS and Python).
    One bug only a fee shows, fixed with it: a transaction's first `transfer`
    event is its fee to the fee collector, so `wallet`'s transaction summary
    (and `get_transaction_status`, receipts, the benchmark's scorer) took
    the fee for the payment. The summary now skips the fee collector and
    reports `fee` apart. Checked on an `aether-rc-devfees` devnet at
    0.0001uaeth: a zero-fee send was refused (`required: 40uaeth`), the
    default send paid 40 uaeth and `--tip 3` paid 120, directory listings
    paid the minimum, and the payments benchmark ran its whole stack on it
    (`bench/devnet.sh`): the reference agent 13 of 13 and the careless one
    5.5 of 13, the same as at zero fees, with 720 and 1,000 uaeth paid in
    fees. The scorer now leaves fees out of what an agent spent.
  - **Block limits: decided 2026-11-03 (Joshua), written by `rcgenesis`.**
    22 MB every 5.7 s is about 3.7 MB/s to every peer, more than a home
    link and more than the network needs: the testnet has never come near
    it. `max_bytes` 4,194,304 (about 1,000 sends a block, 175 a second) and
    `max_gas` 200,000,000 (Helicase's relays may use up to half). Filling
    those blocks at the 0.0001uaeth minimum costs about 10 AETH an hour,
    and anyone who tips goes ahead of it, while each block stays small
    enough for an ordinary server. A chain booted from an `rcgenesis` file
    reports them (`consensus_params`: `max_bytes` 4194304, `max_gas`
    200000000). Governance can change them later with an x/consensus
    param change.
  - **Governance spends the treasury's USDC** (AET-27): a treasury-spend
    proposal's amount may be one coin of any denom the treasury holds,
    paid from its bank balance; uaeth still goes through the ledger. On
    from block 1 on fresh chains, at the fees height on the testnet
    (`TreasuryCoinSpendActivationHeight`). No new message type or field.
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

The fuzz targets, the invariants, the replay script, the findings so far
and the proposed bounty are in `docs/REVIEW.md`.

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

## Opening a new miner's account (decided 2026-11-01: the opener)

A new account exists on chain only once it receives funds, and a
transaction can't be signed by an account that doesn't exist (the ante
chain needs its number and sequence). On the testnet the faucet's first
drip opens it; `aether-1` has no faucet. So a brand-new browser miner
couldn't send its first find even though mining submissions need no fee
(`app/mining_fee.go`).

**The opener** (Joshua's call): a courtesy of 0.001 AETH to each new
miner, enough to open the account and pay for its first few ordinary
transactions. It is the faucet run with `--opener` (`cmd/faucet`):

- it funds an address **once, only while the chain has no account for
  it**, and refuses one that has (`account_exists`), so it can't be
  farmed by asking again;
- its amount defaults to 1,000 uaeth (0.001 AETH) unless `--amount` says
  otherwise;
- it keeps the faucet's proof-of-work challenge and per-visitor limits,
  and the Mine page's "Activate" step calls it exactly as it calls the
  testnet faucet.

**Funding it through the treasury** (Joshua's idea): after launch, Joshua
and Gitty send part of what they mine to the treasury, and governance
pays the opener's address from it with treasury-spend proposals. The
treasury also fills by itself from block 1 (2% of every block reward and
every fee), so it can keep the opener going once mining picks up. Every
refill is a public proposal, so anyone can see what the opener holds and
where it came from.

- **Sizing:** 10 AETH opens about 10,000 accounts, plus the opener's own
  fees (about 0.0001 AETH a send at the planned price). A refill every
  few weeks, sized from the opener's `/stats`, keeps it from holding much.
- **Timing:** each refill waits one voting period (7 days by default), so
  ask before it runs low. The opener's balance is on its `/` page.
- **Key:** the opener's key is a hot key on a server, kept in an encrypted
  keyring (`docs/SERVICE-KEYS.md`), as the testnet faucet's is.

**Rehearsed (2026-11-01)** on an `aether-rc-onboard` devnet run as
`aether-1` will be: one validator whose node asks 0.0005uaeth per gas,
only the opener and the validator's miner funded in genesis, `faucet
--opener` (no `--amount`) behind the explorer, and the Mine page in
Chromium.

- A brand-new browser account (no account on chain, balance 0) pressed
  Activate; the opener sent it exactly 1,000 uaeth, paying its own fee
  (200 uaeth at the node's price), and the account existed 6 s later.
- It started mining, and its first find was accepted 19 s after
  activation: a lone `MsgSubmitPoW` with a fee of 0, code 0, 287,331 gas.
  The account then held 4,251,000 uaeth (the 1,000 plus the reward;
  nothing taken for a fee).
- Asking the opener again for the same address: `account_exists`.
- An ordinary send with no fee on the same node: refused,
  `insufficient fees; required: 200uaeth`.

The other ways considered: someone else's send (no help to a stranger),
or submissions anyone can relay so the miner never signs until it has
funds (a consensus change to `MsgSubmitPoW`, kept as a later option).

## Decisions for Joshua

| Decision | Options | Recommendation | Decided (2026-10-22) |
|---|---|---|---|
| Seat rule | a + b + c, or a + b + c + d | The testnet rule from 396,000 is a + b + c + d (merged work counts). Decide d for mainnet with the simulation of SEAT-RULE.md's known weaknesses, after asking the large Litecoin and Dogecoin pools whether they would run validators | **a + b + c at launch** (2026-11-01): merged work earns its reward but seats nobody on a fresh chain until an upgrade turns d on, once two or more independent pools commit to running validators. Built: `FreshMergedSeatWorkHeight` (SEAT-RULE.md, "Merged work on mainnet") |
| Supply | fair launch (as now) or an allocation | fair launch | **fair launch** |
| First USDC path | Injective (Noble is being wound down by Circle) | Injective, as on the testnet | **Injective** |
| Inflow cap | an amount | low at launch, raised by governance | **5,000 USDC**, chain-wide, raised by governance |
| Fees | a minimum price, USDC accepted, where fees go | accept USDC; fees to the treasury | **accept USDC; fees to the treasury** (built: `app/fees.go`). Minimum price **decided 2026-11-03: 0.0001uaeth and 0.0005 per gas in the USDC voucher** (a send at the 400,000 default limit: 0.00004 AETH or $0.0002), with tipping above it (built: `app/tipping.go`, #207). A lone native mining submission with valid proof-of-work needs no fee (built: `app/mining_fee.go`, 2026-11-01), so a miner can send a find with an empty balance; its account must still exist on chain, which the opener provides (see "Opening a new miner's account") |
| Operators | who, and how many | seven or more before phase 2 | to define: a draft is in §3 |
| Legal | — | one consultation before phase 2 | Joshua is researching it |
