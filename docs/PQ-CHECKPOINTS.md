# Post-quantum checkpoints (design)

**Status:** design only, 2026-10-18. Nothing here is built. It changes consensus
state, so it ships behind an activation height agreed with every validator
operator.

## The gap

Every Aether account transaction is signed with ML-DSA-44 (`app/post_quantum.go`):
no classical signature moves AETH. Block finality is not post-quantum.
Validators are chosen by work in `x/pow`, with no stake, but CometBFT finalizes
each block with their **ed25519** consensus keys. An attacker who could forge
ed25519 signatures could forge commits for a fake chain. Anyone checking
Aether's history would accept that chain: a CometBFT light client, an IBC
counterparty, an exchange, or a new node syncing from a peer. `docs/WHITEPAPER.md`
already states this limitation. This design narrows it without replacing
CometBFT.

## The idea

Every `N` blocks, the validators co-sign a **checkpoint** with ML-DSA-44: the
block hash and app hash at that height, plus a hash of the next committee's
keys. Checkpoints chain from committee to committee. Given one trusted
checkpoint, anyone can verify all later finalized history with ML-DSA alone,
and ed25519 is never consulted. A chain forged by someone who can break
ed25519 has no valid checkpoints, so it is refused.

The checkpoints use the validator set Aether already has: no new set, no stake,
no new token. Power stays with whoever earned a seat by mining.

## What it does not do

Stated up front, because the pitch will be tested against it:

- **Consensus is still ed25519.** Blocks are still produced and finalized by
  CometBFT. Between checkpoints (up to `N` blocks plus the signing window),
  history is protected only by ed25519.
- **IBC counterparties don't see checkpoints.** Osmosis's and Injective's light
  clients of Aether verify ed25519 commits. Bridged assets (USDC, OSMO) keep
  their home chains' classical security either way. A counterparty would need
  a custom light client (for example 08-wasm) to use checkpoints.
- **Liveness and censorship are unchanged.** Checkpoints protect history, not
  the next block.
- **The trust root is a checkpoint.** A verifier starts from a checkpoint it
  trusts: the activation checkpoint published with the release, or a later one.
  Like any BFT system, it assumes fewer than a third of each committee is
  dishonest.

## Design

### Checkpoint keys

Each miner who wants a validator seat registers a dedicated ML-DSA-44
**checkpoint key**. It is separate from the miner's account key, so funds stay
in a cold key while the checkpoint key sits on the validator host.

```
MsgRegisterCheckpointKey {
  miner      string   // signer; the miner account (ML-DSA, as every tx)
  pubkey     bytes    // ML-DSA-44 public key, 1,312 bytes
  pop        bytes    // ML-DSA-44 signature by pubkey over popBytes
}
popBytes = "AETHER-PQ-CHECKPOINT-KEY-V1\x00" || chain_id || "\x00" || miner
```

Proof of possession is domain-separated and bound to the chain. The existing
ed25519 registration signs only the bare miner address, so this one does not
copy it. As with consensus keys, one key belongs to one miner: a key another
miner holds is refused (the `ConsensusKeyGuard` rule). Rotation replaces the
key; the new key signs from the next committee on.

### Checkpoint heights and records

A checkpoint is taken at every height `H` where `(H + 1) % N == 0`. `N` must
divide the epoch length (1,440). The proposed `N` is 240: six checkpoints an
epoch, about one every 24 minutes at about 6 s a block. The last block of each
epoch, `n×1440−1`, is always a checkpoint height. That is the height whose
EndBlock recomputes the validator set (`ComputeValidatorUpdates`), so every
set change at an epoch boundary is captured.

The record is built over two blocks, because a block's app hash is only known
in the next block's header:

- **EndBlock of `H`:** store `block_hash(H)` (from the FinalizeBlock request)
  and `next_committee`. This is the active validator set as of the end of `H`,
  each member with its checkpoint key or none.
