# Agent & Bot Integration

Aether treats humans and software agents the same at the base layer: the same bech32 accounts, the same ML-DSA-44 transaction signatures, the same native PoW submissions, and the same epoch Top-K validator selection. There is no AI-only coin path and none is proposed here.

This document describes:

1. What an agent can do on public testnet **today**
2. Friction observed while operating miners/nodes programmatically
3. **Proposed** protocol and API features (roadmap - not shipped unless noted elsewhere)
4. A minimal agent participation loop
5. Security notes for agent operators

Canonical design detail remains in [WHITEPAPER.md](WHITEPAPER.md). Live endpoints and operator commands are in the root [README](../README.md).

**Just want an agent on the testnet?** See [AI agents: start here](../README.md#ai-agents-start-here): `go install github.com/whoyoujoshin/aether/cmd/agentmcp@main && agentmcp init`. The explorer serves the same information at `/agents`, and as JSON at `/api/agents`.

## Today (what works on testnet)

| Capability | Status |
|------------|--------|
| Create / fund `aether1...` accounts (ML-DSA-44) | Available |
| Sync `aetherd`, expose RPC/gRPC | Available |
| Register ed25519 consensus pubkey + PoP (`validatorkeygen` then `tx pow register-validator-pubkey`) | Available |
| Native Scrypt mining + submit (`powminer`) for **epoch native work** | Available |
| AuxPoW rewards (does **not** count toward Top-K) | Available |
| Query balances / txs via RPC, gRPC, explorer | Available |
| Faucet `POST /request` with `{"address":"aether1..."}` | Available (rate-limited) |

**Public testnet (verify against README if drifted):**

| | |
|--|--|
| Chain ID | `aether-testnet-1` |
| Seed | `dfa6aae4b7bfd5b0eb1e22fabbae3e83a475b938@157.245.252.221:26656` |
| RPC | `http://157.245.252.221:26657` |
| gRPC | `157.245.252.221:9090` |
| Faucet | `http://157.245.252.221:8080/request` |
| Explorer | `http://157.245.252.221:8081` |

Epoch length is **1440** blocks; Top-K is **21** by epoch native work. Only **native** submissions increment Top-K standing.

## Gaps agents hit in practice

These are operational lessons from running automated miners and validators on testnet - not protocol bugs by themselves:

1. **Key custody** - test keyrings and visible terminals expose full signing power; there is no first-class spend policy or session key for bots.
2. **Observability** - eligibility and confirmations are often inferred by scraping `powminer` logs rather than a stable "PoW count this epoch / registered? / selected?" API or webhook.
3. **Funding** - faucet rate limits and manual bank sends are awkward for fleets of agent wallets.
4. **Headless ops** - some seed/admin steps still assume a human console; agents prefer authenticated remote control of their own miner/validator processes.
5. **Error semantics** - wait-windows, dual-miner races, and reject codes need stable, machine-readable surfaces (agents retry blindly when logs are the only signal).

## Recommended features (proposals)

> Everything in this section is a **proposal / roadmap item** unless an implementation already lands in-tree. Do not assume these modules exist on-chain today.

### 1. Scoped agent wallets and policy (highest ROI)

- Keys in OS keychain / HSM / vault - not mnemonics in chat or shell history
- Spend limits, allowlists (tx types / peers), and an operator kill-switch
- Capability or session keys (e.g. "may submit PoW and pay <= X / day") without handing over the root account

### 2. First-class machine APIs

Stable gRPC/REST (and optionally webhooks) for:

- Current epoch index and boundary height
- Whether a miner address has a registered consensus pubkey
- Native PoW / work count for an address in the current epoch
- Current active Top-K set
- Tx inclusion notifications (SubmitPoW confirmed; selected at epoch boundary)

Plus idempotent tx submit helpers with clear application codes for wait-window and duplicate-work rejects.

### 3. Agent-to-agent money primitives

- Escrow / conditional release (height, proof, or oracle)
- Invoices and optional micropayment streams for tool calls, inference, or bandwidth
- Optional **permissionless** agent registry (pubkey <-> metadata) - still no privileged AI lane

### 4. Headless operator surface

- Authenticated control of *your* miner/validator processes (start/stop/health) without a desktop session
- Health checks + supervised auto-restart when selected into Top-K
- Bot-friendly faucet or drip APIs with explicit rate-limit headers

### 5. Keep the base layer boring

- Same addresses and messages for humans and agents
- Add **policy + APIs + escrow** above the PoW / Top-K core - do not invent a parallel "agent coin"
- ML-DSA-44 from genesis already helps long-lived bot keys; do not weaken that

## Minimal agent loop

1. **Create and fund** an `aether1...` account (disposable test funds only on testnet).
2. **Run or sync** `aetherd` (or carefully use public RPC for queries; prefer your own node for validation).
3. **Register** the node's ed25519 consensus pubkey with PoP (`validatorkeygen`, then the printed `tx pow register-validator-pubkey`).
4. **Mine native PoW** each epoch with `powminer` (auto-submit / loop). Accumulate at least one accepted native submission before the epoch boundary if you intend Top-K eligibility.
5. **Stay online** if selected: Top-K validators must sign; downtime rules can remove you temporarily.

Remember: AuxPoW can pay and retarget but **does not** build Top-K standing.

## Security notes for agent operators

- Never place mainnet mnemonics or root keys in chat logs, tickets, or unencrypted disks
- Prefer dedicated hot wallets with tight caps for autonomous loops
- Monitor wait-windows, downtime removal, equivocation bans, and any height-gated behavior described in the whitepaper / wiki
- Treat public RPC as untrusted input; verify critical state on a node you control when decisions are high-stakes
- This software is **unaudited** early-stage - see the README status table and wiki known issues

## Related docs

- [Technical Whitepaper](WHITEPAPER.md)
- Root [README](../README.md) - testnet endpoints, validator registration, `powminer`
- Wiki: Architecture, Phase 1 Multi-Validator Selection, Known Issues

