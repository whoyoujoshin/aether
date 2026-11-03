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

The shopper is a library (`shopper`), a CLI (`cmd/shopper`), a page
on the existing explorer for a person, and MCP tools on the local
key-holding `agentmcp`. The public read-only `/mcp` and the hosted
test wallet stay as they are. A person who has never used a chain
gets one search box and a Buy button. An agent gets the same offers
as machine-readable tools, and still cannot spend outside the cap.

## What it buys, and what it leaves alone

Two markets, two rails:

| Rail | Where it is found | How it is paid | Who may buy it unattended |
|---|---|---|---|
| `api` | On-chain service directory, confirmed by a live HTTP 402 | x402 v2 `exact`, one `MsgSend`, settled by `cmd/facilitator` | Only after the owner turns `--auto` on, and only when the price is within both caps, the asset is AETH, and the payee is allowlisted. Until then every buy waits for a person. USDC always waits for a person |
| `job` | Job board (`GET /jobs`) | Escrow, later (`hire_for_job` / `release_escrow`). The facilitator does not settle escrow | Shown in every quote. Not buyable until the escrow phase |

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
fee at the node's minimum gas price, in uaeth), and sends it as a
`PAYMENT-SIGNATURE` header. The paywall
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
interpreted, and it is not the source of synonyms. Seller text stays
attacker-controlled data.

A small file shipped in the repo, `shopper/synonyms.txt`, expands the
caller's words before that match. Each line is a phrase, a tab, and
the words to try instead (`block-data	block`, `chain pulse	block`,
`leaderboard	miner`). A phrase in the query is tried both as written
and replaced by its target. The listing matches if any of those
word-sets fits. With the shipped file, "block-data feed" matches
pulse, because both phrases collapse to `block`, and pulse's
description says "block time" and "block reward". It still does not
match miners. The owner can add lines; a listing cannot. The first
file also covers the other public services in the same style
(`practice	hello`, `balance	address`, `signature	verify`).

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
   payer or the approver, weight 2. That is the whole default set.
   `--trust` adds further addresses when the owner edits the config.
   Ratings from anyone else are available on the advanced toggle and
   in the agent payload, labeled untrusted, and do not sort.
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
| `--max-per-payment` | Most one payment may be. A higher price is refused. A person cannot approve past it | `0.01 AETH` |
| `--daily-cap` | Most settled or possibly-settled spend in a rolling 24h. A payment past it is refused | `0.1 AETH` |
| `--allow-asset` | Assets that may be paid after a confirm. USDC is never unattended | `uaeth` only |
| `--allow-payee` | If set, the only payees that may be paid, and each must also be a current directory announcer | empty: every current verified listing |
| `--allow-url` | If set, the only URL prefixes that may be paid | empty: any URL under an allowed payee's listing |
| `--auto` | After the owner sets this, an in-policy AETH payment can complete without a fresh confirm | off |

`--max-price` on a single request can only narrow `--max-per-payment`.
It cannot widen it. Both caps are hard. Raising either one is an edit
to the config file on the machine that holds the key. An approval, a
confirm, and the explorer page cannot raise them.

**Pay on confirm** (the default, `--auto` off): the offer is
selectable, its live price is at most `--max-per-payment`, the price
plus the rolling spend is at most `--daily-cap`, the asset is AETH,
the payee is allowlisted, and the URL is allowlisted. The shopper
signs only after `shopper buy --quote <id> --confirm`, or after the
person presses Buy on the explorer page. Nothing in policy is signed
on the first request.

**Pay unattended** only when the owner has set `--auto`, and every
check in the previous paragraph is true. USDC never takes this path.

**Stop for a person,** with nothing signed, when any of these is true:

- `--auto` is off, and the payment is otherwise in policy (a confirm)
- the asset is USDC (always, even with `--auto`, even under the cap)
- the payee or URL is outside the allowlist, or the URL is unlisted
- the rail is `job` (shown, and not buyable until the escrow phase,
  so this stop has no Approve button yet)

