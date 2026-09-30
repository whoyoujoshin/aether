# Merged mining: a pool bridge for mainnet

Aether accepts merged-mining proof-of-work (AuxPoW) from the Litecoin
family under AuxPoW chain ID 17776. It earns the block reward and
retargets difficulty, but never counts toward validator selection. Only
native work does (`submitAuxPoW` in `x/pow/msg_server.go`).

**Status (2026-09-30):**
- **Nothing sends real merged-mining work to Aether.**
  - No pool integration exists.
  - The only thing that has ever produced an AuxPoW proof is
    `cmd/auxpowtest`, which grinds a made-up parent header.
- **Three problems in the chain's rules would stop a real pool** even
  with a bridge. They are listed below.
- **All four chain changes are built:** A (byte order), B (template
  binding), C (separate tracks) and D (parent chain ID). They share one
  placeholder activation height, `MergedMiningActivationHeight`. The
  bridge is next.

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
- From `MergedMiningActivationHeight` (`x/pow/types.go`, a
  placeholder), the parent's scrypt hash is read little-endian before the
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

A, B, C and D change which submissions are accepted, so they share one
coordinated activation height, like the earlier cutovers.

## The bridge: `cmd/auxpowd`

A small service that each pool runs beside its Litecoin node.

- **RPC.** It speaks the merged-mining RPC that Namecoin and Dogecoin
  already use, so pool software needs configuration, not code:
  - `createauxblock <aether address>` returns `hash`, `chainid` (17776),
    `previousblockhash`, `coinbasevalue`, `bits`, a little-endian
    `_target` and `height`;
  - `submitauxblock <hash> <auxpow hex>`;
  - legacy `getauxblock`.
- **Templates.**
  - Reads the latest Aether block and the AuxPoW difficulty from a node.
  - Builds one template per Aether block, bound to the requested reward
    address (change B).
  - Caches templates by hash, and drops them once they leave the recency
    window.
- **Submissions.**
  - Parses the standard `CAuxPow` serialization (coinbase transaction,
    coinbase branch, chain branch, parent header) into `AuxPowData`.
  - Runs `CheckAuxPow` locally first.
  - Signs a `MsgSubmitPoW` with its own ML-DSA-44 key and broadcasts it.
  - Reports "a submission was already accepted this block" as a stale
    share, not an error.
- **Operations.** An RPC username and password (the same model as
  `litecoind`), binding to the pool's private network only, a health
  endpoint, and metrics for accepted and stale shares.

## Testing

- **Unit tests:**
  - parse real Dogecoin and Namecoin AuxPoW test vectors;
  - the byte-order regression (done, for change A);
  - rejection of stolen, replayed and stale-template proofs (done, for
    change B);
  - separate difficulty tracks, slots and reward shares (done, for
    change C);
  - the chain-ID check (done, for change D).
- **End to end on regtest:**
  - Setup: `litecoind -regtest`, plus a stratum pool with merged mining
    enabled, pointed at `auxpowd` on an Aether devnet.
  - Pass means rewards land at the pool's address, and a native miner
    still wins its slot and keeps validator standing.
- **Testnet:** one small Scrypt pool trial, before any mainnet date.

## Milestones

1. **M1: chain changes A–D, with tests.** Done.
   This is the consensus-critical part.
2. **M2: `cmd/auxpowd`.**
3. **M3: regtest end to end** with a real Litecoin node and pool.
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
