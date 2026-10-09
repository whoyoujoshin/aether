# aether-chain-client

```bash
npm install aether-chain-client
```

TypeScript client for the [Aether](https://github.com/whoyoujoshin/aether) chain: ML-DSA-44 keys, sending and receiving AETH, buying from and selling paid APIs (x402 `aether-memo`, `aether-prepaid` and `aether-pull`) and the on-chain service directory. It talks to a node's CometBFT RPC (port 26657) only.

```ts
import { AetherClient, Key, fetchPaid, findServices, rateService, withdrawPrepaid } from "aether-chain-client";

const client = new AetherClient({ rpc: "http://localhost:26657", chainId: "aether-testnet-1" });
const key = Key.fromMnemonic(process.env.AETHER_MNEMONIC!); // same address as `aetherd keys add` / `agentmcp`

const sent = await client.send(key, "aether1...", "0.5 AETH", { memo: "invoice-42" });
const done = await client.waitForTransaction(sent.hash);         // confirmed | failed | pending (timeout)
// Retrying after an error: client.rebroadcast(sent.signed) -- never a second send().

const res = await fetchPaid(client, key, "https://api.example.com/forecast", { maxAmount: "0.05 AETH", prepay: "1 AETH" });
// Or, where offered, pay from your own account under a capped, 7-day allowance the seller collects from later:
const res2 = await fetchPaid(client, key, "https://api.example.com/forecast", { maxAmount: "0.05 AETH", pullAllowance: "1 AETH" });
const services = await findServices(client, { query: "weather", maxPrice: "0.1 AETH", trusted: [ownerAddress] });
// services[i].reputation: payments, payers, ratings (can be faked), trustedRatings (can't)
// res.receipt: the seller's signed receipt and whether it matches exactly what was sent and received
await rateService(client, key, "https://api.example.com", 5); // only raters who paid a service count
const back = await withdrawPrepaid(client, key, "https://api.example.com", { withdrawalId: "w1" }); // unspent prepaid balance
```

## AI agents

`AetherToolkit` turns a client and key into six tools for a model: balance,
transaction status, the service directory, a payment, a paid API call and the
testnet faucet. Spending is capped in code (per payment and per rolling 24 hours,
checked before anything is signed), a send's `idempotency_key` never pays twice,
and a failure comes back as `{error: {code, message}}` for the model to read.

```ts
import { AetherClient, AetherToolkit, Key } from "aether-chain-client";

const kit = new AetherToolkit(new AetherClient({ rpc: "https://rpc.157-245-252-221.sslip.io" }),
  Key.fromMnemonic(process.env.AETHER_MNEMONIC!), { maxPerPayment: "0.1 AETH", dailyBudget: "1 AETH" });
const tools = kit.toolSpecs();             // OpenAI-style function tools; "anthropic" for Messages API tools
const result = await kit.call(name, args); // answer the model's tool call
```

Without a key it offers only the read-only tools. The same tools, in other frameworks:

```ts
// Coinbase AgentKit: an action provider beside the others
import { aetherActionProvider } from "aether-chain-client/agentkit";
const agentkit = await AgentKit.from({ walletProvider, actionProviders: [aetherActionProvider(kit)] });

// GOAT SDK: a plugin beside the others
import { aether } from "aether-chain-client/goat";
const tools = await getOnChainTools({ wallet, plugins: [aether(kit)] });
```

Both need `zod` (AgentKit and GOAT already depend on it) and work whatever
network or chain the framework's own wallet is on: they pay from the Aether key
in the toolkit, within its limits. Checked with AgentKit 0.10 (and its LangChain
adapter) and GOAT 0.5 (and its Vercel AI adapter). For ElizaOS, use
[`elizaos-plugin-aether`](../eliza).

## Selling

Charge per request from a Node service -- all three payment schemes, the `/.well-known/x402` manifest for the service directory, and withdrawals of unspent prepaid balances. Compatible with the Go paywall and every Aether buyer.

```ts
import express from "express";
import { AetherClient, Key, Paywall } from "aether-chain-client";

const client = new AetherClient({ rpc: "http://localhost:26657", chainId: "aether-testnet-1" });
const pw = new Paywall({
  client, payTo: "aether1...", price: "0.01 AETH", name: "Weather", description: "Forecasts by city",
  prepaid: {
    ledger: "ledger.json",          // customers' balances: back it up
    minDeposit: "0.1 AETH",
    payoutKey: Key.fromMnemonic(process.env.PAYOUT_MNEMONIC!), // pays withdrawals; keep a small float in it
  },
  receipts: { key: payeeKey }, // sign a receipt for every paid response (or a delegated key + createReceiptDelegation)
  pull: { collectorKey: Key.fromMnemonic(process.env.COLLECTOR_MNEMONIC!), credit: "1 AETH" }, // aether-pull: needs no funds
});
pw.startCollecting(); // collects what aether-pull buyers owe, in batches
const app = express();
app.use(pw.middleware({ free: ["/health"] })); // before body parsers
app.post("/forecast", express.json(), (req, res) => res.json({ city: req.body.city, paidBy: (req as any).aether.payer }));
```

A signed (prepaid) request's body is read by the middleware to check its signature, then left on `req.rawBody` and `req.body`. For other frameworks, `pw.handle(request, body)` returns what to do. The ledger is one JSON file (the Go paywall's format) for one process.

Amounts always carry a unit (`"1.5 AETH"`, `"1500000uaeth"`).

**USDC.** `new AetherClient({ rpc, usdcChannel: "channel-N" })` (Aether's end of its channel to Noble) also knows USDC: Noble's `uusdc` over exactly that channel, never a lookalike that arrived another way. For USDC that arrives another route, give its denom trace and its denom on the issuing chain instead: `usdcPath: "transfer/channel-2", usdcBaseDenom: "erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d", usdcIssuer: "Injective"` (`usdcBaseDenom` defaults to `uusdc`). Then `send(key, to, "5 USDC")` sends USDC, `balances(address)` lists each asset, `waitForPayment` matches in the asset of `minAmount`, and `fetchPaid` pays services priced in USDC with `maxAmount` (and `prepay` / `pullAllowance`) in USDC; an amount in another asset than the service charges is refused with `ASSET_MISMATCH` before anything is paid. Results carry `asset`, `amount`, `balance`, `owed` and `allowance` in that asset's base units; the `*Uaeth` fields are set only for AETH. Without the channel, `"5 USDC"` is an unknown unit. The seller kit charges USDC too: `new Paywall({ client, payTo, price: "0.05 USDC", usdcChannel: "channel-N" })` charges Noble USDC for everything (payments, deposits and balances, pull allowances and collections, withdrawals), `minDeposit` and `pull.credit` must then be in USDC, and 402s, the manifest and receipts carry the asset exactly as `cmd/paywall`'s do.

Tests: `npm test` (checked against `../testdata/vectors.json`, generated by the Go code).
