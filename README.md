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

To redeploy the live explorer from `main`, run `bash scripts/deploy-explorer.sh` as root on the server that hosts it. It builds while the old version keeps serving, keeps backups, restarts `aether-explorer` and rolls back if the new one doesn't answer.

## AI agent wallet (MCP)

An MCP server exposing wallet operations as tool calls, so an AI agent can pay and get paid directly instead of only a human clicking through a UI.

**Quick start (testnet):**

```bash
go install ./cmd/agentmcp
agentmcp init
```

`init` creates the agent's account (showing its recovery phrase once), asks the testnet faucet for funds, waits until they arrive and prints the exact `claude mcp add ...` command and the JSON config block for Claude Desktop and other MCP clients. Run it again to reuse the same account. `--faucet <url>` points it at another faucet, `--no-faucet` skips funding; it takes the same `--grpc`, `--rpc`, `--chain-id` and `--keyring-dir` flags as the server. Once running, the agent can top itself up with the `request_testnet_funds` tool (testnet only; `FAUCET_RATE_LIMITED` means wait).

To run the server by hand:

```bash
go run ./cmd/agentmcp --grpc localhost:9090 --rpc http://localhost:26657 --chain-id aether-testnet-1 \
    --per-tx-limit 1000000 --daily-limit 5000000 \
    [--granter <your-address> [--fee-granter <your-address>]]
```

Built for how agents actually fail:

