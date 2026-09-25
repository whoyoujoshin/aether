# Aether (AETH)

Aether is a sovereign, staking-free proof-of-work blockchain built on Cosmos SDK and CometBFT. Validators are selected by real, tracked **native mining work** — not bonded capital — and account transactions require **ML-DSA-44 (Dilithium2 / FIPS 204)** signatures from genesis.

**Technical design:** see the full [Technical Whitepaper](docs/WHITEPAPER.md).

> **Warning: no independent security audit.** This is early-stage software. Do not use it with funds you cannot afford to lose. See [Known Issues and Technical Debt](../../wiki/Known-Issues-and-Technical-Debt) and the security review materials in this repo for an honest account of what has and has not been independently reviewed.

Design history, live-verification notes, and locked architectural decisions are tracked in the [project wiki](../../wiki).

## Current status

| Area | Status |
|------|--------|
| Core PoW (Scrypt), difficulty retarget, height-based reward decay and tail emission | Built, tested, live-verified |
| Epoch Top-K validator selection (no staking module) | Built, tested, live-verified |
| Validator bonding, equivocation slashing, escrow release | Built, tested, live-verified |
| Downtime / liveness detection (distinct from equivocation) | Built, tested, live-verified |
| Ancestor validation | Built, tested, live-verified |
| AuxPoW (LTC/DOGE-family merged mining) | Built, tested, live-verified |
| Post-quantum account signatures (ML-DSA-44), mandatory from genesis | Built, tested, live-verified |
| Governance (deposit, tenure-weighted voting, treasury execution) | Built, tested, live-verified |
| `uaeth` / `aether` bech32 prefix | Built, migrated, live-verified |
| Wallet library and CLI | Built, tested, live-verified |
| Testnet faucet and block explorer | Built, live, deployed with seed node |
| **Public testnet** | **Live** — see below |
| Account abstraction, native IBC | Not built |
| Independent professional security audit | Not yet performed |

See [Known Issues and Technical Debt](../../wiki/Known-Issues-and-Technical-Debt) and [Roadmap](../../wiki/Roadmap).

## Why staking-free, and why post-quantum?

Aether has no `x/staking` module. The active validator set is the Top-K miners by **epoch native work**. AuxPoW can earn rewards and retarget difficulty but does **not** count toward Top-K standing.

Every account transaction must use ML-DSA-44 from genesis (no classical fallback), enforced by `PostQuantumDecorator`. CometBFT consensus keys remain ed25519. Details: [docs/WHITEPAPER.md](docs/WHITEPAPER.md) and the wiki decision records.

## Public testnet

| | |
|--|--|
| **Chain ID** | `aether-testnet-1` |
| **Seed** | `dfa6aae4b7bfd5b0eb1e22fabbae3e83a475b938@157.245.252.221:26656` |
| **RPC** | `http://157.245.252.221:26657` |
| **gRPC** | `157.245.252.221:9090` |
| **Faucet** | `http://157.245.252.221:8080/request` — `POST` JSON `{"address":"aether1..."}` |
| **Explorer** | `http://157.245.252.221:8081` |
| **Genesis** | [`testnet/genesis.json`](testnet/genesis.json) |

### Connecting a node

```bash
aetherd init <your-moniker> --chain-id aether-testnet-1
```

Replace `config/genesis.json` with [`testnet/genesis.json`](testnet/genesis.json), set in `config/config.toml`:

```toml
seeds = "dfa6aae4b7bfd5b0eb1e22fabbae3e83a475b938@157.245.252.221:26656"
```

Then:

```bash
aetherd start
```

To expose RPC/gRPC publicly (defaults bind localhost only), before start:

```toml
# config/config.toml, [rpc]
laddr = "tcp://0.0.0.0:26657"
```

```toml
# config/app.toml, [grpc]
address = "0.0.0.0:9090"
```

This is an early-stage public network — not audited, subject to resets. Use only disposable test funds.

**Historical note:** for roughly the first ~10 hours, `timeout_commit` remained near CometBFT’s ~5s default instead of the intended ~60s, so height grew faster than wall-clock age (~7,000 blocks in that window). Fixed live; not a consensus/security failure — disclosed for operators interpreting height vs age.

## Quick start (single-node devnet)

```bash
# Build
go build ./...
go install -mod=mod ./cmd/aetherd

# Initialize (once, or after full reset)
aetherd init mynode --chain-id aether-testnet-1

# Start (foreground)
aetherd start
```

Second terminal:

```bash
aetherd query pow difficulty
aetherd query pow block-reward
aetherd query pow active-validators
aetherd query pow current-epoch
aetherd query governance params
aetherd query governance proposals

# ML-DSA-44 key (no special flags)
aetherd keys add mywallet --keyring-backend test

# Mine a valid native PoW nonce against live state
go run ./cmd/powminer --miner <your-bech32-address>
# then run the aetherd tx pow submit command it prints
```

**Keys:** each account is one ML-DSA-44 keypair — no HD multi-account derivation from a mnemonic.  
**Addresses / denom:** `aether1...` bech32; `uaeth` base unit (1 aeth = 1_000_000 uaeth).

Binary home defaults to `~/.aether`.

## Wallet

```bash
go run ./cmd/wallet create mywallet --keyring-backend test
go run ./cmd/wallet balance mywallet --grpc localhost:9090
go run ./cmd/wallet send mywallet <recipient> 1000000uaeth --keyring-backend test --grpc localhost:9090 --chain-id aether-testnet-1
```

`balance` and `send` accept a bech32 address or keyring account name.

## Faucet

```bash
go run ./cmd/faucet --from faucet --chain-id aether-testnet-1 --keyring-backend test
```

