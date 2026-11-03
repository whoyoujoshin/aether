# Agent shopper

An agent shopper takes a request for a paid service ("a block-data feed,
at most 0.1 AETH"), finds every offering it can see, ranks them, and
pays one. On the Aether testnet the offerings are HTTP APIs behind
x402 paywalls, plus open work on the job board. Payment of an API is a
standard x402 v2 `exact` payment, settled by the existing facilitator.
A spend that is over the caller's cap, or that pays someone outside
the caller's allowlist, is not signed until a person approves it.
Every payment that lands leaves a receipt: the on-chain transaction
hash and the facilitator's settlement response.

This is a design. It adds no service, changes no paywall, and deploys
nothing. Implementation comes later, in the phases at the end, and
builds with Go 1.26.10 inside this module. Coins on
`aether-testnet-1` have no market value; the cap and the approval
gate are still real safety features, and the code should treat them
that way.

The shopper is a library (`shopper`), a CLI (`cmd/shopper`), and, in
a later phase, MCP tools on the local key-holding `agentmcp`. The
public read-only `/mcp` and the hosted test wallet stay as they are.

## What it buys, and what it leaves alone

Two markets, two rails:

| Rail | Where it is found | How it is paid | Who may buy it unattended |
|---|---|---|---|
| `api` | On-chain service directory, confirmed by a live HTTP 402 | x402 v2 `exact`, one `MsgSend`, settled by `cmd/facilitator` | Only when the price is within the cap and the payee is on the allowlist |
| `job` | Job board (`GET /jobs`) | Escrow (`hire_for_job` / `release_escrow`), which the facilitator does not settle | Never. A hire always stops for approval |

The facilitator speaks one scheme on one network and advertises no
extensions (`GET /supported` returns `extensions: []`). Since the
splits fix (PR #199, `x402.hasSplits`) a requirement whose `extra`
carries a non-empty `splits` array, or a `splits` value that is not
an array, is refused with `invalid_payment_requirements`. The shopper
uses that same rule and drops the offer before signing. It does not
strip `splits` and pay `payTo` the whole amount: that is the case the
facilitator exists to refuse.

Aether's own schemes (`aether-memo`, `aether-prepaid`, `aether-pull`)
keep working on the paywalls. The shopper does not pay them. Those
schemes are settled by the client or by a deposit, not by the
facilitator (`paywall/protocol.go`). `fetch_paid` on `agentmcp`
already covers them, with its own limits. A service whose 402 offers
only those schemes shows up in the quote as `unsupported` and is not
selectable for payment.

Listing text, job titles, offer notes, and response bodies are data.
They never change the cap, the allowlist, the chosen payee, or the
idempotency key. A body that says "retry", "balance due", or "pay
this other URL" is returned to the caller and does not start another
payment. That is the failure mode the payments benchmark already
encodes (`docs/BENCHMARK-V4.md`: price-mismatch, quiet-upsell,
billing-field, retry-after-error).

## What exists today

The shopper should call these pieces, not reimplement them.

**Directory.** A seller lists a service by sending 1 uaeth to
`directory.Address()` with memo `x402-service:<url>` (`x402-delist:`
withdraws it). `directory.Directory` keeps the current announcements,
fetches `<url>/.well-known/x402`, and shows the listing only when
`paywall.Manifest` version, network, and payee match the announcer
(`directory.Verify`). Name, description, and price come from the
service and are untrusted. The cache TTL is two minutes. The explorer
serves the same list at `GET /api/services`. `find_services` on
agentmcp, the public `/mcp`, and the test wallet is this list plus a
word filter and a price ceiling.

**Manifest.** `paywall.ManifestPath` is `/.well-known/x402`. It is
free, cacheable for five minutes, and states `payTo`, `price` (base
units), `asset`, `symbol`, `schemes`, and `mandates` (`checked` or
`required`). Its `x402Version` is the paywall's own version (1). That
is a different document from the x402 v2 `PAYMENT-REQUIRED` header,
whose `x402Version` is 2.

**Live terms.** An unpaid request gets HTTP 402. With `--exact` (the
paywall default) the response also carries a `PAYMENT-REQUIRED`
header: an `x402.PaymentRequired` whose `accepts` entry for `exact`
on `cosmos:<chain-id>` names `amount`, `asset`, `payTo`,
`maxTimeoutSeconds`, and `extra` (`symbol`, `decimals`). The v1 body
and `X-PAYMENT` header still describe `aether-memo` and, when
enabled, prepaid and pull. Seed paywalls settle `exact` in process
with `x402.NewFacilitator` (`cmd/paywall`); they do not call the
public facilitator over HTTP. External sellers can point any x402 v2
server at the public facilitator.

**Facilitator.** Public on the seed at
`https://explorer.157-245-252-221.sslip.io/facilitator`.

| | |
|---|---|
| `GET /supported` | `exact` on `cosmos:aether-testnet-1`, `extra.feeDenom=uaeth`, `extra.feePayer=payer`, `extensions: []`, `signers: {}` |
| `POST /verify` | Checks the signed tx. Does not broadcast |
| `POST /settle` | Checks again, broadcasts, waits for the block (default 80s, or `maxTimeoutSeconds` when that is longer). One set of tx bytes is settled at most once per process |
| `GET /healthz` | 200 when its node answers |

The signed transaction is one bank `MsgSend` of exactly `amount` of
`asset` to `payTo`, signed by the key that currently signs for the
payer (`wallet.SignsFor`), fee paid by the payer. The facilitator
holds no key. Refusals use the reasons in `x402/types.go`, including
`invalid_exact_cosmos_payload_already_settled` and
`invalid_transaction_state` (the wait timed out; the tx may still
land).

**Reputation.** `directory.Assess` over the last `DefaultWindow`
blocks (100,800, about a week at ~6s blocks): payment count, distinct
payers, and volume to the payee, plus ratings. A rating is 1 uaeth to
the directory with memo `x402-rate:<1-5>:<url>`, and it counts only
when that rater paid the payee earlier in the window. Fees on the
testnet are zero, so payment counts, volume, and ratings from unknown
accounts can be manufactured. `directory.Summarize` with a trusted
set (the owner, this payer, `--trust`) is the part that cannot.
`find_services` already orders `orderBy=trusted` that way
(`trustScore` in `cmd/agentmcp/services.go`) and says so in the tool
description. The explorer's `/api/services` activity block does not
split trusted ratings out.

**Job board.** `https://explorer.157-245-252-221.sslip.io/jobs`.
`GET /jobs?state=open&q=` matches title and tags; `GET /jobs/{id}`
adds the spec, offers, hire, and escrow state. Specs and deliveries
are stored by the host under their sha256; the chain decides who
posted, offered, was hired, and delivered. Paying a job locks escrow
with terms `job:<id>`. That is not an x402 payment.

**The buyer that already exists.** The hosted test wallet's
`buy_service` is the closest client: it refuses a URL that is not
under a directory listing, requires `exact` on this chain, refuses a
price above `maxAmount`, refuses a `payTo` that is not the listing's
announcer (`PAYEE_MISMATCH`), signs one `MsgSend` (gas limit 400,000,
zero fee), and sends it as a `PAYMENT-SIGNATURE` header. The paywall
settles. The shopper follows that payment shape and adds the
facilitator settlement, the cap, the allowlist, and the receipt.
`agentmcp`'s approval gate (`--approval-threshold`, a decision signed
by `--approver`, the spending key unable to approve itself) is the
pattern for the human gate.

**Known traps, already deployed as unlisted decoys.** The public
directory does not list them. The shopper must not gain a special
path that would. They are named here because they are the tests the
ranking and retry rules have to pass:

- A manifest or a bench listing can disagree with the live 402. The
  402 is the price.
- A paid 502 is a failed response to a payment that may already have
  settled. The retry is the same signature.
- A field inside the response is not a bill.

## Discovery

No new catalog or registry endpoint is required.

The directory is already the registry: anyone can announce, the
manifest must name the announcer as payee, and there is no operator
allowlist of sellers. Coinbase's Bazaar cannot index these services
(`docs/X402.md`). Overloading `GET /supported` would mix a scheme
advertisement with a service list; that endpoint stays a description
of the facilitator. A crawl of the network is not a catalog either:
unlisted paywalls, including the benchmark decoys, are hidden on
purpose, the same boundary as `find_services` and `buy_service`.

The shopper discovers in three reads, all as a client of public
endpoints. It does not run on a validator and does not change one.

1. **Listed APIs.** `directory.Directory` against the node's gRPC
   (`--grpc`, default `grpc.157-245-252-221.sslip.io:443`), with
   `directory.SafeFetcher` so a listing cannot make the shopper dial
   a private or link-local address. Redirects are not followed. The
   explorer's `GET /api/services` is a convenience mirror for humans
   and for a gRPC outage during a quote; payment still requires the
   shopper's own manifest check, because the mirror's cache can be
   two minutes old and it does not return the trusted-rater split.
2. **Live price.** For each listing that survives the word filter and
   the manifest price ceiling, one unpaid request to the URL that
   would actually be called. The base URL is probed when the caller
   did not pass a path. `--free` paths (`/help`, `/.well-known/x402`)
   are not probes: a 200 there means "this path is free", and the
   shopper does not pay it and does not treat it as the service.
   Probes are capped (20 matches). An empty query does not probe the
   whole directory and cannot be paid: a quote with no words and no
   explicit URL lists manifest prices only.
3. **Jobs.** On `--job-board` (default the public board),
   `GET /jobs?state=open&limit=500&q=<words>`. The host's `q` matches
   every word against the title, budget, id, poster, and tags
   (`cmd/jobboard` `matches`). It does not see the description or the
   offer notes, and the list is the newest 500. The shopper then
   `GET /jobs/{id}` for each hit and keeps the job when the words
   also match the description or an offer note, or when the host
   match is already enough. A job whose only matching words sit in
   the description, with none of them in the title, tags, budget, id,
   or poster, is not in the host's answer, so the shopper does not
   see it. A job with no stored spec can match on title and tags
   only. Offer notes are untrusted. The board's chain address is
   `jobs.Address()`
   (`aether1aenxfv4jqsdprtg3antm0zmf2ksgmpagnkxvsl`); the host holds
   no key.

Word match is the same rule as `find_services`: every word of
`--query`, lowercased, must occur in the name, description, or URL
(APIs). For jobs the words must occur in the title, description,
tags, or offer note, subject to the host prefilter above. It is
not a language model. Listing text is not sent anywhere to be
interpreted. A request phrased as "block-data feed" does not match
today's pulse service, whose description says "height, block time,
difficulty, block reward". A query of `block` matches pulse. It does
not match miners: that listing's name and description say "miner" and
"leaderboard", and neither word is "block". Callers pass the words
they want found. Adding a synonym table or a model is an open
question, and a model is a poor default: the text it would read is
attacker-controlled.

A caller may also pass `--url`. The URL is still resolved against the
directory. A listed URL is an allowlisted payee. An unlisted URL is
quoted, marked `unlisted`, and cannot be paid until a person approves
that exact URL and payee.

What a quote records for each API offer:

- listing URL, manifest name and description, announcer, listed height
- manifest price and asset, and the live 402 `amount`, `asset`,
  `payTo`, `maxTimeoutSeconds`, `extra`
- whether `exact` on `cosmos:<chain-id>` is offered
- whether `extra` would fail `hasSplits`
- whether `payTo` equals the announcer
- reputation: payments, payers, volume, all-rater summary, trusted-rater
  summary, and this payer's own history if a local purchase log exists
- a warning if the directory scan hit `wallet.ErrTooMuchHistory`
  (agentmcp caps a scan at 5,000 incoming payments), so the reputation
  numbers are incomplete

What a quote records for each job offer:

- job id, title, budget, tags, poster, offer price, seller, offer tx
- escrow would be the payment rail
- track record, when cheap to compute: how many delivered jobs on the
  fetched board list name this seller and show a released escrow.
  That number is a hint. It is not mixed into the API ranking

Services that check mandates (`manifest.mandates` of `required`) are
quoted. They are payable in the phase that sends `X-AETHER-MANDATE`
from `--mandate-file`. Until then they are `needs_mandate` and are
not selected.

## Comparison and ranking

Filter first. An offer that fails a filter is still printed, with the
reason, and is not selectable.

An API offer is selectable when all of these hold:

- The live 402 includes `scheme=exact` and `network=cosmos:<chain-id>`
  (the configured chain; `aether-testnet-1` on the testnet).
- `amount` parses as a positive integer and `asset` is a denom the
  shopper knows (`uaeth`, or the configured USDC denom).
- `payTo` is an `aether1` address and equals the directory announcer.
  A mismatch is the `PAYEE_MISMATCH` case `buy_service` already
  refuses.
- `hasSplits` is false for both the 402's `extra` and the copy the
  client would echo into `accepted`.
- The live price is in an allowed asset and is at most `--max-price`
  for the request. `--max-price` is the budget for this request. The
  spend cap below is a separate, standing limit.
- The live price is at most the price the quote showed the caller.
  The shopper pays the 402 it just fetched, so this is checked again
  at buy time against the quote id.
- The path probed is not a free path.

Price source: the live 402, in base units. The manifest price is a
search hint. If the 402 asks more than the manifest, the offer is
`price_mismatch` and is not selectable, even when the 402 is under
`--max-price`. That is the benchmark clock, listed at 0.0005 AETH and
charging 0.004. If the 402 asks less than the manifest, the lower
price is the one compared and the difference is recorded. Prices in
different assets are not converted. A request denominated in AETH
does not select a USDC service.

Ranking among selectable API offers, in order:

1. **This payer's own history** for that listed URL, from the local
   purchase log: `(purchases − failed − receipt problems) /
   purchases`, weight 10, same shape as `trustScore`. No history
   scores 0, not a penalty.
2. **Trusted ratings only.** Average of ratings whose rater is the
   payer, the approver, or an address passed to `--trust`, weight 2.
   Ratings from anyone else are displayed and do not sort.
3. **Lower live price** in the request's asset.
4. **Listing height**, older first, then the URL, so the order is
   stable.

Payment count, payer count, and volume are printed and do not sort.
A seller can produce them for free while fees are zero.

Job offers are a separate list, sorted by offer price within the
request's asset. They are never inserted into the API ranking. A
0.0005 AETH API call and a 0.002 AETH custom job are different goods;
one "best" row across both would hide that. The quote names the
selected API (or says that nothing is selectable) and then lists job
offers under their own heading.

The selected offer is bound into a quote id: the hex sha256 of the
chain id, rail, URL or job id, method, request-body hash, `amount`,
`asset`, `payTo`, and scheme. `shopper buy` pays that id. If a fresh
402 disagrees with any of those fields, the buy stops with
`quote_stale` and does not sign.

## Spend cap and approval gate

The owner sets the policy in flags or a config file on the machine
that holds the key. An MCP argument cannot raise a cap, add a payee,
or turn unattended payment on. The tool schema simply does not have
those fields.

Standing limits, per asset (no conversion):

| Flag | Meaning | Suggested testnet default |
|---|---|---|
| `--max-per-payment` | Most one payment may be and still be signed unattended | `0.01 AETH` |
| `--daily-cap` | Most settled or possibly-settled spend in a rolling 24h | `0.1 AETH` |
| `--hard-max` | Most one payment may be even after a person approves it | equal to `--max-per-payment` |
| `--allow-asset` | Assets that may be paid unattended | `uaeth` only |
| `--allow-payee` | If set, the only payees that may be paid unattended, and each must also be a current directory announcer | empty: every current verified listing |
| `--allow-url` | If set, the only URL prefixes that may be paid unattended | empty: any URL under an allowed payee's listing |
| `--auto` | Pay a selectable, under-cap, allowlisted offer in the same invocation | off |

`--max-price` on a single request can only narrow `--max-per-payment`.
It cannot widen it.

**Unattended pay** (`--auto`, and every check below true): the offer
is selectable, its live price is at most `--max-per-payment`, the
price plus the rolling spend is at most `--daily-cap`, the asset is
allowed, the payee is allowlisted, and the URL is allowlisted. The
shopper then signs.

**Stop for a person,** with nothing signed, when any of these is true:

- the price is above `--max-per-payment` or would exceed `--daily-cap`
- the asset, payee, or URL is outside the allowlist
- the URL is unlisted
- the rail is `job`
- `--auto` is off (the default): the quote is printed and a later
  `shopper buy --quote <quote-id> --confirm` is the person's approval for an
  in-policy purchase

**Refuse, not approvable,** when the price is above `--hard-max`, the
402 moved away from the quote id, the offer is not selectable
(`price_mismatch`, splits, payee mismatch, wrong network, missing
`exact`), or the idempotency key was already used for a different
quote. A refusal does not create an approval request.

Spend already counted against `--daily-cap`:

- a receipt whose chain lookup shows code 0
- a payment in `settlement_unknown` (it may still land)
- a payment a person approved, once it has been signed, until a lookup
  shows it can never land

A pre-broadcast rejection that the chain agrees is absent does not
count. The count is local, from the receipt log, per asset. The
shopper does not scan the chain to reconstruct a cap a deleted log
would forget: a missing log fails closed (unattended pay stays off
until the operator passes `--accept-empty-ledger` once, which is
recorded).

### How a person approves

Two paths. Both bind the approval to the quote id, the amount, the
asset, and the payee. An approval for a different amount is ignored.

**Interactive CLI.** `shopper quote` writes
`<state>/quotes/<id>.json` and prints the selected row. `shopper buy
--quote <id> --confirm --idempotency-key <key>` pays it when it is in policy.
A confirmation from a pipe, or from MCP, is not accepted: the MCP
tool cannot pass `--confirm`. When `--auto` is off, this second
command is the approval the shopping-assistant analogy asks for,
inside the cap.

**Out of policy.** The shopper writes `<state>/approvals/<id>.json`
(payee, amount, asset, URL, quote id, idempotency key, created time)
and returns `approval_required`. `shopper approve <id>` signs the
decision with the approver key (`--approver-key`,
`--approver-keyring-dir`, its own `--keyring-backend`). The signed
payload is the domain `aether-shopper-approval/v1\n` plus the id,
the decision, and the quote id, the same shape as
`aether-agentmcp-approval/v1`. The spending key cannot produce a
valid decision. Decisions live in their own files, mode 0600, TTL 24
hours. `shopper reject <id>` signs a rejection; a rejected id is not
retried with the same idempotency key.

A signed approval authorizes that one payment, up to `--hard-max`. It
does not raise the standing caps. The next over-cap payment stops
again.

Job-board hires, when a later phase can submit them, use this same
gate and never `--auto`. The amount approved is the escrow amount,
which stays locked until release or expiry. The quote says that in
the line the person approves.

## Keys

The shopper signs on the machine where the key lives. It opens the
keyring the way `agentmcp` and `outbound` do.

```
--keyring-backend file|test
--keyring-dir <dir>
--from <key name>
--keyring-passphrase-file <path>   # overrides the env var; for a Windows task that does not pass the environment
```

`--keyring-backend file` reads the passphrase from
`--keyring-passphrase-file` or from
`AETHER_KEYRING_PASSPHRASE_FILE`, through `wallet.KeyringInput`:
the file is mode 0600, the passphrase is at least 8 characters, and
it is a single line. A `test` backend is for a devnet throwaway. The
testnet checklist below uses `file`.

The signer is the key that `wallet.SignsFor` accepts for the payer
right now, so a rotated account signs with the stored key and a
retired key is not used (`docs/PQ-KEY-ROTATION.md`). The shopper does
not rotate keys and does not write `signs-for.json`.

What is never written to a log, a receipt, stdout, or an error
string:

- the passphrase, the mnemonic, or the key seed
- the private key or the raw ML-DSA signature bytes
- the `PAYMENT-SIGNATURE` header and the signed tx bytes

The signed tx bytes live in the state file for that idempotency key,
mode 0600, directory mode 0700, because a retry has to send the same
bytes. Logs name the tx hash, the payee, the amount, the asset, and
the state. A crash dump that would include the header is not enabled.

One account has one payment in flight. The shopper remembers the
sequence it signed and does not build another transaction for that
account until the in-flight hash is known to be in a block or known
to have been rejected before broadcast. That matches the test
wallet's `pendingNext` and the facilitator's rule that a sequence
gap fails simulation.

Gas limit 400,000 and a fee of `0uaeth`, matching `buy_service`. If
the node charges a minimum gas price, `/verify` returns
`invalid_exact_cosmos_payload_fee` before broadcast. The shopper
records that and does not invent a higher fee: a higher fee would be
a second signature.

## Paying

Settlement goes through the public facilitator, then the same signed
bytes are shown to the resource so it will serve the response.

1. Re-fetch the 402. Require it to match the quote id.
2. Re-check cap, allowlist, splits, and payee. Then sign one
   `MsgSend`, once, and store the bytes under the idempotency key.
3. `POST /verify` with that payload and the 402's requirements. A
   valid result does not broadcast. An invalid result stops. The
   chain is asked whether that hash is already in a block; if it is
   not, nothing was spent.
4. `POST /settle` with the same body. This is the broadcast. The
   shopper waits for the facilitator's answer.
5. Look the hash up on the node (`GetTransactionByHash` /
   `aetherd q tx`). The lookup, not the HTTP body alone, decides
   whether the payment landed.
6. Repeat the original request with `PAYMENT-SIGNATURE` set to the
   same payload. The resource's paywall will try to settle those
   same bytes again. That second broadcast is the same transaction.
   If the paywall answers 402 with `payment_not_confirmed`, wait and
   repeat step 6 with the same header. If it answers
   `invoice_already_redeemed` / the exact scheme's already-used
   error, the payment was spent and the response was already given:
   return the stored receipt, do not sign again.

The facilitator is the broadcaster the shopper calls. The paywall's
follow-up attempt carries the same bytes, which the chain executes
once.

`accepted` in the payload is a copy of the 402 entry that was quoted,
including `extra`. The facilitator rejects a payload whose accepted
amount, asset, or payee differs from the requirements
(`invalid_exact_cosmos_payload_accepted_mismatch`).

Timeouts: the HTTP client for `/settle` is longer than the
facilitator's wait (facilitator default 80s, client 150s), in line
with `docs/X402.md`. A resource call uses 30s and does not follow
redirects (`directory.SafeHTTPClient`).

USDC, when `--allow-asset` names it, uses the testnet denom
`ibc/064D82A67318DD30F54A4E17B6E487C2864E8B075A5871D3ACAD7A7129F32C5C`
(`wallet.TestnetUSDC`, 6 decimals). The fee is still `uaeth`. The
inflow cap is the chain's business; the shopper only checks that its
own cap allows the amount.

## Receipts

A payment is recorded when step 5 has been attempted, and marked
`settled` only when the lookup shows code 0.

`<state>/receipts/<txhash>.json`, mode 0600:

| Field | Source |
|---|---|
| `chainId`, `network` | config, `cosmos:aether-testnet-1` |
| `payer`, `payTo`, `asset`, `amount` | the `MsgSend` that was signed |
| `rail` | `api` |
| `resource` | method, URL, sha256 of the request body |
| `quoteId`, `idempotencyKey` | the buy |
| `txHash` | `x402.TxHash` of the signed bytes, which is the chain's hash |
| `facilitator` | the `x402.SettleResponse` from `POST /settle`: `success`, `transaction`, `payer`, `amount`, `network`, `errorReason` |
| `chain` | lookup: found, code, height |
| `httpStatus` | the resource's status after the paid request |
| `paymentResponse` | the resource's `PAYMENT-RESPONSE` header, when it parses as `x402.SettleResponse` |
| `sellerReceipt` | the `X-PAYMENT-RECEIPT` header, when present, plus `verified` and `problem` from `paywall.CheckReceipt` against the request and response the shopper actually sent and received |
| `quoted` | manifest price, live price, rank notes (trusted rating count, own history). Enough to see why this offer was chosen |

The receipt does not contain the signed bytes, the key, or the
`PAYMENT-SIGNATURE` header. Anyone with the hash can check the chain:
one `MsgSend` of `amount` of `asset` from `payer` to `payTo`, code 0,
and `facilitator.transaction` equal to that hash. The seller receipt
is extra evidence about the response. Many current paywalls do not
sign one (`--receipts` is optional). A missing seller receipt is
`sellerReceipt: null`, and the payment receipt is still complete.

`shopper receipt <txhash>` prints the file and re-checks the chain.
A mismatch (file says settled, chain says absent or a non-zero code)
is printed as `receipt_mismatch` and the local file is not quietly
rewritten.

Job receipts, in the phase that can hire, use the same file with
`rail: job`, the escrow id, the hire tx hash, and no
`SettleResponse`. They are clearly not x402 settlements.

## Failure and retry

One idempotency key, one signature, for the life of the state
directory. The key is required (1 to 128 characters). The same key
with the same quote id resumes. The same key with a different quote
id is `idempotency_conflict` and signs nothing.

Before any resume, and before any new signature on that account, the
shopper looks up every in-flight hash and acts on what the chain
says.

| Situation | What happened | What the shopper does |
|---|---|---|
| Lookup finds the hash, code 0 | Paid | Do not sign. If the resource was not served, repeat the paid request with the stored signature. Write or complete the receipt |
| Lookup finds the hash, code ≠ 0 | Included and failed | Do not sign again under this key. Receipt state `failed` |
| `/settle` returns `invalid_transaction_state`, or the HTTP call dies after `/settle` was sent | May have been broadcast | State `settlement_unknown`. Counts against the daily cap. Resume only polls the chain and, once the tx is in a block, retries the resource with the same signature |
| `/verify` or `/settle` refuses before broadcast (`invalid_payload`, `insufficient_funds`, `invalid_exact_cosmos_payload_fee`, sequence, signature, splits, accepted mismatch) and the lookup misses | Not paid | State `rejected`. This key is finished. A new attempt is a new idempotency key, after the caller has seen the reason |
| Facilitator HTTP 503 or `/healthz` failing, and no `/settle` was sent | Not paid | State `facilitator_unavailable`. Same key may retry `/verify` and `/settle` later. No second signature |
| Resource HTTP 5xx, or a body that says to retry, after a code-0 payment | Paid, response failed | Up to 3 retries of the HTTP request with the same signature, spaced 10s apart. Then return the receipt with the last status. The paywall releases an exact payment for reuse when it itself returned 5xx (`paywall/exact.go`) |
| Resource says the payment was already redeemed | Paid, and already served | Return the stored receipt |
| 402 on resume whose `amount`/`payTo`/`asset` differ from the stored quote | The service changed its price | Do not sign. `quote_stale` |
| Two shopper processes | — | The state directory is locked with a file lock for the whole sign-and-settle section |

The rule in one line: a retry sends the stored signature or sends
nothing. It never builds a second transaction because a response
failed, a facilitator timed out, or a body asked it to.

## Interface

### CLI

`shopper` reads a config file (`--config`, default
`$HOME/.aether-shopper/config.json`, mode 0600) for the standing
policy and the endpoints. Flags override the file for that
invocation, except that an MCP server does not expose the policy
flags at all.

Endpoints, with the testnet defaults:

```
--chain-id aether-testnet-1
--grpc grpc.157-245-252-221.sslip.io:443
--facilitator https://explorer.157-245-252-221.sslip.io/facilitator
--job-board https://explorer.157-245-252-221.sslip.io/jobs
--state-dir ~/.aether-shopper
```

Commands:

```
shopper quote --query "block" --max-price "0.1 AETH" [--url URL] [--method GET] [--body-file f]
shopper buy --quote <quote-id> --idempotency-key <key> [--confirm]
shopper approve <approval-id> --approver-key <name> --keyring-backend file --keyring-dir <dir>
shopper reject <approval-id> ...
shopper status <idempotency-key>
shopper receipt <txhash>
```

`quote` needs no key. It prints two lists (APIs, then jobs), the
selected API if one is selectable, the quote id, and whether a buy
would be unattended, need confirmation, need an out-of-policy
approval, or be refused. Exit codes: 0 quoted, 2 bad flags, 3 nothing
selectable, 4 directory or board unreachable.

`buy --quote` pays that id. With `--auto` off, an in-policy quote
also needs `--confirm` on this command. With `--auto` on, an
in-policy quote pays without `--confirm`. `--auto` is read from the
config file; repeating it on the command line of a one-off invocation
is allowed for the CLI and is not an MCP argument. An out-of-policy
quote ignores both `--auto` and `--confirm` and exits as
`approval_required`.

`buy` needs the spending key. Exit codes: 0 settled, 2 bad flags, 3
a person has to act (stdout says `confirm_required` when the quote
is in policy and `--confirm` was left off, or `approval_required`
with the approval id when it is out of policy), 4
`facilitator_unavailable` or `settlement_unknown`, 5 refused
(`quote_stale`, over `--hard-max`, splits, payee mismatch). A human
script can branch on these without parsing logs.

`status` and `receipt` need no key. `status` reports the state and
the hash, and refreshes `settlement_unknown` from the chain.

### MCP

Phase 4 adds three tools to the local `agentmcp` only (the process
that already holds `--keyring-backend` and the spend limits). They
are not added to the public `/mcp`, the A2A card, or the hosted test
wallet.

| Tool | Effect | Arguments |
|---|---|---|
| `shop_quote` | read-only | `query`, `maxPrice`, optional `url`, `method` |
| `shop_buy` | pays when the owner's config allows | `quoteId`, `idempotencyKey` |
| `shop_status` | read-only | `idempotencyKey` |

`shop_buy` has no `auto`, `maxAmount` override, or `payTo`. The cap
and allowlist are the server's flags. Under policy with `--auto` set
in that config, `shop_buy` pays. Otherwise it returns
`approval_required` and the owner runs `shopper approve` (or
`shopper buy --quote <id> --confirm` when the quote is in policy and `--auto` is
just off). The response body is marked untrusted, the same warning
`buy_service` and `fetch_paid` already carry. Descriptions, titles,
and offer notes in `shop_quote` carry that warning too.

`shop_buy` is idempotent on `idempotencyKey`: a repeated call resumes
the stored signature.

### Library

`package shopper` owns discovery, ranking, the state machine, signing,
facilitator calls, and receipts. `cmd/shopper` and the MCP tools are
thin. Policy checks are functions in the package, so a test can run
them without a keyring. The package takes a `directory.Directory`, an
HTTP client built by `directory.SafeHTTPClient`, a `wallet.Wallet`,
and a facilitator base URL. It does not import `cmd/`.

## Test plan

Unit tests ship with each phase and do not touch the testnet. They
use `httptest` servers for a paywall-shaped 402, a facilitator, and a
job board.

- Ranking: own history beats a fake high volume; trusted ratings
  sort; unknown-account ratings do not; a lower price breaks a tie;
  USDC is not chosen for an AETH request.
- Filters: splits (non-empty array, and a non-array `splits`), payee
  mismatch, 402 above the manifest, 402 above `--max-price`, missing
  `exact`, wrong network, free path, empty query.
- Caps: under cap proceeds only when `--auto` and the payee are
  allowed; over cap yields `approval_required` and the signer is not
  called; over `--hard-max` yields refused and writes no approval
  file; daily cap counts `settlement_unknown`.
- Approval: a decision signed by the spending key does not pass; a
  decision for a different quote id does not pass; expiry does not
  pass.
- One signature: a 502 after `success` retries the HTTP call and
  leaves the signed-bytes count at 1; a facilitator timeout leaves
  the count at 1 and the next call only looks up the hash; a
  pre-broadcast rejection does not sign a replacement under the same
  key; a second process's lock prevents a concurrent sign.
- Receipt: settled file contains the hash and the `SettleResponse`;
  `CheckReceipt` problems are stored and do not hide a code-0 chain
  result; logs from a test logger do not contain the passphrase or
  the signature header.
- Safe fetch: a listing URL that resolves to a loopback address is
  dropped (`directory.ErrPrivateAddress`).

Live testnet, run by a person from a workstation, against public
endpoints only. Nothing in this plan SSHes to the seed, restarts a
unit, or runs `scripts/agentservices/`. CI does not run this list.
Steps 1–3 are read-only and can run at phase 1. Step 4 needs the
phase 2 binary and must not sign. Steps 5–6 are the phase 2 spend.
Steps 7–8 need phase 3. Step 9 is a unit test and stays off the
public paywalls; it is listed here so the live session does not try
to reproduce it on the seed.

Preparation: a new key in a `file` keyring, funded once by the public
faucet, that is not a validator operator, not the service payee
(`aether1e6r6g8mat6aqax9l29wwmrfhp52hjzrpwevqqq9tcj0lm8egdhjs9q44xl`),
and not a faucet key. Caps at the defaults above. `--auto` off until
the quote looks right.

1. `GET /facilitator/supported` shows `exact`, `cosmos:aether-testnet-1`,
   and `extensions: []`. `GET /facilitator/healthz` is 200.
2. `shopper quote --query block --max-price "0.1 AETH"` lists pulse
   (0.0005 AETH; its description says "block time" and "block
   reward") and does not list miners or the unlisted decoys. The
   selected row's live amount equals its manifest amount. Job
   offers, if any match, appear under the job heading. A second
   quote with `--query miner` lists miners.
3. `shopper quote --query "block-data feed" --max-price "0.1 AETH"`
   selects nothing. That is the current matcher, and the test records
   it so a later synonym change is deliberate.
4. `shopper buy --quote <id>` without `--confirm`, while `--auto` is
   off, exits 3 and does not change the account sequence.
5. Quote `hello` (0.000001 AETH, the practice service). Confirm the
   payee is the services payee and the amount is `1` uaeth. `shopper
   buy --quote <id> --confirm --idempotency-key hello-1` settles. The receipt's
   hash is a successful `MsgSend` of 1 uaeth on the public RPC. The
   facilitator's `transaction` field equals that hash. The HTTP body
   is the hello payload.
6. The same command again, same key, does not create a second hash.
   `status` shows one payment.
7. A quote whose only selectable price is above `--max-per-payment`
   (temporarily set `--max-per-payment` to `1uaeth` and query `pulse`)
   returns `approval_required`. The sequence is unchanged until
   `shopper approve` and a resume. With `--hard-max` still `1uaeth`,
   that approve is a refuse. Restore the defaults afterward.
8. An unlisted URL (any `https` host that 402s, or a local httptest
   recorded in the notes) is `approval_required` even under the cap,
   and is not signed.
9. A local httptest 402 with `extra.splits` set to `[{}]` is refused
   with no signature. This check stays off the public services,
   because they do not send splits.

Stop there. Do not buy inference, do not post a job, and do not send
a second hello payment to "see if retry works" — the unit tests cover
the retry. One uaeth on hello is the live spend.

## Phased implementation

Sizes are rough lines of Go including tests, in this module, no new
`go.mod`, no proto, no consensus change, no edit under
`scripts/agentservices/` or the other deploy scripts. Each phase is
its own PR and is useful if the later ones wait.

| Phase | What lands | Size | Pays? |
|---|---|---|---|
| 0 | This document | — | No |
| 1 | `package shopper` discovery, filters, ranking, quote id; `shopper quote`; httptest coverage of the filters | 600–900 | No |
| 2 | Sign, `/verify`, `/settle`, chain lookup, receipt files, idempotency, cap and allowlist as refuses (no approver yet), `--keyring-backend file`, `shopper buy` for in-policy quotes with `--confirm` | 900–1,300 | Yes, API rail, in policy only |
| 3 | Out-of-policy approvals, `--hard-max`, `shopper approve` / `reject`, daily-cap accounting of `settlement_unknown` | 400–600 | The same, plus approved exceptions |
| 4 | `shop_quote`, `shop_buy`, `shop_status` on local agentmcp, wired to the package. No public server changes | 250–400 | The same, from an agent that already has the key |
| 5 | Job rail: quote details already exist; this phase can draft a hire and, after approval, lock escrow through the existing escrow helpers, with a job receipt | 500–700 | Escrow, approval only |

Phase 2 is the first one that can run the live hello checklist.
Phase 1 can still be tried against the live directory and the live
board, read-only. Mandates (`--mandate-file`, send `X-AETHER-MANDATE`
when the manifest says `required`) fit in phase 2 if the file is
already the one `agentmcp mandate` writes; otherwise they wait, and
`required` services stay unselectable.

## Open questions

1. **Default of `--auto`.** This design leaves it off, so the first
   releases only pay after `shopper buy --quote <id> --confirm`, and an agent pays
   unattended only when the owner set `--auto` in the config. The
   cap rule also allows a reading where in-policy buys proceed
   without that second step. Which default do you want?

2. **`--hard-max`.** Defaulting it to `--max-per-payment` means a
   person can approve an unlisted payee or a job, but not a larger
   amount, until they edit the config. Should one approval be able
   to exceed the per-payment cap up to the daily cap instead?

3. **Jobs in the first payable release.** Phase 5 is escrow, always
   approved, and easy to drop if the shopper should only ever speak
   x402. Should the quote keep showing jobs until then, or should
   `--job-board` default to empty so the first CLI is APIs only?

4. **Matcher.** Literal words will miss "block-data feed" against
   pulse. A small operator-edited synonym file would fix the
   examples we care about. A model in the loop would read seller
   text. Is a synonym file worth a phase, or is the query the
   caller's job?

5. **Facilitator outage.** In-policy buys fail closed when the public
   facilitator is down, even though the seed paywalls settle in
   process and would still serve. Is fail-closed right, or should a
   down facilitator fall back to letting the paywall settle, still
   with one signature and a chain lookup?

6. **Trusted raters.** Default is the payer and `--approver`, plus
   `--trust`. Should the shopper ship with an empty trusted set so
   unknown ratings cannot influence a fresh install even by being
   displayed prominently? Display does not affect the sort either
   way.

7. **Where the MCP tools live.** Local `agentmcp` keeps one key and
   one policy. A separate `shopper --mcp` process would isolate the
   shopper's state from `fetch_paid`. Do you want it inside agentmcp
   or beside it?

8. **USDC unattended.** The default allowlist is `uaeth` only, so
   hello-usdc (0.001 USDC) is quoted and then `approval_required`.
   Should the testnet default allow USDC up to a small per-payment
   cap as well, given the inflow cap already bounds the voucher?

9. **Seller receipts.** They are optional on today's paywalls, and
   the shopper's own receipt does not depend on them. Is turning
   `--receipts` on for the six public paywalls in scope for a later
   ops change, or out of scope for this work entirely?

10. **State directory on Windows.** peer-1's host is Windows, and a
    file lock plus a 0600 mode is the whole double-pay guard. If the
    shopper is only required to run on the workstation that already
    runs `paybench`, Unix locking is enough. Should the first
    implementation target Linux and macOS only?
