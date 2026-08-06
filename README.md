# Aether (AETH)

Aether is a sovereign, staking-free proof-of-work blockchain built on Cosmos SDK and CometBFT. Core design principle: **fair launch via mining, no early validator or founder dominance** — validators are chosen entirely by real, tracked mining work, not capital or staking, and mining rewards flow to everyday participants rather than a pre-mined or founder-allocated supply.

> \\\*\\\*⚠️ This project has not undergone a professional, independent security audit.\\\*\\\* It is early-stage software. Do not use it with real funds you cannot afford to lose entirely. See \\\[Known Issues and Technical Debt](../../wiki/Known-Issues-and-Technical-Debt) and the security review prep document in this repo for an honest account of what has and hasn't been independently reviewed.

Full design history, live-verification write-ups, and every locked architectural decision (with complete reasoning) are tracked in the [project wiki](../../wiki).

## Current Status

|Area|Status|
|-|-|
|Core PoW mining, Scrypt hashing, difficulty retargeting, height-based reward decay and tail emission|✅ Built, tested, live-verified|
|Epoch-based Top-K validator selection (no staking module)|✅ Built, tested, live-verified on a 2-node network|
|Validator bonding, equivocation slashing, automatic escrow release|✅ Built, tested, live-verified with real constructed equivocation evidence|
|Validator liveness (downtime) detection — distinct from equivocation|✅ Built, tested, live-verified|
|Ancestor validation (prevents work-inflation via unvalidated headers)|✅ Built, tested, live-verified|
|AuxPoW (merged mining) — self-verifying, Litecoin/Dogecoin-family|✅ Built, tested, live-verified against primary-source binary formats|
|Post-quantum signatures (ML-DSA-44 / Dilithium2), mandatory from genesis|✅ Built, tested, live-verified — real keys, real ante-handler enforcement|
|Full governance module (deposit-gated proposals, tenure-weighted voting, treasury execution, full query service)|✅ Built, tested, live-verified|
|`uaeth` sub-unit (1,000,000 uaeth = 1 aeth) and `aether` bech32 address prefix|✅ Built, migrated, live-verified|
|Wallet library and CLI (account management, chain queries, send)|✅ Built, tested, live-verified|
|Testnet faucet and minimal block explorer|✅ Built and live-verified — currently run locally, not yet deployed publicly (see note below)|
|**Public testnet**|✅ **Live** — see below|
|Account abstraction, native IBC|⬜ Not built|
|Independent professional security audit|⬜ Not yet performed|

See [Known Issues and Technical Debt](../../wiki/Known-Issues-and-Technical-Debt) for an honest, detailed accounting of every known gap and deferred item, and [Roadmap](../../wiki/Roadmap) for what's next.

## Why staking-free, and why post-quantum?

Most Cosmos SDK chains derive their validator set from `x/staking` — token holders bond capital, delegate to validators, and voting power follows stake. Aether has no staking module at all. Validators are chosen by **real, tracked mining work**, not capital — influence is earned, not bought or pre-allocated.

Aether also requires **ML-DSA-44 (Dilithium2, NIST FIPS 204) post-quantum signatures for every account transaction, from genesis, with no classical-signature fallback** — a deliberate choice to build quantum resistance into the chain's identity from day one rather than retrofit it later. See the design decision records in the wiki for the full reasoning, including why ML-DSA was chosen over Falcon, and the real, current cryptographic limitations around key derivation that shaped the keyring design.

## Public testnet

Aether's public testnet is live.

* **Chain ID**: `aether-testnet-1`
* **Seed node**: `dfa6aae4b7bfd5b0eb1e22fabbae3e83a475b938@157.245.252.221:26656`
* **RPC endpoint**: `http://157.245.252.221:26657`
* **gRPC endpoint**: `157.245.252.221:9090`
* **Genesis file**: [`testnet/genesis.json`](testnet/genesis.json)

### Connecting a node

```powershell
aetherd init <your-moniker> --chain-id aether-testnet-1
```

Replace the freshly-generated `config/genesis.json` with [`testnet/genesis.json`](testnet/genesis.json) from this repo, then edit `config/config.toml` and set:

