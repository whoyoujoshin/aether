# Aether Technical Whitepaper

**Version:** 1.0  
**Chain ID (testnet):** `aether-testnet-1`  
**Source commit (parameters verified against):** `796e9c8`  
**Status:** Early-stage; no independent security audit

---

## Abstract

Aether is a sovereign Cosmos SDK / CometBFT blockchain that replaces staking-based validator selection with epoch-ranked proof-of-work. Native Scrypt mining accumulates work toward a fixed-size Top-K validator set; Litecoin/Dogecoin-family AuxPoW may satisfy block difficulty and earn rewards but does not contribute Top-K standing. Account transactions require ML-DSA-44 (Dilithium2 / FIPS 204) signatures from genesis. Consensus remains CometBFT with ed25519 validator keys. This document describes the consensus-adjacent PoW module, economics, cryptography, governance, and known security limitations as implemented in-repo.

---

## 1. Introduction

Most Cosmos chains derive voting power from `x/staking`: bonded capital determines who produces blocks. Aether omits staking entirely. Influence is earned by submitting valid native proof-of-work within discrete epochs; at each epoch boundary the top K miners by recorded native work become the active validator set with equal flat voting power.

Design goals reflected in the current codebase:

- Fair-launch mining without a staking or founder-dominated validator set
- Merged-mining compatibility (AuxPoW) without allowing external hashrate to dominate validator selection
- Mandatory post-quantum account signatures from genesis
- Height-gated protocol upgrades for replay-safe parameter and enforcement changes

This whitepaper is descriptive of the implementation under `x/pow`, `x/governance`, `x/treasury`, `crypto/mldsa`, and related packages. It is not a product roadmap and invents no future dates.

---

## 2. System Overview

| Component | Value |
|-----------|--------|
| Chain ID (testnet) | `aether-testnet-1` |
| Bech32 prefix | `aether` |
| Base denom | `uaeth` (1 aeth = 1e6 uaeth) |
| Node binary | `aetherd` |
| Default home | `~/.aether` |
| Language / SDK | Go 1.24; Cosmos SDK v0.50.12 |
| Consensus | CometBFT v0.38.12 |
| PQ crypto library | circl v1.6.4 |

Application layout (selected paths):

- `cmd/aetherd` — node binary
- `x/pow` — Scrypt / AuxPoW verification, difficulty, rewards, validator selection, bonding, slashing, liveness
- `x/governance` — proposals, voting, quorum/threshold resolution
- `x/treasury` — community funds spent only via governance
- `crypto/mldsa` — ML-DSA-44 keys, ADR-028 addresses, ante decorator
- `wallet/` — reusable account / tx library
- `testnet/genesis.json` — live public testnet genesis

Nodes participate in CometBFT consensus using registered ed25519 consensus public keys. Transaction auth uses ML-DSA-44 exclusively via `PostQuantumDecorator`.

---

## 3. Proof-of-Work and AuxPoW

### 3.1 Native Scrypt PoW

Native submissions use Scrypt with parameters **N=1024, r=1, p=1**. Target block interval is **60 seconds**. Difficulty retargets on every accepted submission.

| Parameter | Value |
|-----------|--------|
| Initial difficulty | 285960 |
| Minimum difficulty | 1024 |
| Maximum difficulty | 1e8 |

Accepted **native** submissions increment the miner’s epoch native-work counter by **+1**. That counter alone ranks candidates for Top-K selection (see §4).

Relevant implementation: `x/pow` (mining verification, retarget, submission handling); tooling in `cmd/powminer`, `cmd/scryptbench`.

### 3.2 AuxPoW (merged mining)

Aether accepts Litecoin/Dogecoin-family Auxiliary Proof-of-Work with **AuxPoW chain ID 17776**. A miner may satisfy a block’s work requirement with either native Scrypt or a valid AuxPoW proof.

AuxPoW behavior:

- Earns the full block mining reward
- Triggers difficulty retarget
- **Does not** increment Top-K native-work standing

This separation preserves validator eligibility as a function of deliberate native work rather than incidental external hashrate. Construction and verification utilities: `cmd/auxpowtest`; verification logic in `x/pow`.

---

## 4. Validator Selection

### 4.1 Epochs and Top-K

| Parameter | Value |
|-----------|--------|
| Epoch length | 1440 blocks |
| Top-K | 21 |
| Ranking metric | Epoch native work (accepted native submits) |
| Voting power per selected validator | `ValidatorVotingPower = 1_000_000` (flat) |

