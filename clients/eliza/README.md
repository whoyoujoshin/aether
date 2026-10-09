# elizaos-plugin-aether

An [ElizaOS](https://github.com/elizaOS/eliza) plugin that gives an agent an
[Aether](https://github.com/whoyoujoshin/aether) wallet: it can pay for APIs that
charge per request (HTTP 402, x402), send AETH and USDC, check balances and
transactions, and find paid services in Aether's on-chain directory, within
spending limits enforced in code.

```bash
npm install elizaos-plugin-aether
```

```ts
import aetherPlugin from "elizaos-plugin-aether";

export const character = {
  name: "Ada",
  plugins: ["@elizaos/plugin-bootstrap", "elizaos-plugin-aether"], // or plugins: [aetherPlugin] in code
  settings: {
    secrets: { AETHER_MNEMONIC: process.env.AETHER_MNEMONIC },
    AETHER_MAX_PER_PAYMENT: "0.1 AETH",
    AETHER_DAILY_BUDGET: "1 AETH",
  },
};
```

New to Aether? Make the agent an account (once) and keep its phrase as `AETHER_MNEMONIC`:

```bash
node -e 'import("aether-chain-client").then(({Key})=>{const {key,mnemonic}=Key.generate();console.log(key.address+"\n"+mnemonic)})'
```

Then ask the agent to get itself some testnet AETH: it asks the faucet.

## Actions

| Action | What it does | Spends |
| --- | --- | --- |
| `AETHER_GET_BALANCE` | Balance of an address in every known asset (the agent's own if none is given) | no |
| `AETHER_TRANSACTION_STATUS` | Whether a transaction is pending, confirmed or failed | no |
| `AETHER_FIND_PAID_SERVICES` | Paid APIs in the on-chain directory, by words and maximum price | no |
| `AETHER_SEND_PAYMENT` | Sends AETH (or USDC) to an address | yes |
| `AETHER_CALL_PAID_API` | Calls an API, pays what it asks up to a maximum, returns the answer | yes |
| `AETHER_REQUEST_TESTNET_FUNDS` | Asks the testnet faucet for starter AETH | no |

The `AETHER_WALLET` provider tells the agent its address and what's left of its budget.

## Settings

| Setting | Default | |
| --- | --- | --- |
| `AETHER_MNEMONIC` | none | The agent account's 24-word recovery phrase. A secret. Without it the plugin is read-only. |
| `AETHER_MAX_PER_PAYMENT` | `1 AETH` | Most one payment may be, with its unit. |
| `AETHER_DAILY_BUDGET` | `5 AETH` | Most it may spend in a rolling 24 hours, in the same asset. |
| `AETHER_RPC_URL` | `https://rpc.157-245-252-221.sslip.io` | The public testnet. |
| `AETHER_CHAIN_ID` | `aether-testnet-1` | |
| `AETHER_FAUCET_URL` | the public testnet faucet | `none` turns it off. |
| `AETHER_USDC_PATH`, `AETHER_USDC_BASE_DENOM`, `AETHER_USDC_ISSUER` | unset (AETH only) | The USDC to know. Testnet USDC from Injective: `transfer/channel-2`, `erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d`, `Injective`. To spend USDC, give the limits in USDC. |

## Safety

- **Limits hold in code.** A payment over `AETHER_MAX_PER_PAYMENT`, or past
  `AETHER_DAILY_BUDGET` in 24 hours, is refused before anything is signed, whatever
  the conversation says. Anyone who can talk to the agent can ask it to spend
  within them: set them to what you're prepared to lose, and keep only that much
  in the account.
- **One message pays at most once.** A send's idempotency key comes from the
  message, not the model, so a retried handler doesn't pay twice.
- **Untrusted text stays data.** Memos, service names and descriptions and API
  answers are written by other people; the model is told not to take
  instructions or payment details from them.
- Use a dedicated account for the agent, never one holding anything else.

The tools themselves are `AetherToolkit` in [`aether-chain-client`](../ts), which
also gives them as plain JSON tool specs for any other framework.
