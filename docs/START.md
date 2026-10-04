# Start here: a wallet, test funds and a first payment

No node, no mining, no validator. A bot needs a key, the faucet and the
public RPC, and this page has all three. **Testnet only: use disposable
keys, never anything of value.**

| | |
|--|--|
| Chain ID | `aether-testnet-1` |
| RPC (CometBFT) | https://rpc.157-245-252-221.sslip.io |
| Faucet | `POST https://faucet.157-245-252-221.sslip.io/request` with `{"address":"aether1..."}` |
| Balance | `GET https://explorer.157-245-252-221.sslip.io/api/address?addr=aether1...` (`balance` is in uaeth) |
| A transaction | `https://explorer.157-245-252-221.sslip.io/tx/<hash>` |

Amounts are integers in uaeth: 1 AETH = 1,000,000 uaeth. Addresses start
with `aether1`. Transactions are signed with ML-DSA-44 (post-quantum),
so generic Cosmos wallets and secp256k1 signers don't work: use one of
the three paths below, which do.

## 1. An MCP agent (Claude, Cursor, any MCP client)

```bash
go install github.com/whoyoujoshin/aether/cmd/agentmcp@latest   # Go 1.25+
agentmcp init   # a new key, test funds, and the MCP config to paste
```

No Go? Download `agentmcp` or the Claude Desktop bundle
`aether-wallet.mcpb` from the
[releases](https://github.com/whoyoujoshin/aether/releases). Then ask the
agent:

> Using the aether-wallet tools: get your address and balance. If you have
> under 1 AETH, call request_testnet_funds and wait until your balance
> shows it. Then send 0.001 AETH to aether1cdugwhxk9cktjsemm6yjrd6xtfsq9wkjvnef03ml4u6ltuv7edcs0eyjds with idempotencyKey
> "first-payment", wait for the transaction to confirm, and report its
> hash and https://explorer.157-245-252-221.sslip.io/tx/<hash>.

The wallet caps what the agent can spend (per payment and per day) and
can pay from your account under a limit the chain enforces instead; see
[Spending limits](#spending-limits-without-the-main-key).

## 2. TypeScript (Node 20+)

```bash
git clone --depth 1 https://github.com/whoyoujoshin/aether
(cd aether/clients/ts && npm install && npm run build)
npm install ./aether/clients/ts
```

```ts
// first.mjs -- plain JavaScript too, so no build step: node first.mjs
import { AetherClient, Key } from "@aether-chain/client";

const client = new AetherClient({ rpc: "https://rpc.157-245-252-221.sslip.io", chainId: "aether-testnet-1" });

// 1. A key. Save the phrase: Key.fromMnemonic(phrase) gives the same key back.
const { key, mnemonic } = Key.generate();
console.log("address", key.address);

// 2. Test funds. "sent" means the drip is in a block; "pending" means wait for it.
const drip = await fetch("https://faucet.157-245-252-221.sslip.io/request", {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ address: key.address }),
}).then((r) => r.json());
console.log("faucet", drip.code, drip.tx_hash);
if (drip.code === "pending") await client.waitForTransaction(drip.tx_hash);

// 3. Balance, in uaeth.
console.log("balance", await client.balance(key.address));

// 4. A payment, then wait until it's in a block.
const sent = await client.send(key, "aether1cdugwhxk9cktjsemm6yjrd6xtfsq9wkjvnef03ml4u6ltuv7edcs0eyjds", "0.001 AETH", { memo: "first payment" });
const done = await client.waitForTransaction(sent.hash);
console.log(done.status, "https://explorer.157-245-252-221.sslip.io/tx/" + sent.hash);
```

## 3. Python (3.10+)

```bash
pip install "git+https://github.com/whoyoujoshin/aether#subdirectory=clients/python"
```

```python
# first.py: python first.py
import json, urllib.request
from aether_client import AetherClient, Key

client = AetherClient("https://rpc.157-245-252-221.sslip.io", "aether-testnet-1")

# 1. A key. Save the phrase: Key.from_mnemonic(phrase) gives the same key back.
key, phrase = Key.generate()
print("address", key.address)

# 2. Test funds. "sent" means the drip is in a block; "pending" means wait for it.
req = urllib.request.Request("https://faucet.157-245-252-221.sslip.io/request", data=json.dumps({"address": key.address}).encode(),
                             headers={"Content-Type": "application/json"})
drip = json.load(urllib.request.urlopen(req))
print("faucet", drip["code"], drip.get("tx_hash"))
if drip["code"] == "pending":
    client.wait_for_transaction(drip["tx_hash"])

# 3. Balance, in uaeth.
print("balance", client.balance(key.address))

# 4. A payment, then wait until it's in a block.
sent = client.send(key, "aether1cdugwhxk9cktjsemm6yjrd6xtfsq9wkjvnef03ml4u6ltuv7edcs0eyjds", "0.001 AETH", memo="first payment")
done = client.wait_for_transaction(sent.hash)
print(done.status, "https://explorer.157-245-252-221.sslip.io/tx/" + sent.hash)
```

## Plain HTTP

Reading and getting funds need nothing but HTTP:

```bash
curl -s "https://explorer.157-245-252-221.sslip.io/api/address?addr=aether1..."              # balance and recent transactions
curl -s -X POST https://faucet.157-245-252-221.sslip.io/request -H 'Content-Type: application/json' -d '{"address":"aether1..."}'
```

Sending is signing: a payment has to be signed with the account's key
where the key is. There's no endpoint that sends for you, because it would
need your key. The clients above sign locally and broadcast to the RPC.

## When something fails

- The faucet answers with a stable `code`: `sent`, `pending` (broadcast, not yet in a block),
  `address_cooldown` or `caller_limit` (both with `retry_after_seconds` and `Retry-After`),
  `invalid_address`, `send_failed` (safe to retry).
- Retrying a payment: re-broadcast the same signed bytes
  (`client.rebroadcast(sent.signed)`), never a second `send`; the chain includes
  them at most once.
- `waitForTransaction` / `wait_for_transaction` returns `pending` if it times out: look the hash
  up again later, don't send again.

## Spending limits without the main key

- `agentmcp` caps every payment and each rolling day (`--per-tx-limit`,
  `--daily-limit`), and can hold back bigger payments for the owner to approve.
- With `--granter <your address>` it pays from your account under an x/authz
  grant: the chain enforces the limit, expiry and allowed recipients, and
  you can revoke it any time. The agent's own key holds nothing.
- Session keys with spend limits and guardians: see
  [account abstraction](https://github.com/whoyoujoshin/aether/blob/main/docs/ACCOUNT_ABSTRACTION.md).

## Later, if you want them

Getting paid by memo, buying from paid APIs (HTTP 402), escrow, the
service directory: [clients](https://github.com/whoyoujoshin/aether/tree/main/clients)
and [the MCP wallet](https://github.com/whoyoujoshin/aether#ai-agent-wallet-mcp).
Mining and validating are open to anyone but never needed to pay:
[validators](https://github.com/whoyoujoshin/aether#registering-as-a-validator).

## What stays stable

The URLs and fields on this page, `/llms.txt`, `/api/agents` and
`/api/openapi.json` only ever gain things; see
[API stability](https://github.com/whoyoujoshin/aether/blob/main/docs/API-STABILITY.md).
