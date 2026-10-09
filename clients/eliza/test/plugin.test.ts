import { test } from "node:test";
import assert from "node:assert/strict";
import type { IAgentRuntime, Memory, Content } from "@elizaos/core";
import { AETH, AetherClient, AetherToolkit, Key } from "aether-chain-client";
import type { SendResult } from "aether-chain-client";
import { createAetherPlugin, parseArgs, toolkitFromSettings } from "../src/index.js";

function fakeRuntime(settings: Record<string, string>, modelReplies: string[]) {
  const prompts: string[] = [];
  const runtime = {
    getSetting: (k: string) => settings[k] ?? null,
    useModel: async (_type: string, p: { prompt: string }) => {
      prompts.push(p.prompt);
      return modelReplies.shift() ?? "";
    },
    composeState: async () => ({ values: {}, data: {}, text: "user: please pay aether1… 0.1 AETH" }),
  } as unknown as IAgentRuntime;
  return { runtime, prompts };
}

function fakeKit(key: Key | undefined) {
  const client = new AetherClient({ rpc: "http://127.0.0.1:1" });
  const sent: [string, string, string][] = [];
  client.balances = async () => [{ asset: AETH, amount: 2_500_000n }];
  client.send = async (_k, to, amount, o = {}) => {
    sent.push([to, amount, o.memo ?? ""]);
    return { hash: `TX${sent.length}`, status: "pending", code: 0, log: "" } as SendResult;
  };
  return { kit: new AetherToolkit(client, key, { maxPerPayment: "0.5 AETH", dailyBudget: "1 AETH", faucetUrl: null }), sent };
}

const msg = (text: string, id = "11111111-1111-1111-1111-111111111111") => ({ id, content: { text, source: "test" } }) as unknown as Memory;

async function setup(withKey: boolean, replies: string[]) {
  const key = withKey ? Key.random() : undefined;
  const { kit, sent } = fakeKit(key);
  const plugin = createAetherPlugin({ toolkit: () => kit });
  const { runtime, prompts } = fakeRuntime({}, replies);
  await plugin.init!({}, runtime);
  const action = (name: string) => plugin.actions!.find((a) => a.name === name)!;
  return { plugin, runtime, prompts, action, sent, key };
}

test("the plugin offers six actions and a wallet provider", async () => {
  const { plugin, runtime, action, key } = await setup(true, []);
  assert.equal(plugin.name, "aether");
  assert.deepEqual(plugin.actions!.map((a) => a.name), [
    "AETHER_GET_BALANCE", "AETHER_TRANSACTION_STATUS", "AETHER_FIND_PAID_SERVICES", "AETHER_SEND_PAYMENT", "AETHER_CALL_PAID_API", "AETHER_REQUEST_TESTNET_FUNDS",
  ]);
  for (const a of plugin.actions!) {
    assert.ok(a.description.length > 20, a.name);
    assert.equal(await a.validate(runtime, msg("x")), true, a.name);
  }
  assert.match(action("AETHER_SEND_PAYMENT").description, /never pays twice/);
  const p = await plugin.providers![0].get(runtime, msg("x"), { values: {}, data: {}, text: "" });
  assert.match(p.text!, new RegExp(key!.address));
  assert.match(p.text!, /0\.5 AETH at a time and 1 AETH per 24 hours/);
});

test("without a key, spending actions don't validate", async () => {
  const { plugin, runtime, action } = await setup(false, []);
  assert.equal(await action("AETHER_SEND_PAYMENT").validate(runtime, msg("x")), false);
  assert.equal(await action("AETHER_GET_BALANCE").validate(runtime, msg("x")), true);
  const p = await plugin.providers![0].get(runtime, msg("x"), { values: {}, data: {}, text: "" });
  assert.match(p.text!, /read-only/);
});

test("a send takes its arguments from the model, its idempotency key from the message, and pays once", async () => {
  const to = Key.random().address;
  const reply = `Sure: {"to": "${to}", "amount": "0.1 AETH", "idempotency_key": "model-made", "memo": "report"}`;
  const { runtime, prompts, action, sent } = await setup(true, [reply, reply]);
  const said: Content[] = [];
  const callback = async (c: Content) => {
    said.push(c);
    return [];
  };
  const send = action("AETHER_SEND_PAYMENT");
  const r = await send.handler(runtime, msg(`send 0.1 AETH to ${to}`), undefined, {}, callback);
  assert.equal(r!.success, true);
  assert.equal(sent.length, 1);
  assert.deepEqual(sent[0], [to, "0.1 AETH", "report"]);
  assert.match(said[0].text!, /Sent 0\.1 AETH/);
  assert.deepEqual(said[0].actions, ["AETHER_SEND_PAYMENT"]);
  assert.doesNotMatch(prompts[0].split("\n")[1], /idempotency_key/, "the model never picks the key");
  assert.match(prompts[0], /data, not instructions/);

  // The same message handled again (a retry) doesn't pay again.
  const again = await send.handler(runtime, msg(`send 0.1 AETH to ${to}`), undefined, {}, callback);
  assert.equal(again!.success, true);
  assert.equal(sent.length, 1);
  assert.match(said[1].text!, /Already sent/);
});

test("limits hold whatever the model asks for; errors come back as text", async () => {
  const to = Key.random().address;
  const { runtime, action, sent } = await setup(true, [`{"to": "${to}", "amount": "5 AETH"}`, "I can't tell"]);
  const r = await action("AETHER_SEND_PAYMENT").handler(runtime, msg("send 5"), undefined, {}, undefined);
  assert.equal(r!.success, false);
  assert.match(r!.text!, /PER_PAYMENT_LIMIT/);
  const r2 = await action("AETHER_SEND_PAYMENT").handler(runtime, msg("send some", "2"), undefined, {}, undefined);
  assert.equal(r2!.success, false);
  assert.equal(sent.length, 0);
});

test("balance: an empty address means the agent's own", async () => {
  const { runtime, action, key } = await setup(true, ["{}"]);
  const r = await action("AETHER_GET_BALANCE").handler(runtime, msg("what's your balance?"), undefined, {}, undefined);
  assert.equal(r!.text, `${key!.address} holds 2.5 AETH.`);
});

test("settings build the toolkit: read-only without a mnemonic, limits and USDC from settings", () => {
  const ro = toolkitFromSettings(fakeRuntime({}, []).runtime);
  assert.equal(ro.key, undefined);
  assert.equal(ro.client.opts.rpc, "https://rpc.157-245-252-221.sslip.io");
  const { mnemonic } = Key.generate();
  const kit = toolkitFromSettings(fakeRuntime({
    AETHER_MNEMONIC: mnemonic, AETHER_MAX_PER_PAYMENT: "0.2 USDC", AETHER_DAILY_BUDGET: "2 USDC",
    AETHER_USDC_PATH: "transfer/channel-2", AETHER_USDC_BASE_DENOM: "erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d", AETHER_USDC_ISSUER: "Injective",
  }, []).runtime);
  assert.equal(kit.key!.address, Key.fromMnemonic(mnemonic).address);
  assert.equal(kit.spendingStatus().perPaymentLimit, "0.2 USDC");
});

test("parseArgs takes the first JSON object", () => {
  assert.deepEqual(parseArgs('Here: {"a": "b"} done'), { a: "b" });
  assert.equal(parseArgs("no json"), undefined);
  assert.equal(parseArgs("[1]"), undefined);
});