```toml
seeds = "dfa6aae4b7bfd5b0eb1e22fabbae3e83a475b938@157.245.252.221:26656"
```

Then start your node as usual:

```powershell
aetherd start
```



\*\*If you want your node reachable by others\*\* (not just as a local client of the network), note that CometBFT's RPC server and the SDK's gRPC server both default to binding only to `localhost`, regardless of firewall rules. Edit `config/config.toml` and `config/app.toml` before starting:



```toml

\\# config/config.toml, under \\\[rpc]

laddr = "tcp://0.0.0.0:26657"

```



```toml

\\# config/app.toml, under \\\[grpc]

address = "0.0.0.0:9090"

```



This is a real, easy-to-miss gap — the firewall being open is necessary but not sufficient; the server itself also needs to actually bind to a public-facing address.



**This is a real, early-stage public network — not audited, and subject to resets.** Only use test funds you're comfortable losing entirely. See the disclosure notice at the top of this README.

**Note on the faucet and explorer**: both are real, working, tested tools (see below), but currently run only locally by the maintainer — there is not yet an always-on, publicly-hosted faucet or explorer endpoint. This is a known, honest gap, not an oversight; deploying them persistently alongside the seed node is a natural next step.



\*\*A related historical note\*\*: for roughly the first 10 hours of this testnet's life, `timeout\_commit` was still at CometBFT's own default (\~5 seconds), not the intended 60 seconds — meaning the chain produced blocks about 10x faster than designed during that window. This was caught, confirmed via real before/after timing measurements, and fixed live (see commit history). The practical effect: block height on this network is noticeably higher than you'd expect for its actual real-world age, since roughly 7,000 blocks accumulated in that first \~10-hour period alone. Not a consensus or security issue, just a real, disclosed quirk of this network's specific history.



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
aetherd query governance params
aetherd query governance proposals

# Create a real ML-DSA-44 key -- works naturally, no special flags needed
aetherd keys add mywallet --keyring-backend test

# Mine and submit a real block (queries live chain state automatically)
go run ./cmd/powminer --miner <your-bech32-address>
# ...then run the aetherd tx pow submit command it prints
```

**Note on keys:** every Aether account is an independent ML-DSA-44 keypair — there is no hierarchical (HD) derivation. A mnemonic backs up exactly one key; it does not derive multiple accounts the way Bitcoin/Ethereum-style wallets do. This reflects a genuine, current limitation in post-quantum cryptography, not a convenience choice — see the wiki for the full explanation.

**Note on addresses:** Aether uses its own `aether1...` bech32 prefix (not the generic Cosmos SDK default) and its own `uaeth` base denomination (1,000,000 `uaeth` = 1 display `aeth`), matching the standard Cosmos convention (e.g. `uatom`/`atom`).

## Wallet

A CLI wallet, backed by a reusable Go library (`wallet/`), for anyone who doesn't want to use `aetherd`'s own lower-level commands directly:

```powershell
go run ./cmd/wallet create mywallet --keyring-backend test
go run ./cmd/wallet balance mywallet --grpc localhost:9090
go run ./cmd/wallet send mywallet <recipient-address> 1000000uaeth --keyring-backend test --grpc localhost:9090 --chain-id aether-testnet-1
```

`balance` and `send` accept either a real bech32 address or a keyring account name.

## Faucet

A rate-limited HTTP faucet for dispensing small amounts of testnet `aeth`:

```powershell
go run ./cmd/faucet --from faucet --chain-id aether-testnet-1 --keyring-backend test
```

```powershell
Invoke-WebRequest -Uri http://localhost:8080/request -Method POST -Body '{"address":"aether1..."}' -ContentType "application/json"
```

Requires a real, funded key named `faucet` in the keyring it's pointed at (via `--keyring-backend`/`--home`).

## Block explorer

A minimal, live, server-rendered dashboard showing real chain state (height, difficulty, block reward, active validators, treasury balance, governance proposals):

```powershell
go run ./cmd/explorer --grpc localhost:9090 --rpc http://localhost:26657 --port 8081
```

Then open `http://localhost:8081` in a browser.

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

## Block reward schedule