```bash
curl -X POST http://localhost:8080/request \
  -H 'Content-Type: application/json' \
  -d '{"address":"aether1..."}'
```

Requires a funded key named `faucet` in the configured keyring.

## Block explorer

```bash
go run ./cmd/explorer --grpc localhost:9090 --rpc http://localhost:26657 --port 8081
```

Open `http://localhost:8081`.

## AI agent wallet (MCP)

An MCP server exposing wallet operations (balance, send, tx status/history) as tool calls, so an AI agent can transact directly instead of only a human clicking through a UI:

```bash
go run ./cmd/agentmcp --grpc localhost:9090 --chain-id aether-testnet-1 \
    --per-tx-limit 1000000 --daily-limit 5000000
```

Speaks MCP over stdio. Manages one dedicated agent account (created on first use) with a per-transaction cap and a rolling 24h spend cap enforced by the server itself — **not yet enforced on-chain**. Read `cmd/agentmcp/main.go`'s package doc comment before pointing this at anything but a small, disposable balance.

### On-chain agent permissions (x/authz, x/feegrant)

From `app.AuthzFeegrantActivationHeight`, an account can grant another account (an agent) a scoped, expiring, chain-enforced permission — e.g. "send up to 1 AETH from my account until Friday" — and optionally pay its fees:

```bash
aetherd tx authz grant <agent-address> send --spend-limit 1000000uaeth --expiration <unix-ts> --from <you>
aetherd tx feegrant grant <you> <agent-address> --spend-limit 100000uaeth --from <you>
aetherd tx authz revoke <agent-address> /cosmos.bank.v1beta1.MsgSend --from <you>
```

Nodes running this binary halt once at the activation height (`CONSENSUS FAILURE`, block not committed) and must be restarted (`systemctl restart aetherd`) to add the two new stores; see `app/authz_feegrant.go` for why.

## Registering as a validator

```bash
go run ./cmd/validatorkeygen --miner <your-bech32-address>
# then run the aetherd tx pow register-validator-pubkey command it prints
```

Mine and submit successfully within an epoch to accumulate native work. At the epoch boundary (1440 blocks), Top-K (21) by native work become the active set. Downtime (>50% missed signatures in a 60-block window) causes temporary removal; equivocation causes permanent ban and escrow burn.

## Merged mining (AuxPoW)

Litecoin/Dogecoin-family AuxPoW (chain ID **17776**) may satisfy PoW and earn the reward + retarget difficulty. **Only native work counts toward Top-K.** See `cmd/auxpowtest` and the whitepaper.

## Block reward schedule

- Genesis: **5_000_000 uaeth**
- Decay factor **0.66** for **8** years (height-based yearly steps)
- Tail: **200_000 uaeth**
- **15%** cut (validators escrowed / treasury)

See the whitepaper and wiki tail-emission notes for derivation details.

## Governance and treasury

| Parameter | Value |
|-----------|--------|
| Min deposit | 25_000_000 uaeth |
| Deposit period | 14 days |
| Voting period | 7 days |
| Quorum | ceil(0.6 × Top-K) |
| Pass | 2/3 non-abstain |
| Veto | 1/3 |
| Tenure ramp | 30 days |

```bash
aetherd tx governance submit-proposal <recipient> <amount> <deposit> --from <key> --chain-id aether-testnet-1
aetherd tx governance vote <proposal-id> yes --from <key> --chain-id aether-testnet-1
aetherd query governance proposal <proposal-id>
```

## Repo layout

| Path | Role |
|------|------|
| `x/pow` | Mining (Scrypt + AuxPoW), difficulty, rewards, validators, slashing, liveness |
| `x/governance` | Proposals, tenure-weighted voting, queries |
| `x/treasury` | Community funds; governance-authorized spends |
| `crypto/mldsa` | ML-DSA-44, ADR-028 addresses, keyring / ante |
| `wallet/` | Account management, queries, tx construction |
| `app/` | App wiring; `authz_feegrant.go` gates x/authz + x/feegrant activation |
| `cmd/aetherd` | Node binary |
| `cmd/wallet` | CLI over `wallet/` |
| `cmd/faucet` | Rate-limited faucet |
| `cmd/explorer` | Minimal live explorer |
| `cmd/agentmcp` | MCP server exposing the wallet as tool calls, for AI agents |
| `cmd/powminer` | Native PoW nonce search against live state |
| `cmd/auxpowtest` | Valid test AuxPoW construction |
| `cmd/scryptbench` | Scrypt throughput benchmarks |
| `cmd/validatorkeygen` | Consensus key + PoP for registration |
| `cmd/balancecheck` | gRPC bank balance helper |
| `cmd/equivocationtest` | Constructed equivocation evidence |
| `docs/WHITEPAPER.md` | Technical whitepaper |
| `testnet/genesis.json` | Live public testnet genesis |

## Multi-node and further docs

- [Technical Whitepaper](docs/WHITEPAPER.md) — system design, economics, crypto, security limitations (incl. BFT/Top-K and height gates)
- [Agent & Bot Integration](docs/AGENT_INTEGRATION.md) — participating as software agents (current surface + proposed APIs/policy)
- Wiki: [Architecture](../../wiki/Architecture), [Phase 1 Multi-Validator Selection](../../wiki/Phase-1-Multi-Validator-Selection), [Known Issues](../../wiki/Known-Issues-and-Technical-Debt)

## Contributing

Major design decisions and subtle Cosmos SDK / CometBFT integration fixes are documented in the wiki. Read those before changing `app/`, `x/pow`, or `crypto/mldsa`.

## Security

No professional third-party audit yet. Community review and responsible disclosure are welcome via issues. Researchers scoping an engagement should use the wiki known-gaps materials and this README’s status table.