**Hard refuse,** with no approval request, when the price is above
`--max-per-payment`, the payment would exceed `--daily-cap`, the 402
moved away from the quote id, or the offer is not selectable
(`price_mismatch`, splits, payee mismatch, wrong network, missing
`exact`). A reused idempotency key for a different quote is also a
refuse. A refusal does not create an approval request.

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

Three paths. All of them bind the approval to the quote id, the
amount, the asset, and the payee. An approval for a different amount
is ignored. The explorer link is the third path and is described
under Human page. It checks the same signature.

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

A signed approval authorizes that one payment when it is still within
both caps. It does not raise them. The next payment outside an
allowlist stops again. A payment over either cap never becomes an
approval.

Job-board rows are visible and carry no buy button until the escrow
phase. When that phase lands, a hire uses this same gate, never
`--auto`, and the amount the person approves is the escrow amount,
which stays locked until release or expiry. The caps still apply, and
a hire over `--max-per-payment` is a hard refuse.

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

The state directory is locked with a file created `O_EXCL` (exclusive
create). That works on Linux, macOS, and Windows. A second process
finds the lock held and does not sign. The lock is removed on exit;
a lock left by a dead process is stolen only when its recorded pid
is gone. On Windows the passphrase is `--keyring-passphrase-file`,
because a scheduled task does not pass `AETHER_KEYRING_PASSPHRASE_FILE`
(the same reason `outbound` has that flag). The default state
directory is `~/.aether-shopper` on Unix and
`%USERPROFILE%\.aether-shopper` on Windows. Mode 0700/0600 is applied
where the filesystem has Unix modes; on Windows the directory stays
inside the user profile.

Gas limit 400,000. The fee is uaeth at the node's minimum gas price
for that gas (`wallet.Client.Fee` with the same `--gas-price` and
`--tip` as the other tools; `auto` and tip 1 by default), whatever
coin the price is paid in. A node that does not serve the Config
query is taken to ask nothing, so the fee is empty. `shopper buy
--dry-run` prints that fee (or that the node asks none) and does not
sign. The Shop page reads that fee from `GET /api/shop/price` (`feeAmount`, `feeDenom`)
and puts it on the same transaction. A retry sends the stored bytes
and does not sign a higher fee.

## Paying

The service's paywall is the broadcaster. Aether's paywalls settle
`PAYMENT-SIGNATURE` themselves and serve a response only for a payment
they broadcast (`docs/X402.md`). Settling through the public
facilitator first puts the transaction in a block. The paywall's
check then sees a used sequence and answers 402, and the body never
comes. The shopper therefore shows the signed payment to the resource
and does not call `POST /settle` first. `buy_service` does the same.

1. Re-fetch the 402. Require it to match the quote id.
2. Re-check cap, allowlist, splits, and payee. Then sign one
   `MsgSend`, once, and store the bytes under the idempotency key.
3. `POST /verify` with that payload and the 402's requirements. A
   valid result does not broadcast. An invalid result stops when the
   hash is not in a block. If the facilitator is down, nothing is
   sent to the service.
4. Repeat the request with `PAYMENT-SIGNATURE` set to that payload.
   The paywall settles it and, on success, returns the body and
   `PAYMENT-RESPONSE`. If it answers 402 with `payment_not_confirmed`,
   wait and repeat with the same header. A 5xx is retried the same way.
5. Look the hash up on the node (`GetTransactionByHash` /
   `aetherd q tx`). The lookup, not the HTTP body alone, decides
   whether the payment landed.

