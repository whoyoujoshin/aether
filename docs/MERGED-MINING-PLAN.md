# Merged mining: a pool bridge for mainnet

Aether accepts merged-mining proof-of-work (AuxPoW) from the Litecoin
family under AuxPoW chain ID 17776. It earns the block reward and
retargets difficulty, but never counts toward validator selection. Only
native work does (`submitAuxPoW` in `x/pow/msg_server.go`).

**Status (2026-10-03):**
- **Nothing sends real merged-mining work to Aether yet.**
  - The pool bridge, `cmd/auxpowd`, is built (M2) and proven end to end
    on a devnet with a real Litecoin node (M3), both with a pool program
    and with real pool software: yiimp's stratum server, mined by
    pooler's `cpuminer`, got Litecoin blocks a real `litecoind` accepted
    and the same blocks' proofs paid on Aether.
- **Three problems in the chain's rules would stop a real pool** even
  with a bridge. They are listed below.
- **All five chain changes are built:** A (byte order), B (template
  binding), C (separate tracks), D (parent chain ID) and E (coinbase-only
  commitment, committed nonce). They share one
  activation height, `MergedMiningActivationHeight`, which is now
  **205,000** on the testnet (`October2026UpgradeHeight`, with the smooth
  retarget and the randomness beacon; docs/UPGRADE-2026-10.md). It was a
  1,000,000 and then a 20,000,000 placeholder. A pool trial is next (M4).

## What the code shows

1. **Real Litecoin work fails Aether's check.**
   - Litecoin reads a scrypt hash as a little-endian number
     (`UintToArith256`), so real work has its zero bytes at the end of the
     raw hash. `meetsdifficulty` reads the raw bytes big-endian.
   - A real Litecoin block therefore clears Aether's difficulty only by
     chance. Litecoin's genesis block is the proof: it clears its own
     target little-endian and fails big-endian
     (`TestParentPoW_RealLitecoinGenesisClearsOnlyLittleEndian`).
   - `cmd/auxpowtest` ground its proofs under the same big-endian rule. So
     the testnet check behind the README's "live-verified" confirmed the
     pipeline, but never with real parent-chain work.
2. **A proof isn't tied to who mined it, or when.**
   - Native work commits to the miner's address and a recent block hash.
     AuxPoW commits only to `aux_block_hash`, which the submitter chooses
     freely.
   - Anyone who sees a pool's proof in the mempool can resubmit it under
     their own address and take the reward.
   - Proofs can also be mined ahead and stockpiled.
3. **Pool hash power would starve native mining.**
   - Native and AuxPoW work share one difficulty and one accepted
     submission per block (`SubmissionCapActivationHeight`).
   - Aether's difficulty (about 11.6M in September 2026) is roughly a
     Litecoin share difficulty of 180, which any pool clears constantly.
     Pools would win every slot, and retargeting would climb to pool scale.
   - Native miners would stop landing work, and native work is the only
     thing that picks validators. The validator set couldn't refresh.
4. **The parent's chain ID isn't checked.** The reference implementation
   rejects a parent that carries the merged-mined chain's own ID.

## Chain changes (one height-gated cutover)

**A. Byte order: built.**
- From `MergedMiningActivationHeight` (`x/pow/types.go`, deferred
  for now), the parent's scrypt hash is read little-endian before the
  difficulty comparison (`parentPoWHash` in `x/pow/auxpow.go`).
- Below that height the old rule applies, so history replays unchanged.
- Native submissions keep Aether's own convention.
- `cmd/auxpowtest --byte-order legacy|litecoin` grinds under whichever
  rule the target chain applies. The default stays `legacy` until the
  height is live.

**B. Bind each proof to a template: built.**
- From `MergedMiningActivationHeight`, `aux_block_hash` must equal
  `AuxPoWTemplateHash` (`x/pow/auxpow.go`): SHA-256 over the
  length-prefixed fields `"aether-auxpow/v1"`, chain ID, template height
  (8 bytes, big-endian), the block hash at that height, and the reward
  address bytes.