- **No double payments on retry.** `send_aeth` requires an `idempotencyKey`; a retry with the same key re-sends the identical signed transaction (its sequence number is signed in, so the chain can include it at most once) and returns its status.
- **Knows when a payment is final.** `send_aeth` returns `pending` once the node accepts it; `wait_for_transaction` waits until it's `confirmed` or `failed` in a block.
- **Can get paid.** `create_invoice` returns a unique memo and the current height; `wait_for_payment(memo, minAmount, sinceHeight)` waits for a confirmed incoming payment that matches. It reads every incoming payment since that height, page by page, so a busy agent can't miss one. Memos are sender-controlled, so tools label them as untrusted data.
- **Can buy from paid APIs.** `fetch_paid(url, maxAmount, idempotencyKey)` requests a URL; if the server answers HTTP 402 (see [Paid APIs](#paid-apis-x402)), it pays at most `maxAmount`, waits for the payment to confirm and returns the response. Retrying with the same key resumes the same payment, never a second one. For many requests to one service, add `prepay` (e.g. `"1 AETH"`): the agent deposits that once and then pays each request instantly by signature — milliseconds instead of a block. `list_prepaid_balances` shows what's left where, and `withdraw_prepaid(service)` takes it back, from services that offer withdrawals.
- **Can find services, and tell good ones from bad.** `find_services(query, maxPrice)` lists paid APIs from the on-chain [service directory](#service-directory) with each one's reputation: recent payments and payers, ratings from paying accounts, ratings from accounts you trust (the owner, the agent itself, `--trust <addresses>`), and the agent's own history with it. `orderBy: "trusted"` puts what can't be faked first. `rate_service(url, score)` rates one it has bought from; `announce_service` lists one the agent runs.
- **Keeps receipts.** When a seller signs receipts, `fetch_paid` checks each one against exactly what was sent and received and returns it; `list_purchases` is the log of what the agent bought, with each receipt — proof anyone can check against the seller's address.
- **Answers to its owner.** With `--approval-threshold "0.5 AETH" --approver <owner-address>`, bigger payments wait (nothing signed or sent) until the owner runs `agentmcp approve <id>`, which signs the decision with the **owner's** key — so the agent can't approve itself even if it can write files on the machine. `agentmcp approvals` lists what's waiting. With `--notify-webhook <url>` (and `--notify-secret` to HMAC-sign each alert), every payment, approval request and refusal is POSTed there.
- **Wakes on new blocks.** Waiting tools subscribe to the node's new-block events over `--rpc` instead of polling, falling back to polling if the feed is down.
- **No unit mistakes.** Amounts must carry a unit (`"1.5 AETH"` or `"1500000uaeth"`); a bare number is refused rather than guessed at, and every result states amounts in both units.
- **Errors a bot can act on.** Every failure is `{"error":{"code","retryable","message"}}` with a stable code (`DAILY_LIMIT_EXCEEDED` with `retryAfterSeconds`, `INSUFFICIENT_FUNDS`, `GRANT_LIMIT_EXCEEDED`, `NODE_UNREACHABLE`, ...); a failed transaction carries an `errorCode` too.

Two modes:

- **Hot wallet** (default): pays from a dedicated agent account (created on first use), capped per transaction and per rolling 24h by the server itself — **not by the chain**. Fund it with only a small, disposable balance.
- **Grant** (`--granter`): pays from your account under an x/authz grant you gave the agent (below), so **the chain** enforces the spend limit, expiry and allowed recipients, and you can revoke it at any time. The agent account needs no balance of its own. The server's caps still apply on top. Available from the activation height.

Read `cmd/agentmcp/main.go`'s package doc comment before deploying either.

### Client libraries (TypeScript, Python)

For agents and services that aren't MCP clients, `clients/ts` (`@aether-chain/client`) and `clients/python` (`aether_client`) implement the same things natively — no Go, no `aetherd`:

- ML-DSA-44 keys from a recovery phrase (the same phrase gives the same address as `aetherd keys add` and `agentmcp`), addresses, signing.
- Sending AETH (signed locally, broadcast over the node's CometBFT RPC), with sequence tracking for several sends per block and a safe `rebroadcast` for retries — the same signed bytes are included at most once.
- `waitForTransaction`, `incomingPayments` / `waitForPayment` for getting paid by memo.
- `fetchPaid` for [paid APIs](#paid-apis-x402), both `aether-memo` and `aether-prepaid` (pass `prepay`), with the same max-price guard and once-only request IDs as `agentmcp`; `withdrawPrepaid` takes back what's left.
- `findServices` over the [service directory](#service-directory), refusing private and internal addresses by default, with each service's reputation (pass `trusted` accounts to get their ratings separately); `rateService` rates one.
- Receipts: `fetchPaid` checks a seller's signed receipt against the purchase and returns it (`result.receipt.verified`); `verifyReceipt` checks one on its own.

```ts
import { AetherClient, Key, fetchPaid } from "@aether-chain/client";
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

**Selling, too.** Both include a seller kit: charge per request from a Node or Python service without running `cmd/paywall` — both schemes, the manifest for the [service directory](#service-directory), withdrawals and signed receipts (`receipts: { key }` / `receipt_key=`, with an optional delegation). It talks to the same buyers (`agentmcp`, either client, a person paying an invoice by hand), and its ledger file is the Go paywall's format.

```ts
import { AetherClient, Key, Paywall } from "@aether-chain/client";
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

With ~60s blocks a paid request waits about one block; a payment is served whenever it lands within the invoice's 24h lifetime, so slow confirmation never forfeits it.

**Prepaid, for agents.** With `--prepaid-ledger <file>` the proxy also offers `aether-prepaid`: an agent deposits once (memo `prepaid:<its address>` — anyone can fund it, e.g. a person funding their bot), then signs each request with its ML-DSA key and the price is deducted instantly. Each request ID is charged once, so a retry is never charged twice. The seller holds unspent balances in that file (back it up); agents should deposit only what they'd trust that service with. People paying occasionally just use the per-request scheme.

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
| `paywall/`, `cmd/paywall` | Charge AETH per HTTP request (x402 format): middleware and reverse proxy |
| `directory/` | On-chain service directory: announcements, manifest verification, safe fetching |
| `clients/ts`, `clients/python` | TypeScript and Python clients: keys, payments, paid APIs (buying and selling), withdrawals, directory |
| `clients/vectors` | Generates the shared test vectors both clients are checked against |
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