A 2xx whose transaction is in a block at code 0 is the only result
reported as paid. If the transaction is in a block at code 0 and the
resource is still 402, the command says the payment landed and the
service didn't deliver, prints the tx hash, and exits non-zero.
Running the same command again sends the same `PAYMENT-SIGNATURE`
and does not sign another transaction. That re-fetch gets the body
only when this paywall broadcast the payment (a 5xx after it
settled, or `payment_not_confirmed` before the block). A payment the
public facilitator broadcast, which this paywall did not, stays 402:
the paywall will not serve it, and a new purchase is a new
idempotency key.

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
| Facilitator HTTP 503, `/healthz` failing, or the facilitator unreachable, and no `/settle` was sent | Not paid | Fail closed. State `facilitator_unavailable`. The paywall is not used as a fallback broadcaster. Same key may retry `/verify` and `/settle` later. No second signature |
| Resource HTTP 5xx, or a body that says to retry, after a code-0 payment | Paid, response failed | Up to 3 retries of the HTTP request with the same signature, spaced 10s apart. Then return the receipt with the last status. The paywall releases an exact payment for reuse when it itself returned 5xx (`paywall/exact.go`) |
| Resource says the payment was already redeemed | Paid, and already served | Return the stored receipt |
| 402 on resume whose `amount`/`payTo`/`asset` differ from the stored quote | The service changed its price | Do not sign. `quote_stale` |
| Two shopper processes | — | The state directory is locked with a file lock for the whole sign-and-settle section |

The rule in one line: a retry sends the stored signature or sends
nothing. It never builds a second transaction because a response
failed, a facilitator timed out, or a body asked it to.

## Human page

A new person meets the shopper as a page on the explorer, not as a
CLI. The explorer is already the site they open. It already creates
a browser account on `/mine` (ML-DSA key, recovery phrase, faucet
proof-of-work) and it already draws a transaction at `/tx/<hash>`.
The Services page lists paid APIs and does not buy them. `/shop` is
that list plus a search box and a Buy button.

The explorer process stays a reader. It does not hold a spending key,
a cap, or an approval. Signing happens in the browser, with the key
the Mine page already proved can live in `localStorage`. The cap the
page enforces is the one stored next to that key. An agent cannot
raise it. The agent's own cap and approval requests live on the
computer running agentmcp, not on this server.

### What the person sees

Default copy uses plain words. The words x402, facilitator, exact
scheme, paywall, denom, and uaeth do not appear unless the person
opens Advanced. Prices are written as AETH (`0.0005 AETH`). A USDC
price is written as `0.001 USDC` and the button says Approve, not
Buy, because USDC always needs a person.

Nav: a "Shop" link in the explorer top bar, after Services. Routes:

| Route | What it is |
|---|---|
| `/shop` | Search, results, the cap line, Buy |
| `/shop/receipt/:hash` | One receipt, with the payment linked to `/tx/:hash` |
| `/shop/approve/:id` | The page a person opens from an agent's link |

The page is one column. At the explorer's existing 520px breakpoint
the search box, each offer, and the Buy button are full width. The
cap line sits under the title and wraps. Nothing scrolls sideways.
The top bar is the one the explorer already collapses.

```
Desktop and phone (one column)

+--------------------------------------------------+
| AETHER Explorer          Shop  Services  Mine    |
+--------------------------------------------------+
| Shop                                             |
| Today's spending   0.0005 of 0.10 AETH           |
| Limit per buy      0.01 AETH                     |
|                                                  |
|  What do you need?                               |
|  [ block data                            ] [Find]|
|                                                  |
|  Chain pulse                          0.0005 AETH|
|  Height, block time, difficulty        [ Buy ]   |
|                                                  |
|  Miner leaderboard                    0.0005 AETH|
|  Who mined this epoch                  [ Buy ]   |
|                                                  |
|  Name a cat                  Custom job, not yet |
|  A person offered 0.002 AETH                     |
|                                                  |
|  [ Advanced ]                                    |
+--------------------------------------------------+

Buy confirm (same page, the card replaces the list)

+--------------------------------------------------+
| Buy Chain pulse for 0.0005 AETH?                 |
| Paid to the service's account.                   |
| This stays inside today's limit.                 |
|                                                  |
| [ Buy ]                      [ Cancel ]          |
| Advanced: address, quote id                      |
+--------------------------------------------------+

Receipt

+--------------------------------------------------+
| Paid 0.0005 AETH for Chain pulse                 |
| Payment  ABCD…1234          [ View on explorer ] |
| That link is /tx/ABCD…1234                       |
| [ Advanced: settlement details ]                 |
+--------------------------------------------------+

Approve (opened from a link an agent sent)

+--------------------------------------------------+
| Allow this buy?                                  |
| Chain pulse for 0.0005 AETH                      |
| Asked by the agent on your computer.             |
| Still inside the limit (0.02 AETH left today).   |
|                                                  |
| [ Approve ]                  [ No ]              |
+--------------------------------------------------+
```