- **BeginBlock of `H+1`:** store `app_hash` from header `H+1` (the state after
  `H`) and open the record for signatures.

```
Checkpoint {
  height              int64
  block_hash          bytes  // 32
  app_hash            bytes  // 32, state after height
  committee_ref       int64  // height of the checkpoint whose next_committee signs this one
  next_committee      []Member{miner, pubkey}   // sorted by miner address
  next_committee_hash bytes  // SHA-256 over the sorted members (empty pubkey if none)
  signatures          []{miner, signature}
  status              PENDING | FINAL | EXPIRED
}

signBytes = "AETHER-PQ-CHECKPOINT-V1\x00" || chain_id || "\x00"
            || uint64be(height) || block_hash || app_hash || next_committee_hash
```

Sign bytes are fixed-layout binary rather than JSON or amino, so the Go and
TypeScript verifiers can't disagree about encoding.

### Who signs: the committee

The committee for a checkpoint is **frozen**: it is the `next_committee` of
the **last finalized** checkpoint (`committee_ref`). It is never the live set.

- The live set can change in the middle of an epoch: a validator that misses
  more than half of the last 60 blocks is removed at once. A frozen committee
  means a verifier holding the previous checkpoint knows exactly whose
  signatures count.
- Chaining only through *finalized* checkpoints means an expired one never
  strands a verifier. The next checkpoint is signed by the same committee as
  the expired one.
- Members count equally, as validators do (`ValidatorVotingPower`). A member
  with no checkpoint key counts as not signing.

A checkpoint becomes **FINAL** once strictly more than two thirds of its
committee have valid signatures recorded: 3 of 4 today, 15 of 21 at the
`TopKSize` limit.

### Submitting signatures

```
MsgSubmitCheckpointSignature {
  submitter  string   // any ML-DSA account; pays the (zero today) fee
  height     int64
  miner      string   // the committee member this signature is for
  signature  bytes    // ML-DSA-44 over signBytes(height), 2,420 bytes
}
```

The handler accepts a signature only if all of these hold:

- the record exists and is PENDING;
- the miner is in its committee and has a key;
- no signature for that miner is recorded yet;
- the signature verifies.

The checkpoint signature is its own authentication, so any account may submit
it. A small sidecar can relay signatures from a fee account, and the miner's
account key never has to be online. Signatures arriving after FINAL are
refused, which keeps the record small. Gas charges for one ML-DSA verification.

A record still PENDING after `W` blocks (proposed `W = N`) becomes **EXPIRED**,
and an event is emitted. Expiry does not stop the chain. The next checkpoint
uses the same committee.

### Equivocation: what makes it accountable

ML-DSA signatures are on-chain evidence. Anyone may submit:

```
MsgSubmitCheckpointEquivocation {
  submitter, miner, height, block_hash, app_hash, next_committee_hash, signature
}
```

If `signature` is a valid ML-DSA signature by `miner`'s checkpoint key over
sign bytes for `height` that differ from the recorded checkpoint, the miner
signed a conflicting history. They are banned with the existing permanent ban
(`SetBanned`), the same as ed25519 equivocation. A forged fork therefore needs
more than two thirds of a committee to sign a conflict, and each of them leaves
proof that bans them.

### The sidecar

`cmd/checkpointd` runs on each validator host next to `aetherd`:

- it follows new PENDING records over RPC;
- it signs with the checkpoint key (a file with mode 0600, like
  `priv_validator_key.json`);
- it submits the signature from a fee account.

Signing the same checkpoint twice is harmless (same bytes), so a sidecar
restart can't cause equivocation. The sign bytes come from chain state, so an
honest sidecar can only sign what the chain recorded. It ships as a prebuilt
binary (`scripts/prebuild.sh`): nothing is compiled on a validator.

### Why not the alternatives

