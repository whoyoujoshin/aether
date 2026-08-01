# Aether (AETH)

Aether is a sovereign, staking-free proof-of-work blockchain built on Cosmos SDK and CometBFT. Core design principle: **fair launch via mining, no early validator or founder dominance** — validators are chosen entirely by real, tracked mining work, not capital or staking, and mining rewards flow to everyday participants rather than a pre-mined or founder-allocated supply.

> **⚠️ This project has not undergone a professional, independent security audit.** It is early-stage software. Do not use it with real funds you cannot afford to lose entirely. See [Known Issues and Technical Debt](../../wiki/Known-Issues-and-Technical-Debt) and the security review prep document in this repo for an honest account of what has and hasn't been independently reviewed.

Full design history, live-verification write-ups, and every locked architectural decision (with complete reasoning) are tracked in the [project wiki](../../wiki).

## Current Status

| Area | Status |
|---|---|
| Core PoW mining, Scrypt hashing, difficulty retargeting, reward distribution | ✅ Built, tested, live-verified |
| Epoch-based Top-K validator selection (no staking module) | ✅ Built, tested, live-verified on a 2-node network |
| Validator bonding, equivocation slashing, automatic escrow release | ✅ Built, tested, live-verified with real constructed equivocation evidence |
| Validator liveness (downtime) detection — distinct from equivocation | ✅ Built, tested, live-verified |
| Ancestor validation (prevents work-inflation via unvalidated headers) | ✅ Built, tested, live-verified |
| AuxPoW (merged mining) — self-verifying, Litecoin/Dogecoin-family | ✅ Built, tested, live-verified against primary-source binary formats |
| Post-quantum signatures (ML-DSA-44 / Dilithium2), mandatory from genesis | ✅ Built, tested, live-verified — real keys, real ante-handler enforcement |
| Full governance module (deposit-gated proposals, tenure-weighted voting, treasury execution) | ✅ Built, tested, live-verified |
| `x/pow` query CLI | ✅ Built, live-verified |
| Fee market / tail emission / issuance decay | ⬜ Not built — flat block reward only |
| Account abstraction, native IBC, real wallet, faucet, block explorer | ⬜ Not built |
| Independent professional security audit | ⬜ Not yet performed |

See [Known Issues and Technical Debt](../../wiki/Known-Issues-and-Technical-Debt) for an honest, detailed accounting of every known gap and deferred item, and [Roadmap](../../wiki/Roadmap) for what's next.

## Why staking-free, and why post-quantum?

Most Cosmos SDK chains derive their validator set from `x/staking` — token holders bond capital, delegate to validators, and voting power follows stake. Aether has no staking module at all. Validators are chosen by **real, tracked mining work**, not capital — influence is earned, not bought or pre-allocated.

Aether also requires **ML-DSA-44 (Dilithium2, NIST FIPS 204) post-quantum signatures for every account transaction, from genesis, with no classical-signature fallback** — a deliberate choice to build quantum resistance into the chain's identity from day one rather than retrofit it later. See the design decision records in the wiki for the full reasoning, including why ML-DSA was chosen over Falcon, and the real, current cryptographic limitations around key derivation that shaped the keyring design.

## Quick Start (single-node devnet)

```powershell
# Build
go build ./...
go install -mod=mod ./cmd/aetherd

# Initialize node (only needed once, or after a full reset)
aetherd init mynode --chain-id aether-testnet-1

# Start the node (in its own terminal — this runs in the foreground)
aetherd start
```

In a second terminal:

```powershell
# Check current chain state
aetherd query pow difficulty
aetherd query pow block-reward
aetherd query pow active-validators
aetherd query pow current-epoch

# Create a real ML-DSA-44 key -- works naturally, no special flags needed
aetherd keys add mywallet --keyring-backend test

# Mine and submit a real block (queries live chain state automatically)
go run ./cmd/powminer --miner <your-bech32-address>
# ...then run the aetherd tx pow submit command it prints
```

**Note on keys:** every Aether account is an independent ML-DSA-44 keypair — there is no hierarchical (HD) derivation. A mnemonic backs up exactly one key; it does not derive multiple accounts the way Bitcoin/Ethereum-style wallets do. This reflects a genuine, current limitation in post-quantum cryptography, not a convenience choice — see the wiki for the full explanation.

## Registering as a validator

Validators are chosen by real mining work, not staking. To become eligible:

```powershell
# Generate (or load an existing) consensus keypair and the required
# proof-of-possession signature
go run ./cmd/validatorkeygen --miner <your-bech32-address>
# ...then run the aetherd tx pow register-validator-pubkey command it prints
```

Once registered, mine and submit successfully within an epoch to accumulate ranked work — at the next epoch boundary, the top-K addresses by recorded work become the active validator set. Validators who go offline (missing >50% of signatures within a rolling window) are automatically, temporarily removed — a much milder consequence than equivocation (double-signing), which results in a permanent ban and full escrow forfeiture.

## Merged mining (AuxPoW)

Aether supports merged mining with Litecoin and Dogecoin (both Scrypt-based). A miner may satisfy a block's proof-of-work requirement with either native Scrypt work or a valid AuxPoW proof — never both required. **Only native work counts toward validator (Top-K) eligibility** — AuxPoW work earns the full mining reward and secures the chain, but deliberately does not build standing toward becoming a validator, preserving the fair-launch principle against large, incidental external hashrate. See `cmd/auxpowtest` for a tool that constructs a real, valid test AuxPoW submission, and the wiki for the complete design rationale.

## Governance & Treasury

Aether has a full, working governance system: anyone can submit a deposit-gated treasury-spend proposal (25,000,000 aeth minimum deposit), active validators vote with tenure-weighted power (a 30-day ramp rewarding sustained, continuous participation), and passing proposals (60% quorum, 2/3 threshold, with a 1/3 veto override) automatically execute a real transfer from the treasury.

```powershell
aetherd tx governance submit-proposal <recipient> <amount> <deposit> --from <key> --chain-id aether-testnet-1
aetherd tx governance vote <proposal-id> yes --from <key> --chain-id aether-testnet-1
```

## Repo Layout

- `x/pow` — the core module: mining verification (Scrypt + AuxPoW), difficulty retargeting, reward distribution, validator selection/bonding/slashing/liveness, ancestor validation
- `x/governance` — deposit-gated proposals, tenure-weighted voting, quorum/threshold resolution
- `x/treasury` — single source of truth for spendable community funds, spent only via governance-authorized proposals
- `crypto/mldsa` — the ML-DSA-44 post-quantum signature scheme: real keys, ADR-028 addressing, keyring integration
- `cmd/aetherd` — the node binary
- `cmd/powminer` — standalone tool that queries real chain state and brute-forces a valid native PoW nonce
- `cmd/auxpowtest` — standalone tool that constructs a real, valid test AuxPoW (merged-mining) submission
- `cmd/scryptbench` — benchmarks real Scrypt hash throughput, used to responsibly retune difficulty constants
- `cmd/validatorkeygen` — generates/loads a consensus keypair and produces the registration proof-of-possession signature
- `cmd/balancecheck` — gRPC bank balance checker
- `cmd/equivocationtest` — constructs real, cryptographically valid equivocation evidence, used to live-verify the slashing path

## Multi-node devnet

See the [Architecture](../../wiki/Architecture) and [Phase 1](../../wiki/Phase-1-Multi-Validator-Selection) wiki pages for how dynamic validator onboarding works, including a full worked example of standing up a second node with real P2P peering and registering it as a validator purely through mining work.

## Public testnet

Aether is moving toward a public testnet. If you're reading this before that's live: the chain is currently devnet-only, running on the maintainer's own machine, not yet reachable by outside nodes. Watch the wiki and this README for updates on real network details (seed nodes, genesis file, chain-id) once the public testnet is live.

## Contributing / Development Notes

This project has an unusually well-documented debugging history — several genuinely subtle Cosmos SDK / CometBFT integration bugs were found and fixed along the way (silent Go interface-satisfaction failures in genesis dispatch, a permanently-exempt bootstrap validator, a store version-consistency issue, address codec gaps, and a real double-reversal bug in AuxPoW's Merkle verification caught specifically through live testing). Every major design decision — the consensus model, the AuxPoW architecture, the post-quantum signature scheme, the liveness-detection mechanism, and the project's funding model — has a full, honest written decision record in the wiki, including alternatives considered and rejected. Read those before making changes to `app.go`, `x/pow`, or `crypto/mldsa` — several of these bugs are the kind that compile cleanly and fail silently or only under real, live conditions.

## Security

This project has not undergone a professional third-party security audit. The maintainer welcomes community review, bug reports, and independent researcher attention — see open issues or reach out directly. If you are a security researcher or firm interested in reviewing this project, a consolidated project summary and honest known-gaps document is available in this repo to help scope an engagement.