Empty and error copy, in the same place as the list:

| State | Words on the page |
|---|---|
| Nothing typed yet | The box, and one line: "Try block, miner, or hello." No offers yet |
| No match | "Nothing matched that. Try fewer words." |
| No wallet yet | "Create a wallet in this browser." Then the recovery-phrase step from `/mine` |
| No coins | "This wallet has no test coins yet." Button: "Get test coins" |
| Over the per-buy limit | "That costs more than your 0.01 AETH limit. Nothing was spent." No Approve |
| Over today's limit | "You've reached today's limit. Nothing was spent." |
| USDC | "This one is priced in test USDC. Approve it to continue." |
| Price changed | "The price changed. Look again before buying." |
| Payments paused | "Payments are paused. Nothing was spent. Try again in a minute." This is the facilitator being down. Fail closed |
| Not enough coins | "Not enough test coins for this buy." |
| Custom job | The row is visible. The button is absent. The line reads "Custom job, not ready to buy yet." |
| Already paid (same try) | "You already paid for this. Here is the receipt." |

Advanced, behind one toggle on each screen, shows the payee address,
the quote id, the asset code, and (on a receipt) the settlement
fields a developer would call the facilitator response. The default
view does not.

### Wallet

The naive path is the browser wallet, a sibling of the Mine page's
keystore, under its own storage key `aether-shop-key-v1`. Mining
rewards and shop spending stay in different accounts. The setup is
the Mine page's: generate a key, show the 24 words, require a saved
copy, then store the key in this browser. Anyone who can use the
browser can spend the test coins in it. The page says that in the
same words `/mine` already uses.

"Get test coins" runs the faucet the Faucet and Mine pages already
use (the proof-of-work challenge, then `POST` to the public faucet).
The person does not copy an address anywhere.

Two other ways in, both off the default path:

- "Use my miner account" loads the existing Mine keystore. One
  confirm: "Buys will spend the account you mine with."
- Advanced: paste a recovery phrase, or "this computer's shopper" for
  a person sitting at the machine that runs agentmcp. The hosted test
  wallet (a token, an MCP session) is not offered here. It is an
  agent tool.

The browser signs the payment and calls the public facilitator, then
the service, using the same steps as the CLI. The signed bytes and
the phrase are not sent to the explorer. The page's cap and today's
spend are the receipts in `localStorage`. A deleted browser profile
forgets them; unattended buy stays off until the person sets the cap
again (the same fail-closed rule as a missing ledger).

### Approval link

An agent that needs a person calls `shop_approve_request` and gets
back a link on the computer running the shopper, such as
`http://127.0.0.1:8470/approve/<id>`. The id is 128 random bits. The
summary stays in the shopper's state directory. The public explorer
does not store it. `shopper serve` on that computer shows the request
in plain words and, when started with the approver key, the Approve
button signs it there. A phone away from that computer cannot approve
through the seed. A relay for that, if one is ever wanted, would be a
separate optional service, not a validator.

The person opens that link on the same computer. `shop_status` on the
agent reads the local decision. A signature over a different quote or
a higher amount is ignored, and a price over the cap is never asked.

A webhook is optional and local. `--notify-webhook` on agentmcp is a
URL the owner set. The public explorer does not receive approval
decisions. Polling `shop_status` works with no webhook.

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
shopper buy --quote <quote-id> --idempotency-key <key> [--confirm] [--dry-run]
shopper approve <approval-id> --approver-key <name> --keyring-backend file --keyring-dir <dir>
shopper reject <approval-id> ...
shopper status <idempotency-key>
shopper receipt <txhash>
shopper budget
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

`buy` needs the spending key. `--dry-run` signs nothing and prints
the decision it would have made. Exit codes: 0 settled (or a dry-run
that would settle), 2 bad flags, 3 a person has to act (stdout says
`confirm_required` when the quote is in policy and `--confirm` was
left off, or `approval_required` with the approval id and the
explorer link when it is out of policy), 4
`facilitator_unavailable`, `settlement_unknown`, or `paid_not_delivered`, 5 refused
(`quote_stale`, over either cap, splits, payee mismatch, job not
buyable yet). `--json` prints the same structured error as the MCP
tools. A script can branch on the exit code without parsing logs.

