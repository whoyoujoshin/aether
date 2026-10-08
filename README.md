# Aether (AETH)

Aether is a sovereign, staking-free proof-of-work blockchain built on Cosmos SDK and CometBFT. Validators are selected by real, tracked **native mining work** — not bonded capital — and account transactions require **ML-DSA-44 (Dilithium2 / FIPS 204)** signatures from genesis.

**Technical design:** see the full [Technical Whitepaper](docs/WHITEPAPER.md).

> **Warning: no independent security audit.** This is early-stage software. Do not use it with funds you cannot afford to lose. See [Known Issues and Technical Debt](../../wiki/Known-Issues-and-Technical-Debt) and the security review materials in this repo for an honest account of what has and has not been independently reviewed.

Design history, live-verification notes, and locked architectural decisions are tracked in the [project wiki](../../wiki).

## AI agents: start here

Agents use the same accounts and transactions as people: there is no AI-only lane. **Testnet only: use disposable keys, never anything of value.**

**One page from nothing to a first payment: [docs/START.md](docs/START.md)** (the explorer serves it too, at [`/start.md`](https://explorer.157-245-252-221.sslip.io/start.md)). A key, test funds, a balance and a send, copy-paste for an MCP agent, TypeScript or Python. What won't change under you: [API stability](docs/API-STABILITY.md).

```bash
go install github.com/whoyoujoshin/aether/cmd/agentmcp@latest   # Go 1.25+
agentmcp init   # new key, testnet funds, and the MCP config for Claude / Cursor / any MCP client
```

No Go? `agentmcp` is in every platform's archive on the [releases page](../../releases). `@latest` is the newest tested release; `@main` has unreleased changes.

That gives the agent a spend-capped wallet as MCP tools: balance, send, invoice and wait-for-payment, paying for HTTP 402 APIs, and the service directory (full list and guarantees in [AI agent wallet](#ai-agent-wallet-mcp)).

| | |
|--|--|
| Chain ID | `aether-testnet-1` · denom `uaeth` (1 AETH = 10⁶ uaeth) · addresses `aether1...` |
| RPC / gRPC | `https://rpc.157-245-252-221.sslip.io` / `grpc.157-245-252-221.sslip.io:443` (TLS; plain `157.245.252.221:26657`/`:9090` still work) |
| Faucet | `curl -X POST https://faucet.157-245-252-221.sslip.io/request -H 'Content-Type: application/json' -d '{"address":"aether1..."}'` |
| Explorer | `https://explorer.157-245-252-221.sslip.io/agents` · balance: `/api/address?addr=aether1...` · this card as JSON: `/api/agents` · every endpoint: `/api/openapi.json` · for LLMs: `/llms.txt` · start page: `/start.md` |

Let an agent spend from your account with a chain-enforced cap instead of holding funds: [agent permissions](#on-chain-agent-permissions-xauthz-xfeegrant). Sell to agents: [paid APIs](#paid-apis-x402). See one agent pay another for a tool call, live, in [docs/AGENT_DEMO.md](docs/AGENT_DEMO.md). A prompt to check an agent is set up (the address is a test counterparty run by the project):

> Using the aether-wallet tools: get your address and balance. If you have under 1 AETH, call request_testnet_funds and wait until your balance shows it. Then send 0.001 AETH to aether1cdugwhxk9cktjsemm6yjrd6xtfsq9wkjvnef03ml4u6ltuv7edcs0eyjds with idempotencyKey "aether-smoke-1", wait for the transaction to confirm, and report its hash and https://explorer.157-245-252-221.sslip.io/tx/<hash>.

## Current status

| Area | Status |
|------|--------|
| Core PoW (Scrypt), difficulty retarget, height-based reward decay and tail emission | Built, tested, live-verified |
| Epoch Top-K validator selection (no staking module) | Built, tested, live-verified |
| Validator bonding, equivocation slashing, escrow release | Built, tested, live-verified |
| Downtime / liveness detection (distinct from equivocation) | Built, tested, live-verified |
| Ancestor validation | Built, tested, live-verified |
| AuxPoW (LTC/DOGE-family merged mining) | Built and tested; verified live only with synthetic proofs. Real pool work needs the chain changes and bridge in [docs/MERGED-MINING-PLAN.md](docs/MERGED-MINING-PLAN.md) |
| Post-quantum account signatures (ML-DSA-44), mandatory from genesis | Built, tested, live-verified |
| Governance (deposit, tenure-weighted voting, treasury execution) | Built, tested, live-verified |
| `uaeth` / `aether` bech32 prefix | Built, migrated, live-verified |
| Wallet library and CLI | Built, tested, live-verified |
| Testnet faucet and block explorer | Built, live, deployed with seed node |
| **Public testnet** | **Live** — see below |
| Native IBC (core, ICS-20 transfer, ICS-27 interchain accounts) | Built, tested, live-verified — activated at block 122,000; full client/connection/channel/transfer round trip relayed with Aether's own ML-DSA relayer, locally and on the live testnet |
| Account abstraction (session keys, guardian thresholds) | Built, tested, live-verified — activated at block 122,000 |
| Escrow between accounts (`x/escrow`: release, refund, arbiter, deadline) | Live since block 161,000 ([docs/ESCROW.md](docs/ESCROW.md)) |
| Ligase: someone on another chain funds, releases and withdraws Aether escrows with IBC transfers carrying an instruction; such instructions can never move AETH | Live since block 161,000; proven on a two-chain devnet ([docs/LIGASE.md](docs/LIGASE.md)) |
| Helicase: the block proposer relays IBC packets, acknowledgements and timeouts onto Aether, unsigned and proof-checked, so no relayer signs on Aether; `cmd/outbound` relays the other way with the other chain's own keys | Live since block 161,000; both directions proven unattended on a two-chain devnet ([docs/HELICASE.md](docs/HELICASE.md)) |
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
| **RPC** | `https://rpc.157-245-252-221.sslip.io` (plain `http://157.245.252.221:26657` still works) |
| **gRPC** | `grpc.157-245-252-221.sslip.io:443`, TLS (plain `157.245.252.221:9090` still works) |
| **Faucet** | `https://faucet.157-245-252-221.sslip.io/request` — `POST` JSON `{"address":"aether1..."}`; several at once: [Faucet](#faucet) |
| **Explorer** | `https://explorer.157-245-252-221.sslip.io` |
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
# (--fees 40uaeth if your node runs with --minimum-gas-prices=0.0001uaeth)
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

It's built for bots as well as people:

- `POST /request/batch` with `{"addresses":["aether1...", ...]}` funds up to `--batch-max` (10) addresses in one transaction and lists any it skipped.
- `GET /status?address=aether1...` says whether a request would be funded now, and when if not. `GET /` describes the faucet and its limits.
- Each address is funded once per `--cooldown-minutes` (60), and each caller (client IP) funds at most `--caller-limit` (20) addresses per `--caller-window-minutes` (60). Behind a reverse proxy, list it in `--trusted-proxies` (default: localhost) so `X-Forwarded-For` names the real caller; the header is ignored from anyone else.
- Every answer has a stable `code` (`sent`, `pending`, `address_cooldown`, `caller_limit`, `invalid_address`, `batch_too_large`, `invalid_request`, `send_failed`) and the caller's quota in `RateLimit-Limit`, `RateLimit-Remaining` and `RateLimit-Reset`; a 429 also has `Retry-After`. A failed send gives back both the cooldown and the quota.
- `--gas-price` (e.g. `0.0001uaeth`) pays fees for a node with `--minimum-gas-prices`.
- **Where requests come from.** Each drip is tagged by source and kept in `--drip-log` (JSON lines, default `<keyring dir>/faucet-drips.jsonl`), so the explorer's Faucet page can show who's creating wallets:
  - `web`: a browser that solved a small proof of work. `GET /challenge?address=` returns a challenge and `bits`; the page finds a nonce where `sha256(challenge + ":" + nonce)` starts with that many zero bits (`--pow-bits`, 18 by default, about a second) and sends `{"address", "pow":{"challenge","nonce"}}`.
  - `agent`: a request signed with a self-registered agent key. Register an ed25519 public key once with `POST /agents {"name":"my-bot","public_key":"<base64>"}` to get an `agent_id`, then send `X-Aether-Agent: <agent_id>`, `X-Aether-Agent-Timestamp: <unix seconds>` and `X-Aether-Agent-Signature: base64(ed25519(timestamp + "\n" + body))`. Agents get their own quota (`--agent-limit`, 100 per window) instead of their IP's; registry in `--agent-registry`.
  - `api`: neither, on the per-IP limit as before.
- A drip to an address the chain had no account for counts as a **new wallet** (single requests answer `"new_wallet": true`). `GET /stats` sums drips, wallets funded and created, sources over 30 days, new wallets per day and the top agents; `GET /drips?limit=` lists the newest. `"strand":"ibc"` answers `ibc_unavailable` until the Osmosis channel opens.

## Block explorer

```bash
go run ./cmd/explorer --grpc localhost:9090 --rpc http://localhost:26657 --port 8081
```

Open `http://localhost:8081`.

**The helix.** Add `--ibc-rpc <the RPC of the chain on the other end of Aether's transfer channel>`, and optionally `--ibc-name` to name it. The Overview and Blocks pages then draw both chains as the two strands of a helix. Aether's blocks run along one strand and the other chain's along the second. Each IBC packet between them is a rung, labelled in flight, received, acknowledged or timed out. A Both / Aether / other-chain switch sits in the top bar. With several connected chains, give `--ibc-rpc` and `--ibc-name` comma-separated lists in the same order (for example `--ibc-rpc https://rpc.osmotest5.osmosis.zone,https://testnet.sentry.tm.injective.network:443 --ibc-name Osmosis,Injective`): the switch then gets a picker for which one is the second strand, remembered per browser, and `/api/helix` lists them as `peers` and takes `?peer=` (a chain ID or name). `/api/helix` serves the data, read live from both chains' RPCs. Without `--ibc-rpc`, the Overview still draws Aether's strand live. The second strand is a faint ghost labelled "no IBC chain connected", and the rest of both pages stays as it was.

To redeploy the live explorer from `main`, run `bash scripts/deploy-explorer.sh` as root on the server that hosts it. It builds while the old version keeps serving, keeps backups, restarts `aether-explorer` and rolls back if the new one doesn't answer.


The Faucet page talks to the faucet through the explorer: run it with `--faucet-api http://127.0.0.1:8080` (the faucet must trust the explorer's address in `--trusted-proxies`, localhost by default). The Validators globe shows what `--node-locations` publishes; see [docs/EXPLORER-LOCATIONS.md](docs/EXPLORER-LOCATIONS.md).
## AI agent wallet (MCP)

An MCP server exposing wallet operations as tool calls, so an AI agent can pay and get paid directly instead of only a human clicking through a UI.

**Quick start (testnet):**

```bash
go install github.com/whoyoujoshin/aether/cmd/agentmcp@latest   # or, in a clone: go install ./cmd/agentmcp
agentmcp init
```

`init` creates the agent's account (showing its recovery phrase once), asks the testnet faucet for funds, waits until they arrive and prints the exact `claude mcp add ...` command and the JSON config block for Claude Desktop and other MCP clients. Run it again to reuse the same account. `--faucet <url>` points it at another faucet, `--no-faucet` skips funding; it takes the same `--grpc`, `--rpc`, `--chain-id` and `--keyring-dir` flags as the server (on `aether-testnet-1`, `--grpc` and `--rpc` default to the public node). Once running, the agent can top itself up with the `request_testnet_funds` tool (testnet only; `FAUCET_RATE_LIMITED` means wait).

**Claude Desktop without Go:** download `aether-wallet.mcpb` from the [latest release](../../releases) and open it. Claude Desktop asks for the spending limits and a keyring folder; the agent creates its account on first use and funds it with `request_testnet_funds`. The same bundle runs on Windows, macOS (Intel and Apple Silicon) and Linux. Its MCP Registry name is `io.github.whoyoujoshin/aether-wallet`.

To run the server by hand:

```bash
go run ./cmd/agentmcp --grpc localhost:9090 --rpc http://localhost:26657 --chain-id aether-testnet-1 \
    --per-tx-limit 1000000 --daily-limit 5000000 \
    [--granter <your-address> [--fee-granter <your-address>]]
```

**Public read-only URL** (no local package). `--http` serves Streamable HTTP at `/mcp` and `GET /healthz`. It is a different server from the wallet above: it never opens the keyring and does not register tools that spend, sign, create a key, or call the faucet. `get_balance`, `get_miner_status` and `get_account_authenticators` require an address. `get_transaction_status` looks a hash up. `find_services` is the service directory. Stdio, with the full wallet, stays the default when `--http` is omitted.

```bash
go run ./cmd/agentmcp --http 127.0.0.1:8090 \
    --grpc grpc.157-245-252-221.sslip.io:443 --chain-id aether-testnet-1
```

That listens on `http://127.0.0.1:8090/mcp`. Bind it to loopback. The explorer SPA serves `index.html` with HTTP 200 for unknown paths, so Caddy has to proxy `/mcp` to this process before that fallthrough — the snippet is commented in [`scripts/tls/Caddyfile`](scripts/tls/Caddyfile) and is not applied. The registry entry stays `io.github.whoyoujoshin/aether-wallet`; `agentmcp server-json --remote <url>` can add a `remotes` URL later, and omits it when the flag is empty. Neither the Caddy route nor a registry publish is done yet.

Built for how agents actually fail:

- **No double payments on retry.** `send_aeth` requires an `idempotencyKey`; a retry with the same key re-sends the identical signed transaction (its sequence number is signed in, so the chain can include it at most once) and returns its status.
- **Knows when a payment is final.** `send_aeth` returns `pending` once the node accepts it; `wait_for_transaction` waits until it's `confirmed` or `failed` in a block.
- **Can get paid.** `create_invoice` returns a unique memo and the current height; `wait_for_payment(memo, minAmount, sinceHeight)` waits for a confirmed incoming payment that matches. It reads every incoming payment since that height, page by page, so a busy agent can't miss one. Memos are sender-controlled, so tools label them as untrusted data.
- **Can buy from paid APIs.** `fetch_paid(url, maxAmount, idempotencyKey)` requests a URL; if the server answers HTTP 402 (see [Paid APIs](#paid-apis-x402)), it pays at most `maxAmount`, waits for the payment to confirm and returns the response. Retrying with the same key resumes the same payment, never a second one. For many requests to one service, add `pullAllowance` (e.g. `"1 AETH"`): if the service offers `aether-pull`, the agent grants it an on-chain allowance of that much (payable only to it, for 7 days, revocable, and approved by the owner like a payment of that size when it's over the approval threshold), then pays each request instantly by signature; the service collects what the agent owes later, so nothing is deposited with it. Or add `prepay` (e.g. `"1 AETH"`): the agent deposits that once and then pays each request instantly by signature — milliseconds instead of a block. `list_prepaid_balances` shows what's left where, and `withdraw_prepaid(service)` takes it back, from services that offer withdrawals.
- **Can find services, and tell good ones from bad.** `find_services(query, maxPrice)` lists paid APIs from the on-chain [service directory](#service-directory) with each one's reputation: recent payments and payers, ratings from paying accounts, ratings from accounts you trust (the owner, the agent itself, `--trust <addresses>`), and the agent's own history with it. `orderBy: "trusted"` puts what can't be faked first. `rate_service(url, score)` rates one it has bought from; `announce_service` lists one the agent runs.
- **Hires other agents through escrow** (once `x/escrow` is active: see [docs/ESCROW.md](docs/ESCROW.md)). `create_escrow` locks AETH for a payee until this agent or an arbiter releases it, the payee or arbiter refunds it, or its deadline settles it the way `onExpiry` says; it counts against the same limits and owner approvals as `send_aeth`, takes an idempotency key, and isn't available in grant mode (the chain caps grant spending only for plain sends). `release_escrow`, `refund_escrow`, `get_escrow` (open, or how and by whom it was settled, including at the deadline) and `list_escrows` cover the rest; the payee checks `get_escrow` before starting work.
- **Knows where it stands as a miner.** `get_miner_status` answers in one call, as of one block: whether the address has a registered consensus key, its work this epoch, its rank among eligible miners against the Top-K size, blocks and estimated seconds until the epoch's last block picks the next validator set, whether it's a validator now, and its escrowed rewards — no log scraping. The explorer serves the same at `/api/miner?addr=`.
- **Keeps receipts.** When a seller signs receipts, `fetch_paid` checks each one against exactly what was sent and received and returns it; `list_purchases` is the log of what the agent bought, with each receipt — proof anyone can check against the seller's address.
- **Answers to its owner.** With `--approval-threshold "0.5 AETH" --approver <owner-address>`, bigger payments wait (nothing signed or sent) until the owner runs `agentmcp approve <id>`, which signs the decision with the **owner's** key — so the agent can't approve itself even if it can write files on the machine. `agentmcp approvals` lists what's waiting. With `--notify-webhook <url>` (and `--notify-secret` to HMAC-sign each alert), every payment, approval request and refusal is POSTed there.
- **Wakes on new blocks.** Waiting tools subscribe to the node's new-block events over `--rpc` instead of polling, falling back to polling if the feed is down.
- **No unit mistakes.** Amounts must carry a unit (`"1.5 AETH"` or `"1500000uaeth"`); a bare number is refused rather than guessed at, and every result states amounts in both units and names the asset.
- **Pays in USDC too, when the owner allows it.** With `--usdc-channel <Aether's channel to Noble>`, or `--usdc-path` and `--usdc-base-denom` for USDC that arrives another route (through Osmosis, say; see [docs/OSMOSIS-TESTNET.md](docs/OSMOSIS-TESTNET.md#usdc-over-this-path)), the agent knows USDC: exactly that token over exactly that route, never a lookalike that arrived another way. Spending it stays off until the owner sets its own caps, `--usdc-per-tx-limit "5 USDC" --usdc-daily-limit "20 USDC"` (and optionally `--usdc-approval-threshold`), separate from AETH's since there's no price to add them up with. Then `send_aeth`, `create_escrow`, `create_invoice` and `wait_for_payment` take `"5 USDC"` or `"5000000uusdc"`, `get_balance` and `get_spending_status` list each asset, and an owner's approval is bound to the asset it was for. Without the caps, a USDC payment fails with `ASSET_NOT_ENABLED`. `fetch_paid` pays services priced in USDC (with `maxAmount` in USDC, and `prepay` or `pullAllowance` in USDC too: a USDC allowance is a send limit in USDC only), `withdraw_prepaid` returns a USDC balance, and `find_services` shows each service's asset.
- **Errors a bot can act on.** Every failure is `{"error":{"code","retryable","message"}}` with a stable code (`DAILY_LIMIT_EXCEEDED` with `retryAfterSeconds`, `INSUFFICIENT_FUNDS`, `GRANT_LIMIT_EXCEEDED`, `NODE_UNREACHABLE`, ...); a failed transaction carries an `errorCode` too.

Two modes:

- **Hot wallet** (default): pays from a dedicated agent account (created on first use), capped per transaction and per rolling 24h by the server itself — **not by the chain**. Fund it with only a small, disposable balance.
- **Grant** (`--granter`): pays from your account under an x/authz grant you gave the agent (below), so **the chain** enforces the spend limit, expiry and allowed recipients, and you can revoke it at any time. The agent account needs no balance of its own. The server's caps still apply on top. Available from the activation height.

Read `cmd/agentmcp/main.go`'s package doc comment before deploying either.

### Client libraries (TypeScript, Python)

For agents and services that aren't MCP clients, `clients/ts` (`aether-chain-client`) and `clients/python` (`aether-chain-client`, imported as `aether_client`) implement the same things natively — no Go, no `aetherd`:

- ML-DSA-44 keys from a recovery phrase (the same phrase gives the same address as `aetherd keys add` and `agentmcp`), addresses, signing.
- Sending AETH (signed locally, broadcast over the node's CometBFT RPC), with sequence tracking for several sends per block and a safe `rebroadcast` for retries — the same signed bytes are included at most once.
- `waitForTransaction`, `incomingPayments` / `waitForPayment` for getting paid by memo.
- `fetchPaid` for [paid APIs](#paid-apis-x402): `aether-memo`, `aether-prepaid` (pass `prepay`) and `aether-pull` (pass `pullAllowance`), with the same max-price guard and once-only request IDs as `agentmcp`; `withdrawPrepaid` takes back what's left.
- `findServices` over the [service directory](#service-directory), refusing private and internal addresses by default, with each service's reputation (pass `trusted` accounts to get their ratings separately); `rateService` rates one.
- Receipts: `fetchPaid` checks a seller's signed receipt against the purchase and returns it (`result.receipt.verified`); `verifyReceipt` checks one on its own.

```ts
import { AetherClient, Key, fetchPaid } from "aether-chain-client";
const client = new AetherClient({ rpc: "http://localhost:26657", chainId: "aether-testnet-1" });
const key = Key.fromMnemonic(process.env.AETHER_MNEMONIC!);
const res = await fetchPaid(client, key, "https://api.example.com/forecast", { maxAmount: "0.05 AETH", prepay: "1 AETH" });
```

```python
import os
from aether_client import AetherClient, Key, fetch_paid
client = AetherClient("http://localhost:26657", "aether-testnet-1")
key = Key.from_mnemonic(os.environ["AETHER_MNEMONIC"])
res = fetch_paid(client, key, "https://api.example.com/forecast", max_amount="0.05 AETH", prepay="1 AETH")
```

**Selling, too.** Both include a seller kit: charge per request from a Node or Python service without running `cmd/paywall` — all three schemes (`pull: { collectorKey }` / `pull_collector_key=`, then `startCollecting()` / `start_collecting()`), the manifest for the [service directory](#service-directory), withdrawals and signed receipts (`receipts: { key }` / `receipt_key=`, with an optional delegation). It talks to the same buyers (`agentmcp`, either client, a person paying an invoice by hand), and its ledger file is the Go paywall's format.

```ts
import { AetherClient, Key, Paywall } from "aether-chain-client";
const pw = new Paywall({ client, payTo: "aether1...", price: "0.01 AETH", name: "Weather",
  prepaid: { ledger: "ledger.json", minDeposit: "0.1 AETH", payoutKey: Key.fromMnemonic(process.env.PAYOUT_MNEMONIC!) } });
app.use(pw.middleware({ free: ["/health"] }));   // Express/Connect, before body parsers; who paid: req.aether
```

```python
from aether_client import AetherClient, Paywall
pw = Paywall(client, "aether1...", "0.01 AETH", name="Weather", prepaid_ledger="ledger.json", min_deposit="0.1 AETH")
app = pw.asgi(app, free=["/health"])             # FastAPI/Starlette; pw.wsgi(...) for Flask/Django. Who paid: scope["aether"]
```

They need only the node's RPC port (26657). Both are tested against `clients/testdata/vectors.json`, which the Go code generates (`go test ./clients/vectors -update-vectors`), so their keys, addresses, signatures and transaction bytes stay identical to the chain's.

### Paid APIs (x402)

`cmd/paywall` puts any HTTP API behind per-request AETH payments, with no changes to the API:

```bash
go run ./cmd/paywall --upstream http://localhost:8000 --pay-to <your-address> \
    --price "0.01 AETH" --grpc localhost:9090 --chain-id aether-testnet-1 --free /health
```

An unpaid request gets `402 Payment Required` in the [x402](https://www.x402.org) wire format with scheme `aether-memo`: a price, an address and a one-time invoice. The client pays that amount with the invoice as the memo (from any wallet — humans can pay too), then repeats the request with an `X-PAYMENT` header naming the invoice and transaction hash. The proxy checks the transaction on chain, serves the request exactly once, and tells the upstream who paid (`X-Aether-Payer`). Invoices are HMAC-signed, so issuing them stores nothing. Go services can use the `paywall` package's middleware directly.

**Charging USDC.** `--price "0.05 USDC" --usdc-channel <Aether's channel to Noble>` charges Noble's USDC over exactly that channel instead (or `--usdc-path` and `--usdc-base-denom` for USDC that arrives another route). One proxy charges one asset: deposits, balances, pull allowances, collections, withdrawals and `--min-deposit`/`--pull-credit` are all in it. The 402's `asset` field is the USDC denom (`ibc/...`), `extra.symbol`/`extra.amount` state the price in USDC (`amountAeth` is only for AETH prices), the manifest carries the same, and a receipt's `amount` gets the denom appended (`50000ibc/...`) so it can't be read as uaeth. AETH services read exactly as before. `agentmcp`'s `fetch_paid` pays a USDC price by any scheme (its `maxAmount`, `prepay` and `pullAllowance` must be in USDC, else `ASSET_MISMATCH`).

With ~60s blocks a paid request waits about one block; a payment is served whenever it lands within the invoice's 24h lifetime, so slow confirmation never forfeits it.

**Prepaid, for agents.** With `--prepaid-ledger <file>` the proxy also offers `aether-prepaid`: an agent deposits once (memo `prepaid:<its address>` — anyone can fund it, e.g. a person funding their bot), then signs each request with its ML-DSA key and the price is deducted instantly. Each request ID is charged once, so a retry is never charged twice. The seller holds unspent balances in that file (back it up); agents should deposit only what they'd trust that service with. People paying occasionally just use the per-request scheme.

**Pull, for agents.** With `--pull-key <name> --keyring-dir <dir>` the proxy also offers `aether-pull`, where the buyer's money stays in its own account until it's owed. The agent grants that keyring account (the collector) an x/authz send allowance — a spend limit, an expiry, and `--pay-to` as the only allowed recipient; the chain enforces all three and the agent can revoke it any time — then signs each request like a prepaid one and is served at once. The proxy collects what each buyer owes in batches, one `MsgExec` moving it straight to `--pay-to`: signed and saved before it's broadcast, then only ever re-sent. `--pull-credit` (default 100 requests) caps what a buyer may owe between collections, which is also the most you can lose if one revokes just before a collection; a failed collection is remembered and that buyer refused until an allowance covers it. The collector needs no funds (the first allowance granted to it creates its account), and can only ever move buyers' money to `--pay-to`, within their limits. What's owed is kept in `--pull-ledger` (default: the `--prepaid-ledger` file).

**Receipts.** With `--receipt-key <name> --keyring-dir <dir>`, every paid response carries a signed receipt (`X-PAYMENT-RECEIPT`): network, payee, payer, the payment, price, method, host, path, a hash of the request body, the status, a hash of the response body (up to 4 MiB; bigger or event-stream responses omit it) and the time. A buyer can prove what it paid for and what it got, to anyone, with nothing but the payee's address. The receipt key must be `--pay-to`'s own, or one it delegated receipts to, so the payee key can stay offline: run `paywall delegate-receipts --payee-key <name> --signer <receipt-key address> --keyring-dir <dir>` where the payee key is, and pass the file it writes as `--receipt-delegation`.

**Withdrawals.** Add `--payout-key <name> --keyring-dir <dir>` and agents can take back what they haven't spent: they POST a request signed like a paid one to `/.well-known/x402/withdraw` (`{"amount":"all"}` or an amount in uaeth), and the proxy pays it from that keyring account — always to the signing account itself, so a leaked or replayed signature can only return the agent's own money. Each withdrawal ID pays out once: the amount leaves the balance and the signed payout is saved in the ledger before it's broadcast, so a retry, or a restart mid-payout, re-sends the same transaction instead of paying again; the balance comes back only if the payout can never land. Partial withdrawals must be at least `--min-deposit`. Keep only a small float in the payout account (it can be `--pay-to`'s own key). The manifest and 402 responses advertise `withdrawPath` when it's on.

The upstream must be reachable only through the proxy.

### Service directory

Paid services list themselves on chain, so agents can find them without a central registry. `cmd/paywall` serves a manifest at `/.well-known/x402` (`--name`, `--description`) and, with `--public-url`, prints the command that lists it: 1 uaeth from the payee account to the directory address with memo `x402-service:<url>` (`x402-delist:<url>` removes it). A listing appears only if the manifest at that URL names the announcer as payee, so nobody can list someone else's service. Agents use `find_services`; the explorer shows them on its **Services** page. Manifests come from URLs anyone can announce, so fetching them refuses private and internal addresses (`--directory-allow-private` for local devnets only).

**Reputation.** Each listing comes with what the chain shows of its use over the last ~10,080 blocks: payments to its payee, from how many accounts, how much. Buyers rate a service by sending 1 uaeth to the directory address with memo `x402-rate:<1-5>:<url>`; a rating counts only if the rater paid that service first, and each account's latest rating replaces its earlier ones. Fees are zero, so a seller *can* manufacture payments and ratings from accounts it controls — treat those numbers as hints. What it can't fake is a rating from an account you trust, or your own experience: `find_services` reports trusted ratings and the agent's own purchase history separately, and ranks by them with `orderBy: "trusted"`.

### On-chain agent permissions (x/authz, x/feegrant)

From `app.AuthzFeegrantActivationHeight` (block 109,000, live on the testnet), an account can grant another account (an agent) a scoped, expiring, chain-enforced permission — e.g. "send up to 1 AETH from my account until Friday" — and optionally pay its fees:

```bash
aetherd tx authz grant <agent-address> send --spend-limit 1000000uaeth --expiration <unix-ts> --from <you>
aetherd tx feegrant grant <you> <agent-address> --spend-limit 100000uaeth --from <you>
aetherd tx authz revoke <agent-address> /cosmos.bank.v1beta1.MsgSend --from <you>

aetherd query authz grants <you> <agent-address>       # or grants-by-granter / grants-by-grantee
aetherd query feegrant grant <you> <agent-address>     # or grants-by-granter / grants-by-grantee
aetherd query bank balances <address>
```

Run `agentmcp` with `--granter <you>` (and `--fee-granter <you>`) to have an agent spend under such a grant.

Nodes running this binary halt once at the activation height (`CONSENSUS FAILURE`, block not committed) and must be restarted (`systemctl restart aetherd`) to add the two new stores; see `app/authz_feegrant.go` for why.

## Registering as a validator

Run this on the validator node itself:

```bash
go run ./cmd/validatorkeygen --miner <your-bech32-address> --home <the node's home>
# then run the aetherd tx pow register-validator-pubkey command it prints
```

It registers the node's own consensus key, read from `<home>/config/priv_validator_key.json`: the key the node signs blocks with. Register any other key and the miner becomes a validator nothing signs for. `--new-key` generates a fresh key instead and prints the `priv_validator_key.json` to install on the node first.

Mine and submit successfully within an epoch to accumulate native work. At the epoch boundary (1440 blocks), Top-K (21) by native work become the active set. Downtime (>50% missed signatures in a 60-block window) causes temporary removal; equivocation causes permanent ban and escrow burn.

**One consensus key, one miner account.** Never register the same node's key under two miner accounts: if both are picked, or one leaves the set as the other joins, the validator updates name that key twice and CometBFT halts the chain. From `ConsensusKeyGuardActivationHeight` (`x/pow/types.go`) the chain refuses it: to move a key to a new miner account, first register a different key on the old one. Rotating an active validator's key also takes it out of the set until an epoch picks it with the new key.

## Miner and validator alerts

`cmd/minerwatch` watches one or more addresses and pushes what happens as it happens, instead of an agent polling `/api/miner` or scraping `powminer`'s logs:

```bash
go run ./cmd/minerwatch --address aether1... --webhook https://example.com/hook --low-balance "5 AETH" --state minerwatch.json
```

Each event is printed to stdout as one JSON object per line and, with `--webhook`, POSTed there with retries. Set `--secret` (or `MINERWATCH_SECRET`) and each body is signed like `agentmcp`'s owner alerts: `X-Aether-Signature` is hex HMAC-SHA256(secret, body). Every event has an `id` that's the same across restarts, so a receiver can drop repeats; `X-Aether-Event` names it.

| Event | When |
|--|--|
| `pow_submission_confirmed` / `pow_submission_failed` | one of the address's PoW submissions landed in a block, or failed there (`rawLog` says why) |
| `validator_selected` | the validator set picked it; `effectiveFrom` is the block its voting power starts |
| `validator_removed` | it left the set: `reason` is `not_reselected`, `downtime` or `banned` |
| `miner_banned` | banned for equivocation |
| `no_work_this_epoch` | `--no-work-warn-at` (0.5) of the way through an epoch, a registered miner has no work yet, so it would leave (or not join) the set at the selection height: `activeValidator`, `blocksUntilSelection`, `message` |
| `selection_at_risk` | `--at-risk-blocks` (20) before the set is picked, a registered miner isn't eligible or on track: `notEligibleBecause`, `rank` |
| `balance_low` / `balance_recovered` | the balance crossed `--low-balance` |
| `node_unreachable` / `node_recovered` | `--unreachable-after` (3) failed polls in a row, then the first success |
| `chain_stalled` / `chain_resumed` | the node answers but made no block for `--stall-after` (5m) |

It checks every `--interval` (30s) and does nothing until a new block. With `--state <file>` a restart reports what happened while it was down; without it, it starts fresh. `--once` with `--state` suits a cron job.

## Merged mining (AuxPoW)

Litecoin/Dogecoin-family AuxPoW (chain ID **17776**) may satisfy PoW and earn the reward + retarget difficulty. **Only native work counts toward Top-K.** Pools connect through `cmd/auxpowd`, a bridge speaking the merged-mining RPC Namecoin and Dogecoin use (`createauxblock` / `submitauxblock`); it hands out work from `MergedMiningActivationHeight` (205,000 on the testnet). Plan and status: [docs/MERGED-MINING-PLAN.md](docs/MERGED-MINING-PLAN.md). See also `cmd/auxpowtest` and the whitepaper.

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
| `x/escrow` | Money locked for another account until released, refunded or expired (not the validator reward escrow in `x/pow`) |
| `crypto/mldsa` | ML-DSA-44, ADR-028 addresses, keyring / ante |
| `wallet/` | Account management, queries, tx construction |
| `app/` | App wiring; `authz_feegrant.go` gates x/authz + x/feegrant activation; `helicase.go` accepts proposer-included relay transactions |
| `helicase/` | The node's Helicase worker: finds packets to relay in from another chain and proves them |
| `relayer/`, `cmd/relayer` | Aether's own ML-DSA relayer: opens paths (handshakes); `relayer.Plan` finds what's pending in either direction |
| `cmd/outbound` | Unattended relayer onto another chain, signing only there; Helicase covers the direction onto Aether |
| `ligase/` | A transfer from another chain that carries an escrow instruction in its memo: fund, release, withdraw, without an Aether key |
| `cmd/aetherd` | Node binary |
| `cmd/wallet` | CLI over `wallet/` |
| `cmd/faucet` | Rate-limited faucet |
| `cmd/explorer` | Minimal live explorer |
| `cmd/agentmcp` | MCP server exposing the wallet as tool calls, for AI agents |
| `paywall/`, `cmd/paywall` | Charge AETH per HTTP request (x402 format): middleware and reverse proxy |
| `directory/` | On-chain service directory: announcements, manifest verification, safe fetching |
| `clients/ts`, `clients/python` | TypeScript and Python clients: keys, payments, paid APIs (buying and selling), withdrawals, directory |
| `clients/vectors` | Generates the shared test vectors both clients are checked against |
| `cmd/powminer` | Native PoW nonce search against live state |
| `cmd/minerwatch` | Webhook alerts for a miner or validator: submissions, selection, balance, stalls |
| `cmd/auxpowd` | Merged-mining bridge for Litecoin pools (`createauxblock` / `submitauxblock`) |
| `cmd/auxpowtest` | Valid test AuxPoW construction; `--auxpowd` acts as a pool against the bridge |
| `cmd/scryptbench` | Scrypt throughput benchmarks |
| `cmd/validatorkeygen` | Consensus key + PoP for registration |
| `cmd/balancecheck` | gRPC bank balance helper |
| `cmd/equivocationtest` | Constructed equivocation evidence |
| `docs/WHITEPAPER.md` | Technical whitepaper |
| `testnet/genesis.json` | Live public testnet genesis |

## Multi-node and further docs

- [Technical Whitepaper](docs/WHITEPAPER.md) — system design, economics, crypto, security limitations (incl. BFT/Top-K and height gates)
- [Agent & Bot Integration](docs/AGENT_INTEGRATION.md) — participating as software agents (current surface + proposed APIs/policy)
- [Agent-to-agent payment demo](docs/AGENT_DEMO.md) — a real spend-capped grant and payment, reproducible against any network
- [TLS for the seed](docs/TLS.md) — HTTPS for RPC/gRPC/faucet/explorer with no domain purchase
- [IBC](docs/IBC.md) — core IBC, ICS-20 transfer, ICS-27 interchain accounts: live since block 122,000, plus the ML-DSA relayer and counterparty chain used to test it end to end, including on the live testnet
- [Helicase](docs/HELICASE.md) — the block proposer relays IBC packets onto Aether without any relayer signature: why it's safe, how to run it, the devnet proof
- [Connecting to Osmosis testnet](docs/OSMOSIS-TESTNET.md) — runbook for a real external IBC counterparty; blocked on a governance precondition, not yet executed
- [Plan: Aether ↔ Osmosis ↔ Noble, paying in USDC or AETH](docs/USDC-PLAN.md) — what each connection is for, the relayer service both need, and the phases to USDC payments
- [Account abstraction](docs/ACCOUNT_ABSTRACTION.md) — session keys and guardian thresholds (`x/accountauth`): live on the testnet since block 122,000
- [Ligase](docs/LIGASE.md) — escrow from another chain with one transfer and no Aether key; the two-strands rule that keeps AETH under post-quantum signatures
- [Escrow](docs/ESCROW.md) — lock money for another account (or agent) until the payer or an arbiter releases it, the payee or arbiter refunds it, or its deadline settles it: built, not yet active
- Wiki: [Architecture](../../wiki/Architecture), [Phase 1 Multi-Validator Selection](../../wiki/Phase-1-Multi-Validator-Selection), [Known Issues](../../wiki/Known-Issues-and-Technical-Debt)

## License and brand

Software in this repository is under the [MIT License](LICENSE).

The Aether name, Æ mark, and assets in [`docs/brand/`](docs/brand/) are **not**
covered by that license. See [Brand usage and trademarks](docs/brand/README.md#brand-usage-and-trademarks).

## Contributing

Major design decisions and subtle Cosmos SDK / CometBFT integration fixes are documented in the wiki. Read those before changing `app/`, `x/pow`, or `crypto/mldsa`.

## Security

No professional third-party audit yet. Community review and responsible disclosure are welcome via issues. Researchers scoping an engagement should use the wiki known-gaps materials and this README’s status table.