At each epoch boundary, addresses are ranked by native work accumulated in that epoch. The top K become the active CometBFT validator set for the next epoch, each with equal voting power.

### 4.2 Registration

Eligibility requires registering an **ed25519 consensus public key** with a **proof-of-possession (PoP)** binding the key to the miner account. CometBFT consensus signing continues to use ed25519. Tooling: `cmd/validatorkeygen`; registration tx path under `x/pow`.

### 4.3 Bonding and penalties

- **Bond cooldown:** 100 blocks (placeholder constant in current code)
- **Equivocation (double-sign):** permanent ban and burn of escrowed funds
- **Downtime:** temporary removal only — rolling window of **60** blocks; remove if **>50%** signatures missed

Liveness is intentionally milder than equivocation. Evidence and slashing paths live under `x/pow`; live-verification helpers include `cmd/equivocationtest`.

### 4.4 BFT implications of flat voting power

Because every active validator has identical voting power, CometBFT’s Byzantine fault tolerance requires **strictly more than 2/3** of the set to be honest (by count, equivalently by power).

Consequences:

- **K = 2** yields **zero** fault tolerance (any single faulty validator can break >1/3)
- Practical tolerance of **one** Byzantine fault requires **K ≥ 4**

Operators should treat small Top-K deployments as research / early-network configurations, not as BFT-hardened production sets. See also §8.

---

## 5. Economics

### 5.1 Block rewards

| Stage | Amount |
|-------|--------|
| Genesis block reward | 5_000_000 uaeth (5 aeth) |
| Decay | Factor **0.66** per year for **8** years (height-based yearly steps) |
| Tail emission | 200_000 uaeth (0.20 aeth) permanent |

Rewards are deterministic from height (no wall-clock dependency). A **15%** cut is taken from mining rewards: portion escrowed for validators / directed to treasury per module logic in `x/pow` and `x/treasury`.

### 5.2 Units

- Display unit: `aeth`
- Base unit: `uaeth` (1 aeth = 1_000_000 uaeth)

Further schedule rationale is documented in the project wiki (e.g. tail-emission decision notes); this whitepaper sticks to on-chain constants.

---

## 6. Cryptography

### 6.1 Account signatures (mandatory PQ)

From genesis, every account transaction must be signed with **ML-DSA-44** (Dilithium2, NIST **FIPS 204**). Enforcement is via `PostQuantumDecorator` in the ante handler. There is no classical-signature fallback for accounts.

- Implementation: `crypto/mldsa`
- Addressing: Cosmos **ADR-028** style addresses over ML-DSA public keys
- Library: Cloudflare circl v1.6.4

Keyring note: each account is an independent ML-DSA-44 keypair. There is no hierarchical deterministic (HD) multi-account derivation from a single mnemonic; a mnemonic backs up exactly one key. This reflects current PQ key-derivation constraints, not a UX preference.

### 6.2 Consensus keys

Validator consensus remains **ed25519** as required by CometBFT. Registration binds the consensus pubkey to the mining/account identity via PoP (§4.2).

---

## 7. Governance

Module: `x/governance`, with spends executed against `x/treasury`.

| Parameter | Value |
|-----------|--------|
| Minimum deposit | 25_000_000 uaeth |
| Deposit period | 14 days |
| Voting period | 7 days |
| Quorum | `ceil(0.6 * TopK)` |
| Pass threshold | 2/3 of non-abstain votes |
| Veto | 1/3 |
| Tenure ramp | 30 days (voting power ramps with continuous participation) |
| Proposal type (primary) | Treasury spends |

Only active validators vote, with tenure-weighted power during the ramp. Passing treasury proposals trigger real transfers from the treasury module account.

CLI examples (illustrative):

```bash
aetherd tx governance submit-proposal <recipient> <amount> <deposit> --from <key> --chain-id aether-testnet-1
aetherd tx governance vote <proposal-id> yes --from <key> --chain-id aether-testnet-1
aetherd query governance proposal <proposal-id>
```

---

## 8. Security and Limitations

### 8.1 Audit status

**Aether has not undergone an independent professional security audit.** The software is early-stage. Do not treat it as production-ready for real value you cannot afford to lose. Known gaps and technical debt are tracked in the project wiki.

### 8.2 BFT / Top-K sizing