`status` and `receipt` need no key. `status` reports the state and
the hash, and refreshes `settlement_unknown` from the chain.

### Agents

An agent can discover the shopper and drive it without a person
sitting beside it. Spending still follows the cap and the confirm
rule: with `--auto` off, the agent gets as far as a quote, a dry-run,
and a link the person taps. It does not get a tool argument that
turns `--auto` on, raises a cap, or adds a payee.

**Where it is written down.** The explorer's `/llms.txt` gains a
short "Shop" section: what the page is, `GET /api/shop/search`, the
OpenAPI path, and the local MCP tool names. The existing
`/api/openapi.json` (OpenAPI 3.1) gains the public read routes. Those
routes do not pay.

| Public route | Auth | Returns |
|---|---|---|
| `GET /api/shop/search?q=` | none | Ranked offers. Prices in AETH and in base units. Jobs included, `buyable: false` until the escrow phase |
| `GET /api/shop/offers/{quoteId}` | none | One offer, if the quote is still in the short public cache |
| `POST /api/shop/approvals` | none; rate-limited, one body per id | Stores a summary. Returns the public URL and a poll token |
| `GET /api/shop/approvals/{id}` | none | The plain summary and the status word. No signature |
| `GET /api/shop/approvals/{id}/decision` | poll token | The signed yes or no, for the agent to verify |
| `POST /api/shop/approvals/{id}/decision` | none (the body is a signature) | The browser's signed decision |

**MCP tools**, on local `agentmcp` only. Not on the public `/mcp`,
the A2A card, or the hosted test wallet. Each tool's input and
output is a JSON schema in the MCP tool definition, the same way
`find_services` and `buy_service` already publish schemas. Names:

| Tool | Reads or spends | Arguments |
|---|---|---|
| `shop_search` | read | `query`, optional `maxPrice` |
| `shop_quote` | read | `query` or `offerId`, optional `url`, `method`. Returns a `quoteId` |
| `shop_buy` | spends only when the rules say so | `quoteId`, `idempotencyKey`, `dryRun` (default false) |
| `shop_approve_request` | no spend | `quoteId`, `idempotencyKey`. Returns `approvalUrl`, `approvalId`, `expires` |
| `shop_status` | read | `idempotencyKey` or `approvalId`. The poll |
| `shop_receipts` | read | optional `limit` |
| `shop_budget` | read | none. Returns this identity's caps, spent today, left today, and whether `--auto` is on |

`shop_buy` has no `auto`, no cap override, and no `payTo`. `dryRun:
true` returns the decision and signs nothing. A repeated
`idempotencyKey` resumes the stored signature. Descriptions in the
results carry the same untrusted-data warning as `find_services`.

**Budgets per agent.** The account has one `--max-per-payment` and
one `--daily-cap`. A config block can give a named caller a tighter
daily cap, still inside the account cap:

```
[[agent_budget]]
name = "research"
daily_cap = "0.02 AETH"
```

The name is matched to the MCP client's name. An unknown client uses
the account cap. No tool argument selects a looser budget. USDC
spends count against an USDC daily figure the owner set the same way,
and still require a person every time. `shop_budget` reports the
identity that matched.

**Errors.** Failures are a JSON object, on MCP and on `shopper
--json`, with a stable `code` and a `next_action` the caller can
branch on. `spent` is false unless a signature has already been
broadcast.