- **ABCI vote extensions (CometBFT 0.38):** validators would attach the ML-DSA
  signature to each precommit. The costs:
  - it needs a consensus-parameter change;
  - it adds about 2.4 KB to every vote in every block;
  - it couples post-quantum signing to liveness: a broken or failing extension
    can stall the chain.

  The sidecar has none of these costs. Vote extensions can be revisited once
  checkpoints have run for a while.
- **Patching CometBFT for ML-DSA consensus keys:** full post-quantum
  consensus, but it means a CometBFT fork maintained by a solo project, and
  every IBC counterparty's light client would break. It's out of scope;
  checkpoints come first either way.

## Verifiers

- **Go:** a `pqcheckpoint` package:
  - `Verify(trusted Checkpoint, chain []Checkpoint) (latest Checkpoint, err)`
    walks finalized checkpoints from a trusted one, checking each committee's
    threshold and `next_committee_hash`;
  - `VerifyBlock(latest, header)` confirms a block at or below a checkpoint
    height (via the block hash, or an app-hash proof for state).

  It is used by `aetherd q pow checkpoint` / `checkpoint-proof`, the explorer
  and agentmcp.
- **TypeScript and Python:** the same walk, in `aether-chain-client`. Both
  clients already carry ML-DSA-44. Shared test vectors live in
  `clients/testdata/` (as `swapquote.json` does), so all three agree.
- **Explorer:** a "PQ-final" mark on blocks at or below the latest FINAL
  checkpoint, a checkpoints page (committee, signatures, status) and
  `/api/checkpoints` (added, not changed, per `docs/API-STABILITY.md`).
- **Full nodes:** an optional `--pq-verify` mode checks checkpoints while
  syncing and **warns** on a mismatch. It does not halt: halting on a
  disagreement would let a broken sidecar stop a node.

## Size and cost

- **Per checkpoint:** `n` × 2,420 bytes of signatures, plus the committee
  (`n` × 1,312-byte keys, stored only when it changes). With 4 validators that
  is under 10 KB every 240 blocks; with 21, about 51 KB.
- **Verification:** ML-DSA-44 verification takes well under a millisecond, so
  checking a committee is negligible.
- **Light client:** about 2.4 MB a month with 4 validators.

## Rollout

The work runs in this order:

1. **Devnet** (`bench/devnet.sh`-style, four validators, low activation height).
   Tests:
   - keeper tests: record building across the H/H+1 split, committee freezing
     and chaining across an expired checkpoint, threshold, window expiry,
     duplicate and late signatures, PoP refusal, key-in-use refusal,
     equivocation ban;
   - a restart test proving a node replaying from genesis builds identical
     records (the gating discipline every `x/pow` activation follows);
   - a mid-epoch liveness removal while a checkpoint is pending.
2. **Activation height** chosen with Gitty. Every validator runs the new binary
   before it, peer-1 included, through its scheduled task. Upgrades go one node
   at a time, only while the other three are signing.
3. **Phase 1, advisory:**
   - keys are registered and checkpoints recorded and signed;
   - equivocation bans are on from the start, since the evidence is
     self-proving;
   - nothing else depends on checkpoints.

   The first FINAL checkpoint after activation is published as the trust root,
   with its hash in the release notes.
4. **Phase 2, required (a later height):** `ComputeValidatorUpdates` skips
   miners without a checkpoint key, so every seat signs. This waits until every
   current operator has registered one.

## Open questions

- **`N` and `W`:** 240 and 240 are a starting point. A shorter interval
  narrows the ed25519-only window and costs more signatures.
- **Committee recovery:** if more than a third of the last finalized committee
  vanish for good (keys lost, operators gone), checkpoints can never finalize
  again. Recovery would need a governance-approved committee reset. That reset
  is secured by classical consensus, so it must be rare, public and announced
  well ahead. Should it exist in phase 1?
- **Which hash to sign:** block hash only, or block hash and app hash (as
  above)? The app hash lets a verifier check state proofs at a checkpoint, not
  only blocks.
- **Required keys:** should registering a checkpoint key be part of validator
  registration for new miners from phase 1, so phase 2 needs no migration?
