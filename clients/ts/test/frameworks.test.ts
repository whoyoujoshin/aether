import { test } from "node:test";
import assert from "node:assert/strict";
import { AETH, AetherClient, AetherToolkit, Key } from "../src/index.js";
import type { SendResult } from "../src/index.js";
import { aetherActionProvider } from "../src/agentkit.js";
import { aether } from "../src/goat.js";

function setup() {
  const client = new AetherClient({ rpc: "http://127.0.0.1:1" });
  const sent: [string, string, string][] = [];
  client.balances = async () => [{ asset: AETH, amount: 1_000_000n }];
  client.send = async (_k, to, amount, o = {}) => {
    sent.push([to, amount, o.memo ?? ""]);
    return { hash: `TX${sent.length}`, status: "pending", code: 0, log: "" } as SendResult;
  };
  const kit = new AetherToolkit(client, Key.random(), { maxPerPayment: "0.5 AETH", dailyBudget: "1 AETH", faucetUrl: null });
  return { kit, sent, to: Key.random().address };
}

test("AgentKit: actions with zod schemas that answer JSON text and keep the limits", async () => {
  const { kit, sent, to } = setup();
  const provider = aetherActionProvider(kit);
  assert.equal(provider.supportsNetwork(), true);
  const actions = Object.fromEntries(provider.getActions().map((a) => [a.name, a]));
  assert.equal(Object.keys(actions).length, 6);
  const send = actions.aether_send_payment;
  assert.equal(send.schema.safeParse({ to, amount: "0.1 AETH" }).success, false, "idempotency_key is required");
  assert.equal(send.schema.safeParse({ to, amount: "0.1 AETH", idempotency_key: "a", memo: null }).success, true);
  assert.equal(send.schema.safeParse({ to, amount: "0.1 AETH", idempotency_key: "a", extra: "x" }).success, false);
  const out = JSON.parse(await send.invoke({ to, amount: "0.1 AETH", idempotency_key: "a", memo: null }));
  assert.equal(out.status, "pending");
  assert.deepEqual(sent, [[to, "0.1 AETH", ""]]);
  assert.equal(JSON.parse(await send.invoke({ to, amount: "0.6 AETH", idempotency_key: "b" })).error.code, "PER_PAYMENT_LIMIT");
});

test("GOAT: a plugin whose tools return the toolkit's results", async () => {
  const { kit, to } = setup();
  const plugin = aether(kit);
  assert.equal(plugin.supportsChain(), true);
  const tools = Object.fromEntries(plugin.getTools().map((t) => [t.name, t]));
  assert.match(tools.aether_find_paid_services.description, /untrusted/);
  assert.equal((await tools.aether_get_balance.execute({})).address, kit.key!.address);
  assert.equal((await tools.aether_send_payment.execute({ to, amount: "0.1 AETH", idempotency_key: "g" })).status, "pending");
});

// The real frameworks are big, so they aren't dev dependencies: these run
// where they're installed.
const optional = async (name: string) => {
  try {
    return await import(name);
  } catch {
    return undefined;
  }
};

test("AgentKit itself takes the provider", async (t) => {
  const ak = await optional("@coinbase/agentkit");
  if (!ak) return t.skip("@coinbase/agentkit not installed");
  const { kit } = setup();
  const walletProvider = { getNetwork: () => ({ protocolFamily: "evm", networkId: "base-sepolia" }) };
  const agentkit = await ak.AgentKit.from({ walletProvider, actionProviders: [aetherActionProvider(kit)] });
  const names = agentkit.getActions().map((a: { name: string }) => a.name);
  assert.ok(names.includes("aether_call_paid_api"));
});

test("GOAT's getTools takes the plugin", async (t) => {
  const goat = await optional("@goat-sdk/core");
  if (!goat) return t.skip("@goat-sdk/core not installed");
  const { kit } = setup();
  const wallet = { getChain: () => ({ type: "evm", id: 84532 }), getCoreTools: () => [] };
  const tools = await goat.getTools({ wallet, plugins: [aether(kit)] });
  assert.equal(tools.length, 6);
});