| `code` | `next_action` | Meaning |
|---|---|---|
| `NEEDS_CONFIRM` | `confirm_or_open_url` | In policy, `--auto` is off. `approvalUrl` is set |
| `NEEDS_APPROVAL` | `open_approval_url` | Out of policy (unlisted payee, and USDC). Under the caps |
| `USDC_NEEDS_APPROVAL` | `open_approval_url` | USDC, even with `--auto` |
| `OVER_CAP` | `pick_cheaper_or_edit_config` | Over `--max-per-payment`. No approval exists |
| `DAILY_CAP` | `wait_or_edit_config` | Would exceed today's cap |
| `NOT_BUYABLE` | `pick_another` | A job, until the escrow phase |
| `NOTHING_FOUND` | `broaden_query` | No selectable offer |
| `QUOTE_STALE` | `shop_quote_again` | The live price moved |
| `PAYEE_MISMATCH` | `pick_another` | The 402 names a different account than the listing |
| `FACILITATOR_DOWN` | `retry_later` | Fail closed. Nothing signed |
| `INSUFFICIENT_FUNDS` | `get_coins` | The account cannot cover the price |
| `IDEMPOTENCY_CONFLICT` | `new_idempotency_key` | This key was used for a different quote |
| `SETTLEMENT_UNKNOWN` | `poll_status` | May have been broadcast. Same key only |
| `ALREADY_PAID` | `read_receipt` | This key already settled and the service answered |
| `PAID_NOT_DELIVERED` | `refetch_same_proof` | The transaction landed and the service did not return the body. Same key, same proof, no new payment |
| `DRY_RUN` | `submit_without_dry_run` | The dry-run would have continued. Nothing signed. The message includes the fee |

`message` is the same plain sentence the explorer page shows.

**Polling and webhooks.** `shop_status` is the default. The agent
calls it until the status is `approved`, `rejected`, `expired`,
`settled`, or `refused`. After the agent has verified a decision,
`--approval-webhook` (owner config, not a tool argument) receives
one POST of that status JSON. A webhook the agent invents at call
time is ignored.

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
- Synonyms: "block-data feed" matches pulse via `shopper/synonyms.txt`
  and does not match miners. A line in a listing cannot add a synonym.
- Caps: with `--auto` off, an in-policy buy returns `NEEDS_CONFIRM`
  and the signer is not called; with `--auto` on, that buy signs.
  Over `--max-per-payment` or the daily cap returns `OVER_CAP` or
  `DAILY_CAP` and writes no approval file. USDC returns
  `USDC_NEEDS_APPROVAL` even with `--auto`. Daily cap counts
  `settlement_unknown`. A named agent budget cannot exceed the
  account cap.
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
   selects pulse, through the shipped synonym file, and does not
   select miners.
4. `shopper buy --quote <id>` without `--confirm`, while `--auto` is
   off, exits 3 and does not change the account sequence.
5. Quote `hello` (0.000001 AETH, the practice service). Confirm the
   payee is the services payee and the amount is `1` uaeth. From a
   throwaway file keyring that is not a validator key and not the
   service payee:

   ```
   shopper buy --quote <id> --confirm --idempotency-key hello-1 \
     --from buyer --keyring-backend file --keyring-dir <dir> \
     --keyring-passphrase-file <passfile>
   ```

   The receipt's hash is a successful `MsgSend` of 1 uaeth on the
   public RPC. The facilitator's `transaction` field equals that hash.
   The HTTP body is the hello payload. Nothing in the shopper's log
   or stdout contains the passphrase or the signed transaction.
6. The same command again, same key, does not create a second hash.
   `status` shows one payment.
7. With `--max-per-payment` temporarily `1uaeth`, a pulse quote
   (0.0005 AETH) returns `OVER_CAP`. No approval file is written and
   the sequence is unchanged. Restore the default afterward. A
   hello-usdc quote under the default caps returns
   `USDC_NEEDS_APPROVAL` even if `--auto` is on.
8. An unlisted URL (any `https` host that 402s, or a local httptest
   recorded in the notes) is `NEEDS_APPROVAL` even under the cap,
   and is not signed. The returned link opens `/shop/approve/<id>`
   and shows the plain-language request.
9. A local httptest 402 with `extra.splits` set to `[{}]` is refused
   with no signature. This check stays off the public services,
   because they do not send splits.

Stop there. Do not buy inference, do not post a job, and do not send
a second hello payment to "see if retry works" — the unit tests cover
the retry. One uaeth on hello is the live spend. On Windows, phase 2
also runs `shopper quote` and `shopper buy --dry-run` on the
maintainer's PC. That run does not spend.