Block rewards decay on a real, height-based schedule (deterministic, no wall-clock dependency): 5.00 AETH at genesis, decaying \~34% per year (discrete yearly steps) for 8 years, then a permanent 0.20 AETH tail. See `tail-emission-decision.md` in the wiki for the full derivation, including a correction of an earlier, mathematically-inconsistent draft figure and an honest accounting of the real (not a flattering cherry-picked) long-term inflation rate.

## Governance \& Treasury

Aether has a full, working governance system: anyone can submit a deposit-gated treasury-spend proposal (25,000,000 uaeth minimum deposit), active validators vote with tenure-weighted power (a 30-day ramp rewarding sustained, continuous participation), and passing proposals (60% quorum, 2/3 threshold, with a 1/3 veto override) automatically execute a real transfer from the treasury.

```powershell
aetherd tx governance submit-proposal <recipient> <amount> <deposit> --from <key> --chain-id aether-testnet-1
aetherd tx governance vote <proposal-id> yes --from <key> --chain-id aether-testnet-1
aetherd query governance proposal <proposal-id>
```

## Repo Layout

* `x/pow` — the core module: mining verification (Scrypt + AuxPoW), difficulty retargeting, height-based reward decay, reward distribution, validator selection/bonding/slashing/liveness, ancestor validation
* `x/governance` — deposit-gated proposals, tenure-weighted voting, quorum/threshold resolution, full query service
* `x/treasury` — single source of truth for spendable community funds, spent only via governance-authorized proposals
* `crypto/mldsa` — the ML-DSA-44 post-quantum signature scheme: real keys, ADR-028 addressing, keyring integration
* `wallet/` — CLI/UI-agnostic Go library: account management, chain queries, transaction construction/signing/broadcasting
* `cmd/aetherd` — the node binary
* `cmd/wallet` — thin CLI wrapper over the wallet library
* `cmd/faucet` — rate-limited testnet faucet HTTP service
* `cmd/explorer` — minimal live block explorer dashboard
* `cmd/powminer` — standalone tool that queries real chain state and brute-forces a valid native PoW nonce
* `cmd/auxpowtest` — standalone tool that constructs a real, valid test AuxPoW (merged-mining) submission
* `cmd/scryptbench` — benchmarks real Scrypt hash throughput, used to responsibly retune difficulty constants
* `cmd/validatorkeygen` — generates/loads a consensus keypair and produces the registration proof-of-possession signature
* `cmd/balancecheck` — gRPC bank balance checker (works around `bank`'s query CLI not being fully wired via autocli in this SDK version)
* `cmd/equivocationtest` — constructs real, cryptographically valid equivocation evidence, used to live-verify the slashing path
* `testnet/genesis.json` — the real, live public testnet's genesis file

## Multi-node devnet

See the [Architecture](../../wiki/Architecture) and [Phase 1](../../wiki/Phase-1-Multi-Validator-Selection) wiki pages for how dynamic validator onboarding works, including a full worked example of standing up a second node with real P2P peering and registering it as a validator purely through mining work.

## Contributing / Development Notes

This project has an unusually well-documented debugging history — several genuinely subtle Cosmos SDK / CometBFT integration bugs were found and fixed along the way (silent Go interface-satisfaction failures in genesis dispatch, a permanently-exempt bootstrap validator, a store version-consistency issue, address codec gaps, a real double-reversal bug in AuxPoW's Merkle verification, a silently-ignored gRPC transaction-broadcast service stub, and several independently-hardcoded bech32 prefix references caught only when the full toolchain was re-verified live after the `aether` prefix migration). Every major design decision — the consensus model, the AuxPoW architecture, the post-quantum signature scheme, the liveness-detection mechanism, the tail-emission schedule, and the project's funding model — has a full, honest written decision record in the wiki, including alternatives considered and rejected. Read those before making changes to `app.go`, `x/pow`, or `crypto/mldsa` — several of these bugs are the kind that compile cleanly and fail silently or only under real, live conditions.

## Security

This project has not undergone a professional third-party security audit. The maintainer welcomes community review, bug reports, and independent researcher attention — see open issues or reach out directly. If you are a security researcher or firm interested in reviewing this project, a consolidated project summary and honest known-gaps document is available in this repo to help scope an engagement.

