# Hosted test wallet

The fastest way for an agent to pay for something on Aether: connect one MCP
server, ask for a wallet, and buy a service. There's no install, key or
sign-in.

```
https://explorer.157-245-252-221.sslip.io/sandbox/mcp
```

```bash
claude mcp add --transport http aether-test-wallet https://explorer.157-245-252-221.sslip.io/sandbox/mcp
```

Then try a prompt:

> Create an Aether test wallet, wait until it's funded, find a paid service
> under 0.001 AETH and buy one call from it. Show me what you got and the
> payment's transaction.

It runs on the testnet only. The wallets are custodial: the server can
spend them too. This is a sandbox for trying agent payments, never for
anything of value. For a wallet you hold yourself, use `agentmcp` (the
full wallet, with spend limits and owner approvals) or `aether-chain-client`.

## Tools

| Tool | What it does |
|---|---|
| `create_test_wallet` | A new wallet funded with 0.01 AETH (enough for at least 10 calls to the seed's services). Returns its `walletToken`. The funding lands with the next block (~60 s) |
| `get_test_wallet` | Its address and balances, and whether the funding has landed |
| `buy_service` | Calls a service from the on-chain directory and pays it with standard x402 (`exact`) if the price is at most `maxAmount`. Returns the response and the payment's transaction. It waits for the payment's block, so it takes up to about a minute |
| `send_from_test_wallet` | Sends test AETH to any address |
| `get_balance`, `get_transaction_status`, `find_services` | As on the read-only `/mcp` |

The `walletToken` is the wallet. Every wallet tool takes it, and anyone
holding it can spend the wallet. An agent keeps it in its context and can
reuse it across sessions. The server keeps nothing per wallet, so the
token works after a restart too.

## Limits

- Three new wallets a day per caller, and 200 a day in all. Reuse a token
  rather than making a new one.
- `buy_service` pays only services listed in the on-chain directory (what
  `find_services` returns), so the server is no open proxy.
- It runs only on a test or dev chain: it refuses to start on any other.

## How it works

A wallet token is `aether-test-wallet-v1.` followed by the key's 256-bit
seed, encrypted with AES-GCM under the server's secret. Each call decrypts
it into an in-memory key, signs and forgets it.

New wallets are paid from a funder key, which is the only key the server
holds. The server never opens an agent account.

`buy_service` reads the service's `PAYMENT-REQUIRED` header, signs a send
of exactly the price to its `payTo`, and retries with `PAYMENT-SIGNATURE`.
The service settles the payment before it answers (see [X402.md](X402.md)).

It is `agentmcp --sandbox-http`, a separate server from the public
read-only `/mcp`, which stays read-only.

## Running it (seed)

```bash
cd /root/aether-src && git fetch origin && git checkout origin/main
bash scripts/agentservices/sandbox.sh   # builds /root/agentmcp-sandbox, runs aether-sandbox on 127.0.0.1:8092
bash scripts/agentservices/caddy.sh     # routes /sandbox/mcp to it
```

The first run creates `sandbox-funder` and the token secret in
`/root/aether-sandbox-key`, and prints the funder's address. Fund it with,
for example, 20 AETH from the faucet key. At most 200 wallets a day at 0.01
AETH each is at most 2 AETH a day.

Back up `token-secret`: losing it invalidates every token handed out.

The flags, if you need to change them: `--sandbox-grant` (0.01 AETH),
`--sandbox-per-ip` (3), `--sandbox-per-day` (200). Logs:
`journalctl -u aether-sandbox`. Each new wallet logs its address and
funding transaction.
