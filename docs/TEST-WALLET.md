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

Cursor, Codex, VS Code, OpenClaw, Claude Desktop and others: [CONNECT.md](CONNECT.md).

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
| `create_test_wallet` | A new wallet funded with 0.01 AETH (enough for at least 10 calls to the seed's services) and, while the funder has some, 0.01 testnet USDC (`fundingUsdc`; ten calls of "Aether hello (USDC)"). Returns its `walletToken`. The funding lands with the next block (~60 s) |
| `get_test_wallet` | Its address and balances, and whether the funding has landed |
| `buy_service` | Calls a service from the on-chain directory and pays it with standard x402 (`exact`) if the price is at most `maxAmount`. Returns the response and the payment's transaction. It waits for the payment's block, so it takes a few seconds |
| `send_from_test_wallet` | Sends test AETH or USDC to any address, with an optional memo (joining the quest and rating a service are memo sends) |
| `swap_from_test_wallet` | Swaps the wallet's AETH for OSMO in the Osmosis testnet pool, the way `swap_aeth` does (quote first, `minOutput`, `maxCostPercent`), and returns once the OSMO is back (about 25 s) or the AETH is refunded. A swap still in flight is followed up by passing its `txHash` instead of an amount ([OSMOSIS-POOL.md](OSMOSIS-POOL.md)) |
| `get_quest` | The [agent quest](QUEST.md): this wallet's progress, what to do next, and the top of the leaderboard |
| `get_balance`, `get_transaction_status`, `find_services`, `quote_swap` | As on the read-only `/mcp` (`quote_swap`: what AETH would fetch in OSMO on Osmosis testnet; [OSMOSIS-POOL.md](OSMOSIS-POOL.md)) |

The `walletToken` is the wallet: a short handle such as
`tw7k2m9x4p8q1r5v3n`. Every wallet tool takes it, and anyone holding it
can spend the wallet. An agent keeps it in its context and can reuse it
across sessions. The server keeps nothing per wallet, so the token works
after a restart too.

## Limits

- Three new wallets a day per caller, and 200 a day in all. Reuse a token
  rather than making a new one. The payments benchmark has its own
  allowance of 200 a day, for callers that send its key
  (`X-Aether-Bench-Key`; see [BENCHMARK.md](BENCHMARK.md)). A caller that
  sends a key that doesn't match gets `INVALID_BENCH_KEY`.
- `buy_service` pays only services listed in the on-chain directory (what
  `find_services` returns), so the server is no open proxy, and only to
  the payee its listing names: a 402 asking for payment to any other
  account is refused with `PAYEE_MISMATCH` and nothing is paid.
- For the payments benchmark only, `--sandbox-bench-listings` names extra
  services (the benchmark's decoys) that a caller with the bench key sees
  and pays when it names them in `X-Aether-Bench-Listings`. Nobody else
  sees them, and they're never on chain. A line may end with a listed
  price (`decoy-clock <url> 500uaeth`), which `find_services` shows
  instead of the manifest's: task set 4's decoy whose 402 asks more.
- It runs only on a test or dev chain: it refuses to start on any other.

## How it works

A wallet handle is `tw` and 16 characters of Crockford base32: 80 random
bits. The wallet's 256-bit key seed is an HMAC of the handle under a key
derived from the server's secret, so the server stores nothing per
wallet. Each call derives the key in memory, signs and forgets it. The
server reads a handle with its case changed, dashes or spaces added, or
`o`, `i` and `l` in place of `0`, `1` and `1`, since that's how copies
tend to go wrong.

Handles replaced longer tokens on 2026-10-14: `aether-test-wallet-v1.`
and the key's seed, encrypted with AES-GCM under the same secret, about
100 characters. In the payments benchmark an open model copied those wrong
partway through a task. Tokens issued before then still work.

New wallets are paid from a funder key, which is the only key the server
holds. The server never opens an agent account.

`buy_service` reads the service's `PAYMENT-REQUIRED` header, signs a send
of exactly the price to its `payTo`, and retries with `PAYMENT-SIGNATURE`.
The service settles the payment before it answers (see [X402.md](X402.md)).
A send or a purchase made while the wallet's last one is still waiting for
its block takes the next sequence, so an agent needn't wait between them.

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

An agent needs no AETH to pay in USDC: fees are zero on the testnet. A test wallet emptied of its AETH still paid "Aether hello (USDC)" 0.001 USDC by standard x402 (checked on a devnet with the seed's zero minimum gas price, 2026-10-16).

New wallets also get 0.01 USDC each (`--sandbox-grant-usdc`, set by
`sandbox.sh`; `USDC_GRANT=` changes it) while the funder holds that much.
Without it they get AETH only, and the log says the funder is short. Benchmark
wallets never get USDC, so each task's AETH budget stays the only money in
its wallet. The USDC is Circle's testnet USDC on Injective, sent over IBC
(`transfer/channel-2`): take some from Circle's faucet (`faucet.circle.com`,
USDC on Injective testnet) and send it to the funder with `relayer.exe -send`
as in step 7 of [INJECTIVE-TESTNET.md](INJECTIVE-TESTNET.md), or with
`aetherd tx bank send` from an Aether address that already holds some. 10
USDC funds 1,000 wallets. `sandbox.sh` prints the funder's USDC balance.

Back up `token-secret`: losing it invalidates every token handed out.

The flags, if you need to change them: `--sandbox-grant` (0.01 AETH),
`--sandbox-grant-usdc` (off; `sandbox.sh` sets 0.01 USDC),
`--sandbox-per-ip` (3), `--sandbox-per-day` (200), `--sandbox-bench-key-file`
(`sandbox.sh` creates `/root/aether-sandbox-key/bench-key`) and
`--sandbox-bench-per-day` (60). Logs:
`journalctl -u aether-sandbox`. Each new wallet logs its address and
funding transaction.
