# Spending mandates

A spending mandate is an owner's signed statement that an agent may pay
on their behalf: **up to so much per payment, only to these payees, until
this time.** The owner signs it with their own ML-DSA key. The agent
sends it with every paid request. A seller can then check, before taking
any payment, that the agent answers to an owner and is acting within what
that owner allowed.

It's the missing half of an agent's limits. agentmcp's caps and on-chain
grants stop an agent from overspending, but a seller can't see them. A
mandate is the part a seller can read and verify for itself, with nothing
but the owner's address.

## What it is, and isn't

- **A statement, not a lock.** The chain doesn't enforce a mandate. What
  binds the agent is still its owner's own controls: agentmcp's
  `--per-tx-limit` and `--daily-limit`, approvals, or an on-chain grant.
  A mandate gives the seller an accountable owner and the scope that
  owner signed.
- **Per payment, checkable by anyone.** `maxPerPayment`, `payTo`,
  `notBefore` and `expires` can be checked by every seller on every
  request. `maxTotal` is stated for the record: one seller sees only its
  own payments and can't add up the rest.
- **Revoked by expiry.** There is no revocation list in version 1, so
  keep mandates short (the default is 30 days) and sign a new one to
  change the terms.

## The format

JSON, sent base64-encoded in the `X-AETHER-MANDATE` header:

```json
{
  "v": 1,
  "network": "aether-testnet-1",
  "id": "9f1c…",
  "owner": "aether1owner…",
  "agent": "aether1agent…",
  "maxPerPayment": "50000",
  "maxTotal": "2000000ibc/064D82A6…2C5C",
  "payTo": ["aether1seller…"],
  "notBefore": 1760788800,
  "expires": 1763380800,
  "purpose": "research assistant",
  "ownerPubKey": "<base64 ML-DSA-44 public key>",
  "signature": "<base64>"
}
```

- **Amounts** are written as receipts write them: a whole number of uaeth,
  or the amount in base units followed by its denom (for USDC, the IBC
  denom).
- **`payTo`** may be left out, meaning any payee.
- **`agent`** is the account the payments come from. In agentmcp's grant
  mode that's the granter.

The owner signs these lines, each ending in a newline, after the line
`aether-x402-mandate/v1`: `v`, `network`, `id`, `owner`, `agent`,
`maxPerPayment`, `maxTotal`, `payTo` (comma-separated), `notBefore`,
`expires` and `purpose`. A field with a line break, or a payee with a
comma, is refused, so the lines can't be shifted.

A mandate is valid when:
- `ownerPubKey` is the owner's address's key and the signature checks;
- the network is the seller's;
- the current time is in [`notBefore`, `expires`);
- the price is in `maxPerPayment`'s asset and at most that amount;
- the seller's payee is in `payTo`, when `payTo` is set;
- after payment, the payer is `agent`.

The Go implementation is `paywall/mandate.go`.

## The owner: signing one

The owner runs `agentmcp mandate` wherever their key is, never on the
agent's machine:

```bash
agentmcp mandate --owner-key me --keyring-dir ~/.aether \
  --agent aether1agent… --max-per-payment "0.05 AETH" --max-total "5 AETH" \
  --pay-to aether1seller… --valid-for 720h --purpose "research assistant" --out mandate.json
```

`--agent` is the agent's paying address (its `get_agent_address`). Copy
`mandate.json` to the agent's machine.

## The agent: carrying one

```bash
agentmcp --mandate-file mandate.json …
```

agentmcp checks the file at startup: it must verify and be for this
chain. From then on it sends the mandate with every `fetch_paid` request
(plain, prepaid and pull) while it is in date and names this agent's
paying account. `get_mandate` shows the mandate and whether it is being
sent. A seller's refusal comes back as `MANDATE_REFUSED` with the reason,
and nothing is paid.

## The seller: checking them

```bash
paywall … --mandates checked     # check mandates that are sent
paywall … --mandates required    # sell only to agents with one
paywall … --mandates required --mandate-owners aether1a…,aether1b…   # only these owners
```

**Before payment**, the paywall checks:
- the signature;
- the network;
- the validity window;
- the price against `maxPerPayment`;
- its own payee against `payTo`.

It refuses with 403 (`MANDATE_REQUIRED` or `MANDATE_REFUSED`) before
asking for payment. **After payment** it checks that the payer is the
mandate's agent. It then tells the upstream the owner in
`X-Aether-Mandate-Owner`; a caller can't set that header itself. The
manifest says `"mandates": "checked"` or `"required"`, so agents know
before they call.

## Not yet

- The TypeScript and Python clients and the hosted test wallet don't send
  mandates yet. Test wallets have no owner to sign one.
- A revocation list, and shared test vectors for other languages, would
  come with a version 2 if mandates get used.
