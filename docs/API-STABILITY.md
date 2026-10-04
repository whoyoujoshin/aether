# API stability

What a bot can build on without it breaking under them. The set is small
on purpose: the pieces the [start page](START.md) uses, plus the discovery
documents that point to them.

## The promise

For everything listed below:

- **Additive only.** New endpoints, new fields, new error codes and new
  optional parameters can appear at any time. Write clients that ignore
  fields they don't know.
- **Nothing is removed or renamed in place.** A breaking change ships as a
  new path or field beside the old one. Both work for at least **30 days**,
  and the old one is marked deprecated in `/llms.txt`, `/start.md` and the
  OpenAPI spec for that whole time.
- **Meanings don't change.** `balance` stays a string of uaeth, `code`
  values keep their meaning, and amounts stay integers in the base denom.
- **Tests enforce it.** Contract tests fail if a listed path, field, code
  or header disappears: `cmd/explorer/contract_test.go`,
  `cmd/faucet/contract_test.go`, `clients/ts/test/contract.test.ts` and
  `clients/python/tests/test_contract.py`. `docs/START.md` is checked
  against the page the explorer serves (`cmd/explorer/start_test.go`).

Testnet caveat: this covers the interfaces, not the chain. A testnet can
be reset, and its funds have no value.

## The stable set

### Explorer (`https://explorer.157-245-252-221.sslip.io`)

| Path | What's stable |
|--|--|
| `GET /llms.txt` | Exists. Its first "Start here" link is `/start.md`. It links `/api/agents` and `/api/openapi.json`. |
| `GET /start.md` | Exists. It names this deployment's chain ID, RPC, faucet and explorer, and has the MCP, TypeScript and Python paths. |
| `GET /api/agents` | `name`, `chainId`, `height`, `addressPrefix`, `denom`, `displayDenom`, `decimals`, `signatures`, `endpoints` (`rpc`, `grpc`, `faucet`, `explorer`, `seed`), `faucet` (`url`, `request`, `batch`, `status`, `reachable`), `mcp` (`install`, `init`, `tools`), `paymentSchemes`, `docs`, `warnings` |
| `GET /api/openapi.json` | OpenAPI 3.1. Paths are only added. |
| `GET /api/address?addr=` | `address`, `balance` (uaeth, as a string), `balances[]` (`denom`, `amount`), `transactions[]` (`hash`, `height`, `code`, `direction`, `amount`, `timestamp`) |
| `GET /api/tx?hash=` | `hash`, `height`, `code`, `from`, `to`, `amount`, `timestamp`, `transfers` |

Errors from `/api/*` are JSON `{"error": "..."}` with a 4xx or 5xx status.

### Faucet (`https://faucet.157-245-252-221.sslip.io`)

| Path | What's stable |
|--|--|
| `POST /request` `{"address"}` | `success`, `code`, `message`, `tx_hash`, `new_wallet`. A refusal adds `retry_after_seconds`. |
| `POST /request/batch` `{"addresses"}` | `success`, `code`, `tx_hash`, `sent`, `skipped[]` (`address`, `code`, `retry_after_seconds`), `invalid` |
| `GET /status?address=` | `address`, `eligible`, `retry_after_seconds`, `amount_uaeth`, `caller_remaining` |
| `GET /stats` | `drips`, `sent_uaeth`, `unique_wallets`, `new_wallets`, `new_wallets_by_agents`, `since`, `sources_30d`, `new_wallets_daily`, `top_agents_30d`, `amount_uaeth`, `address_cooldown_secs`, `ibc_enabled`, `pow_bits`, `balance_uaeth` |
| `GET /` | `chain_id`, `denom`, `amount_uaeth`, `address_cooldown_secs`, `caller_limit`, `caller_window_secs`, `batch_max`, `endpoints` (`request`, `batch`, `status`, `stats`) |

`code` values: `sent`, `pending`, `invalid_request`, `invalid_address`,
`batch_too_large`, `address_cooldown`, `caller_limit`, `send_failed`,
`invalid_pow`, `invalid_agent`, `ibc_unavailable`.

Headers: `RateLimit-Limit`, `RateLimit-Remaining`, `RateLimit-Reset` and
`RateLimit-Policy` on every response that counts against a quota. A
refusal sends `Retry-After`.

### Clients (`clients/ts`, `clients/python`)

The calls the start page uses:

- `Key.generate()`
- `Key.fromMnemonic` / `Key.from_mnemonic`
- `key.address`
- `new AetherClient({rpc, chainId})` / `AetherClient(rpc, chain_id)`
- `balance`, `send`, `rebroadcast`
- `waitForTransaction` / `wait_for_transaction`
- the `hash`, `status` and `signed` fields of a send result

The clients are on version 0.x until they're published to npm and PyPI.
These calls follow the policy above anyway.

### MCP wallet (`agentmcp`)

Tool names only ever gain entries; `/api/agents` → `mcp.tools` lists them
(a test keeps that list equal to the server's).

## Not covered

Everything else may change without notice, including:

- the explorer's other `/api/*` endpoints and every field not listed above;
- the web UI's pages and URLs, except `/tx/<hash>`;
- the faucet's `/challenge`, `/agents` and `/drips`;
- log formats;
- CLI flags.

These are still documented in the OpenAPI spec and the README. Ask in an
issue if you need one of them moved into the stable set.

## Changing the stable set

1. Add the new path or field beside the old one, and add it to this page
   and to the contract tests.
2. Mark the old one deprecated in `/llms.txt`, `/start.md` and the OpenAPI
   description, with the date it goes.
3. Remove it no earlier than 30 days later, in its own PR that also
   removes it from this page.