- `AuxPowData` gains `template_height` and `reward_address`
  (`checkAuxPoWTemplate` in `x/pow/msg_server.go`):
  - `template_height` must have a recorded block hash and fall inside the
    recency window, as for native work (`RecencyWindowK`);
  - `reward_address` must be a valid address and not banned.
- The reward goes to `reward_address` whoever submits. The bridge's key
  then needs no funds and can't redirect anything, and a copied proof
  still pays the pool.
- A template can be claimed once: the accepted-work record is keyed on
  `aux_block_hash`, so a pool gets at most one reward per template and
  can't stockpile shares against an old one.
- Below the height the signer is paid and `aux_block_hash` is unchecked,
  as before.
- `cmd/auxpowtest --template-height N --template-block-hash <hex>
  --reward-address aether1…` builds a bound proof. It computes the hash
  independently, and a test checks it against the chain's.

**C. Separate tracks for native and merged work: built** (`x/pow/merged_mining.go`).
- **Difficulty.** AuxPoW gets its own difficulty, retargeted only on
  AuxPoW submissions toward the same `TargetBlockTime` (60 s).
  - It starts from the native difficulty.
  - It's capped at `AuxMaxDifficulty` (2^62) rather than the native
    `MaxDifficulty`. That way pool hash power retargets to one proof per
    interval instead of landing every block at the cap.
  - `aetherd q pow difficulty` reports it as `aux_difficulty`.
- **Slots.** Each block accepts one native and one AuxPoW submission.
- **Reward (decision 1: fixed total, split).** The schedule issues one
  block reward per target interval, and it still does:
  - while both tracks are mining, a native submission earns 75% and an
    AuxPoW submission earns 25% (the default; governance can change it), so a pair
    earns exactly one reward;
  - a track mining alone earns the full reward, so native miners lose
    nothing until pools actually arrive;
  - a track counts as mining if it had a submission accepted in the last
    five target intervals (5 minutes).
- **Why.** Native mining stays viable at native difficulty and keeps
  driving validator selection, and total issuance stays on the published
  schedule.

**D. Reject a parent whose chain ID is 17776: built.** From
`MergedMiningActivationHeight`, `CheckAuxPow` refuses a parent header
whose version carries Aether's own AuxPoW chain ID, as the reference
implementation does.

**E. Two more reference checks: built.** Found while building the
bridge. From `MergedMiningActivationHeight`, `CheckAuxPow` also requires,
as the reference implementation does:
- **the commitment to be in the parent's coinbase** (coinbase branch
  index 0). Any other transaction's first input is written by whoever
  sends it, so without this anyone could get an ordinary parent-chain
  transaction carrying their own commitment into a block, and be paid
  for that block's work;
- **`chain_nonce` to be the nonce the coinbase commits to.** It picks
  Aether's slot in a multi-chain tree, so it can't be a free choice.

A, B, C, D and E change which submissions are accepted, so they share one
coordinated activation height, like the earlier cutovers.

## The bridge: `cmd/auxpowd` (built)

A small service that each pool runs beside its Litecoin node:

```
auxpowd --from bridge --rpc-user pool --rpc-password-file /etc/auxpowd.pass \
  --node http://127.0.0.1:26657 --grpc 127.0.0.1:9090 --listen 10.0.0.5:8336
```

`--from` is an ML-DSA key in the node's keyring (`--home`, default
`~/.aether`). It signs the submissions and pays only their fee (`--fees`,
zero by default), so it needs no funds beyond having an account.
`--reward-address` sets who the legacy argument-less `getauxblock` pays.