Flat `ValidatorVotingPower` implies fault tolerance is a pure function of K (see §4.4). Undersized validator sets (especially K < 4) have little or no Byzantine resilience. Top-K = 21 is the configured production-oriented size; smaller live-verification networks must not be confused with secure BFT assumptions.

### 8.3 Height-gated activation (replay safety)

Several enforcement and validation behaviors activate only at or after fixed heights so that historical blocks remain replayable under older rules:

| Gate | Height |
|------|--------|
| `BootstrapPowerCorrectionHeight` | 40866 |
| `SubmissionCapActivationHeight` | 51007 |
| `BanEnforcementActivationHeight` | 90000 |
| `RotationRevocationActivationHeight` | 90000 |
| `AmountValidationActivationHeight` | 90000 |
| `ParamChangeGovernanceActivationHeight` | 90000 |

Clients and researchers replaying the chain must respect these gates; behavior below a gate is intentionally different from post-activation rules.

The three 90000 gates above were originally set to 77000/80000, but were never actually crossed by a coordinated, fleet-wide binary upgrade before the chain's tip reached them -- a solo-upgraded node diverged (LastResultsHash mismatch) when it replayed/continued past 77000 with the new gate logic against a chain whose real history had been finalized by the old, ungated logic. Those heights are permanently burned for this reason; a single shared future height (90000) replaces them, to be crossed only after seed, sync3, sync4, and peer-1 all swap to the same binary together and confirm matching AppHash.

### 8.4 Other limitations (non-exhaustive)

- No account abstraction; no native IBC (as of the documented commit)
- Bond cooldown: the code default is now `BondCooldownProduction` (4320 blocks, 3 days at the 60s target), derived from genesis's own CometBFT evidence-validity window (48h) plus a 24h safety margin -- see `x/pow/types.go`. This is the default for a *new* chain from genesis; it does not retroactively change a chain (e.g. the live testnet) already initialized with the prior 100-block placeholder, which would need either a fresh genesis or a governance-driven params update (not yet built for x/pow's own parameters) to move forward.
- Public testnet may reset; faucet funds are worthless outside the testnet
- Early testnet history included a period with unintended ~5s `timeout_commit` (later corrected to the designed ~60s interval), inflating height relative to wall-clock age — disclosed in project docs, not a consensus bug

---

## 9. Testnet

Public testnet endpoints (subject to change; verify against the live README):

| Resource | Value |
|----------|--------|
| Chain ID | `aether-testnet-1` |
| Seed | `dfa6aae4b7bfd5b0eb1e22fabbae3e83a475b938@157.245.252.221:26656` |
| RPC | `http://157.245.252.221:26657` |
| gRPC | `157.245.252.221:9090` |
| Faucet | `http://157.245.252.221:8080/request` (POST JSON `{"address":"aether1..."}`) |
| Explorer | `http://157.245.252.221:8081` |
| Genesis | [`testnet/genesis.json`](../testnet/genesis.json) |

Minimal join steps:

```bash
aetherd init <moniker> --chain-id aether-testnet-1
# Replace config/genesis.json with testnet/genesis.json
# Set seeds in config/config.toml to the seed above
aetherd start
```

To expose RPC/gRPC beyond localhost, bind `laddr` / gRPC `address` to `0.0.0.0` in `config.toml` / `app.toml` as documented in the README.

---

## 10. References

In-repository sources of truth for the parameters in this document:

1. `x/pow` — PoW, AuxPoW, difficulty, rewards, validators, slashing, liveness, height gates
2. `x/governance` — deposit, voting, quorum, thresholds, tenure
3. `x/treasury` — treasury accounting and governance spends
4. `crypto/mldsa` — ML-DSA-44, ADR-028 addressing, `PostQuantumDecorator`
5. `testnet/genesis.json` — live testnet genesis parameters
6. `go.mod` — Go 1.24; Cosmos SDK v0.50.12; CometBFT v0.38.12; circl v1.6.4
7. `cmd/powminer`, `cmd/auxpowtest`, `cmd/validatorkeygen`, `cmd/equivocationtest` — operational and verification tooling
8. Project wiki — design decision records, known issues, architecture notes

External standards:

- NIST FIPS 204 — Module-Lattice-Based Digital Signature Standard (ML-DSA)
- Cosmos ADR-028 — Public Key Addresses
- CometBFT / Cosmos SDK documentation for consensus and application wiring

---

*Parameters in this whitepaper were verified against repository state at commit `796e9c8` on `main`. If code and this document diverge, trust the code and genesis.*