## Phased implementation

Sizes are rough lines including tests. Go stays in this module (Go
1.26.10, no new `go.mod`, no proto, no consensus change). The page is
TypeScript in `explorer-web`. No edit under `scripts/agentservices/`
or the other deploy scripts. Each phase is its own PR and is useful
if the later ones wait.

| Phase | What lands | Size | Pays? |
|---|---|---|---|
| 0 | This document | — | No |
| 1 | `package shopper` discovery, filters, ranking, quote id, `shopper/synonyms.txt`; `shopper quote`; read-only `GET /api/shop/search` on the explorer; httptest coverage | 900–1,200 Go | No |
| 2 | Sign, `/verify`, `/settle`, chain lookup, receipt files, idempotency, hard caps, `--keyring-backend file`, Windows `O_EXCL` lock, `shopper buy --confirm` and `--dry-run` | 900–1,300 Go | Yes, AETH, in policy, after confirm |
| 3 | Approvals, USDC always needs a person, local approval files, `shopper approve` / `reject` | 500–800 Go | The same, plus approved exceptions under the caps |
| 4 | Explorer page `/shop`: search, ranked list, cap line, browser wallet, faucet, Buy / Approve, receipt linked to `/tx/<hash>`, empty and error copy, mobile, Advanced toggle | 1,200–1,800 TS, plus about 200 Go to serve the page's read routes | Yes, from the browser wallet, same rules |
| 5 | Local agentmcp tools `shop_search`, `shop_quote`, `shop_buy`, `shop_approve_request`, `shop_status`, `shop_receipts`, `shop_budget`; per-agent budgets; structured errors; webhook; `/llms.txt` Shop section; OpenAPI for the public shop routes | 800–1,200 Go | The same, from an agent that already has the key |
| 6 | Jobs become buyable: escrow through the existing helpers, still after approval, never `--auto`, still under the caps | 500–700 Go | Escrow, approval only |

The search page can be clicked through as soon as phase 1 is up; the
Buy button waits for phase 4, which needs phases 2 and 3. Phase 2 is
the first one that can run the live hello checklist. Mandates
(`--mandate-file`, send `X-AETHER-MANDATE` when the manifest says
`required`) fit in phase 2 if the file is already the one `agentmcp
mandate` writes; otherwise they wait, and `required` services stay
unselectable. Seller-signed paywall receipts stay out of scope: the
shopper stores one if a service sends it, and nobody turns
`--receipts` on for the public paywalls as part of this work.

## Decisions

These were open in the first draft. They are settled:

1. `--auto` is off. Every buy needs a confirm until the owner turns
   `--auto` on in the config.
2. `--max-per-payment` and `--daily-cap` are hard. A person cannot
   approve a larger amount. Raising them is a config edit.
3. Job-board offers are shown from the first quote. They are not
   buyable until the escrow phase.
4. A small `shopper/synonyms.txt` ships with the library. It expands
   the caller's words. Listings cannot add synonyms.
5. If the facilitator is down, the buy fails closed before the
   payment is sent. The shopper does not broadcast. The service's
   paywall settles the `PAYMENT-SIGNATURE` it is shown; settling
   through the public facilitator first makes that paywall answer 402.
6. The trusted raters are the payer and the approver. `--trust` adds
   more only when the owner edits the config.
7. The MCP tools live in local agentmcp.
8. Testnet USDC always needs a person, even under the cap, even with
   `--auto`.
9. Seller-signed receipts on the public paywalls are later, and out
   of scope for this work.
10. Linux, macOS, and Windows. The lock is an exclusive create, and
    Windows takes `--keyring-passphrase-file`.

## Open questions

The earlier ten are settled in Decisions, including these two that came up while building:

1. **Approval storage.** Summaries stay on the computer running the shopper or agentmcp, or in the browser for the Shop page. They are not stored on the seed. A shared relay, if one is ever needed so a phone can approve a remote agent, would be a separate optional service, not a validator.
2. **Live prices.** Search uses the directory and its short cache. A quote fetches the live price only for the shortlisted offers. A buy always re-checks that one price before signing. Search does not contact every paywall.
