import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { base64 } from "@scure/base";
import { bytesToHex } from "@noble/hashes/utils.js";
import { readFields, first } from "../src/proto.js";
import { AetherClient, ExactAetherScheme, Key, buildSend, x402Network } from "../src/index.js";

// The exact scheme signs what the Go facilitator accepts: the same bytes
// as the Go wallet's send (vectors.json), which x402/exact_test.go runs
// through the facilitator.
const v = JSON.parse(readFileSync(new URL("../../../testdata/vectors.json", import.meta.url), "utf8"));
const key = Key.fromMnemonic(v.key.mnemonic);
const payTo = v.txs[0].to;

function client(account?: { accountNumber: bigint; sequence: bigint }, usdcPath?: string): AetherClient {
  const c = new AetherClient({ rpc: "http://127.0.0.1:1", chainId: "aether-testnet-1", usdcPath, usdcBaseDenom: usdcPath ? "uusdc" : undefined });
  c.accountInfo = async () => account;
  return c;
}

const req = { scheme: "exact", network: "cosmos:aether-testnet-1", asset: "uaeth", amount: "1500000", payTo, maxTimeoutSeconds: 60, extra: {} };

test("it pays exactly the requirement with a send from the key's account", async () => {
  const scheme = new ExactAetherScheme(client({ accountNumber: 7n, sequence: 3n }), key);
  assert.equal(scheme.scheme, "exact");
  assert.equal(x402Network("aether-testnet-1"), "cosmos:aether-testnet-1");
  const r = await scheme.createPaymentPayload(2, req);
  assert.equal(r.x402Version, 2);
  const txBytes = base64.decode(r.payload.transaction as string);
  const raw = readFields(txBytes);
  const want = buildSend(key, { chainId: "aether-testnet-1", accountNumber: 7n, sequence: 3n, to: payTo, amountUaeth: 1500000n });
  assert.equal(bytesToHex(first(raw, 1)!.bytes!), bytesToHex(want.bodyBytes), "one send of the amount to payTo");
  assert.equal(bytesToHex(first(raw, 2)!.bytes!), bytesToHex(want.authInfoBytes), "the account's key, its next sequence, default gas");
  assert.ok(Key.verify(key.publicKey, want.signDoc, first(raw, 3)!.bytes!), "signed for account 7 on aether-testnet-1");
});

test("it refuses what it can't or shouldn't pay", async () => {
  const scheme = new ExactAetherScheme(client({ accountNumber: 7n, sequence: 3n }), key, { maxAmount: { uaeth: 1000000n } });
  const bad: [string, object, number?][] = [
    ["v1", req, 1],
    ["another scheme", { ...req, scheme: "upto" }],
    ["another network", { ...req, network: "eip155:8453" }],
    ["another chain", { ...req, network: "cosmos:osmo-test-5" }],
    ["zero", { ...req, amount: "0" }],
    ["decimal", { ...req, amount: "1.5" }],
    ["bad payTo", { ...req, payTo: "0x0000000000000000000000000000000000000000" }],
    ["over the client's cap", { ...req, amount: "1000001" }],
  ];
  for (const [name, r, version] of bad) {
    await assert.rejects(scheme.createPaymentPayload(version ?? 2, r as typeof req), name);
  }
  await assert.rejects(scheme.createPaymentPayload(2, { ...req, amount: "500" }, { maxAmountPerPayment: "499" }), /spend limit/);
  await assert.rejects(new ExactAetherScheme(client(undefined), key).createPaymentPayload(2, req), /fund it first/);
});

test("it names USDC for x402Client's USD spend caps", () => {
  const c = client(undefined, "transfer/channel-2");
  const usdc = c.assets.bySymbol("USDC")!;
  const scheme = new ExactAetherScheme(c, key);
  assert.deepEqual(scheme.findDefaultAsset(usdc.denom, "cosmos:aether-testnet-1"), { asset: usdc.denom, decimals: 6, symbol: "USDC" });
  assert.equal(scheme.findDefaultAsset("uaeth", "cosmos:aether-testnet-1"), undefined);
  assert.equal(scheme.findDefaultAsset(usdc.denom, "cosmos:other-1"), undefined);
});
