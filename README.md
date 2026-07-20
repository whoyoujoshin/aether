# Aether (AETH)

Aether is a sovereign, staking-free proof-of-work blockchain built on Cosmos SDK and CometBFT. Core design principle: **fair launch via mining, no early validator or founder dominance** — validators are chosen entirely by real, tracked mining work, not capital or staking.

Full design history, live-verification write-ups, and open questions are tracked in the [project wiki](../../wiki).

## Current Status

| Area | Status |
|---|---|
| Core PoW mining, difficulty retargeting, reward distribution | ✅ Built, tested, live-verified |
| Epoch-based Top-K validator selection (no staking module) | ✅ Built, tested, live-verified on a 2-node network |
| Validator bonding, equivocation slashing, automatic escrow release | ✅ Built, tested, live-verified with real constructed equivocation evidence |
| Ancestor validation (prevents work-inflation via unvalidated headers) | ✅ Built, tested, live-verified |
| `x/pow` query CLI | ✅ Built, live-verified |
| Scrypt / AuxPoW consensus upgrade | ⬜ Not started — current PoW hash function is SHA-256, a deliberate placeholder |
| Post-quantum signatures | ⬜ Stubbed only |
| Treasury / governance real logic | ⬜ Modules exist, logic minimal |

See [Known Issues and Technical Debt](../../wiki/Known-Issues-and-Technical-Debt) for an honest accounting of what's not finished or not clean, and [Roadmap](../../wiki/Roadmap) for what's next.

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

# Mine and submit a real block (queries live chain state automatically)
go run ./cmd/powminer --miner <your-bech32-address>
# ...then run the aetherd tx pow submit command it prints
```

## Registering as a validator

Validators are chosen by real mining work, not staking. To become eligible:

```powershell
# Generate (or load an existing) consensus keypair and the required
# proof-of-possession signature
go run ./cmd/validatorkeygen --miner <your-bech32-address>
# ...then run the aetherd tx pow register-validator-pubkey command it prints
```

Once registered, mine and submit successfully within an epoch to accumulate ranked work — at the next epoch boundary, the top-K addresses by recorded work become the active validator set.

## Repo Layout

- `x/pow` — the core module: mining verification, difficulty retargeting, reward distribution, and (as of this project's later phases) the entire validator selection, bonding, and slashing system
- `x/treasury`, `x/governance` — scaffolded, minimal logic (see wiki for current status)
- `cmd/aetherd` — the node binary
- `cmd/powminer` — standalone tool that queries real chain state and brute-forces a valid nonce
- `cmd/validatorkeygen` — generates/loads a consensus keypair and produces the registration proof-of-possession signature
- `cmd/balancecheck` — gRPC bank balance checker
- `cmd/equivocationtest` — constructs real, cryptographically valid equivocation evidence, used to live-verify the slashing path

## Multi-node devnet

See the [Architecture](../../wiki/Architecture) and [Phase 1](../../wiki/Phase-1-Multi-Validator-Selection) wiki pages for how dynamic validator onboarding works, and the project's own session history for a full worked example of standing up a second node (separate home directory, distinct ports, real P2P peering) and registering it as a validator purely through mining work.

## Contributing / Development Notes

This project has an unusually well-documented debugging history — several genuinely subtle Cosmos SDK / CometBFT integration bugs were found and fixed along the way (silent Go interface-satisfaction failures, store version-consistency issues, address codec gaps, and more). Worth reading the wiki's [Architecture](../../wiki/Architecture) page and [Known Issues and Technical Debt](../../wiki/Known-Issues-and-Technical-Debt) before making changes to `app.go` or `x/pow`'s genesis/store wiring — several of these bugs are the kind that compile cleanly and fail silently.