- **RPC.** It speaks the merged-mining RPC that Namecoin and Dogecoin
  already use, so pool software needs configuration, not code:
  - `createauxblock <aether address>` returns `hash`, `chainid` (17776),
    `previousblockhash`, `coinbasevalue`, `bits`, a little-endian
    `_target` and `height`;
  - `submitauxblock <hash> <auxpow hex>`;
  - legacy `getauxblock`;
  - `getblockcount`, the Aether tip;
  - for pool software that treats a merged-mined daemon as a full node
    (yiimp does): `getblocktemplate`, describing the next Aether block
    with no transactions (the work still comes from `getauxblock`),
    `validateaddress`, `getdifficulty` and `getmininginfo`;
  - batches, and bitcoind's HTTP status codes.
  - The target goes out as both `_target` (Namecoin's name) and `target`
    (Dogecoin's legacy name, which yiimp reads), the same little-endian
    bytes.
  - `coinbasevalue` is the merged share of the reward (1.25 AETH of 5 at
    the default 2,500 bps), what a proof earns while native mining is
    active. A merged track mining alone earns the whole reward.
- **Templates.**
  - Reads the latest Aether block and the AuxPoW difficulty from a node.
  - Builds templates bound to the requested reward address (change B),
    and hands out the same one until a proof for it is sent or it's half
    the recency window old. Pools such as yiimp commit the aux hash when
    they build a mining job but submit with the hash they fetched last,
    so work changing every Aether block (about 6 s) would rarely match;
    Namecoin and Dogecoin work changes about as seldom as this.
  - The chain pays a template once, so a sent proof retires its template:
    the next request gets new work, and a later proof for it counts as
    stale rather than costing a transaction the chain would refuse.
  - Caches templates by hash, and drops them once they leave the recency
    window.
- **Submissions.**
  - Parses the standard `CAuxPow` serialization (coinbase transaction,
    coinbase branch, chain branch, parent header) into `AuxPowData`.
  - Uses the template the coinbase commits to when it's one the bridge
    handed out, even if the pool named a newer one (yiimp does): that's
    what the chain checks, and the only address it pays.
  - Runs `CheckAuxPow` locally first.
  - Signs a `MsgSubmitPoW` with its own ML-DSA-44 key and broadcasts it.
  - Sends at most one submission per Aether block, since each block takes
    one. A later proof for the same block, or one whose slot another
    submission took on chain, counts as a stale share, not an error.
  - Follows each transaction into a block and logs it as accepted (with
    the address paid), stale or refused.
  - Refuses work below `MergedMiningActivationHeight`, where the chain
    would pay the bridge's own key rather than the pool.
- **Operations.** An RPC username and password (the same model as
  `litecoind`), binding to the pool's private network only, `GET /health`
  (503 until merged mining is active or while the node is unreachable),
  and Prometheus counters at `GET /metrics`:
  `auxpowd_templates_total` and `auxpowd_shares_total` by outcome
  (invalid, stale, submitted, accepted, rejected, failed).

## Testing

- **Unit tests:**
  - the bridge's parser on three real Dogecoin blocks merged-mined with
    Litecoin (371,337, 748,634 and 894,863): every hash and branch
    matches libdohj's expected values, the coinbase commits to the
    Dogecoin block at the slot its nonce picks, and the Litecoin parent's
    scrypt work meets Dogecoin's target read little-endian (done);
  - the byte-order regression (done, for change A);
  - rejection of stolen, replayed and stale-template proofs (done, for
    change B);
  - separate difficulty tracks, slots and reward shares (done, for
    change C);
  - the chain-ID check (done, for change D);
  - the coinbase-only and committed-nonce checks (done, for change E);
  - the bridge's RPC against a fake chain: a pool's round trip, refused
    and stale work, the activation gate, auth, batches and metrics (done).
- **End to end on a devnet (done, 2026-10-01).** `aetherd` and `auxpowd`
  built with the activation height at 5 (a local patch, not committed),
  and `cmd/auxpowtest --auxpowd` as the pool: it asks for work, mines a
  Litecoin-style parent and submits the standard serialization.
  - Before height 5 the bridge handed out no work (`/health` 503).
  - Three proofs were accepted and paid 4.25 AETH each (the 5 AETH reward
    less the 15% cut) to the pool's address, which had no account before.
    The bridge's key paid nothing.
  - With `cmd/powminer` mining natively at the same time, each proof paid
    the 25% share (1.0625 AETH after the cut), native submissions kept
    landing, and the two difficulties moved apart (91,912 merged, 589,824
    native).
- **End to end with a real Litecoin node (M3, done 2026-10-01).** Litecoin
  Core 0.21.4 (the release from GitHub, checksum matching its
  `SHA256SUMS.asc`) on regtest, the same Aether devnet and `auxpowd`, and
  a small pool program (not committed) doing what a pool does:
  - put a real wallet transaction in `litecoind`'s mempool, then build a
    block from its `getblocktemplate`: a SegWit coinbase with the BIP34
    height, the merged-mining commitment to `createauxblock`'s hash with a
    nonce, the payout and witness-commitment outputs, and the coinbase's
    merkle branch over the block's real transactions;
  - mine it until its scrypt hash, read little-endian, met `_target`;
  - `submitblock` it: `litecoind` accepted all three blocks (heights
    102 to 104, each two transactions) as its new tip;
  - send the same block's proof to `submitauxblock`, the coinbase with its
    witness, which the bridge drops for the txid.

  All three proofs were accepted on Aether and paid 4.25 AETH each to the
  pool's address.
- **Real pool software (done 2026-10-03).** yiimp's stratum server
  (tpruvot/yiimp, built from source with MariaDB) with Litecoin as its
  coin and Aether as an aux coin pointed at `auxpowd`, and pooler's
  `cpuminer` 2.5.1 mining Scrypt into it over stratum on four threads.
  - yiimp needed one local patch, unrelated to Aether: Litecoin Core 0.21
    refuses `getblocktemplate` without the `mweb` rule, which this yiimp
    predates. Any pool on Litecoin 0.21 carries the same change.
  - The first run found two things in the bridge, fixed above: it took
    only string arguments (yiimp passes `getblocktemplate` a request
    object), and its work changed every Aether block, so yiimp's proofs
    named newer work than they committed to and all were refused.
  - Then, over four minutes: yiimp got 8 Litecoin blocks `litecoind`
    accepted and sent 7 aux proofs. The bridge broadcast 4, all accepted
    on Aether, paying the pool 17 AETH (4.25 each); 3 were stale, for work
    already claimed from jobs yiimp built before it fetched new work. No
    transaction was refused on chain.
  - Still to do: confirm under load that a native miner keeps its slot
    and validator standing while a pool is mining.
- **Testnet:** one small Scrypt pool trial, before any mainnet date.

## Milestones

1. **M1: chain changes A–E, with tests.** Done.
   This is the consensus-critical part.
2. **M2: `cmd/auxpowd`.** Done, and proven on a devnet.
3. **M3: end to end with a real Litecoin node.** Done (regtest), with a
   pool program and with real pool software (yiimp and `cpuminer`).
4. **M4: testnet cutover and a pool trial.**

## Decisions

1. **Reward split for merged work: decided.** Fixed total, split 75/25
   while both tracks are mining; a track mining alone earns the full
   reward (change C). Issuance stays on the published schedule. The share
   is a governance parameter: `merged_mining_reward_share_bps` in x/pow's
   `MsgUpdateParams` (`aetherd tx pow draft-update-params
   --merged-mining-reward-share-bps 1500`), 1 to 10,000 basis points,
   2,500 until changed. It can be set only from
   `MergedMiningActivationHeight`, and a proposal that leaves it out keeps
   the current share.
2. **Who runs the bridge.**
   - Ship `auxpowd` for each pool to run next to its own node: standard
     practice, and the least trust.
   - Also run a public endpoint for small pools if wanted.
3. **Parent chain: Litecoin only to start.** Pools that merge-mine
   Dogecoin already produce Litecoin parents, so they're covered.
