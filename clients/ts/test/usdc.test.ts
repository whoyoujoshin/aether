import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createServer } from "node:http";
import { base64 } from "@scure/base";
import { sha256 } from "@noble/hashes/sha2.js";
import { bytesToHex } from "@noble/hashes/utils.js";
import { Writer, readFields, first, text } from "../src/proto.js";
import {
  AETH, AetherClient, Assets, Key, fetchPaid, formatAmount, receiptAmount, usdc, MSG_GRANT_TYPE_URL, MSG_SEND_TYPE_URL, PaymentError,
} from "../src/index.js";

const v = JSON.parse(readFileSync(new URL("../../../testdata/vectors.json", import.meta.url), "utf8"));
const CHAIN = "aether-testnet-1";
const USDC = usdc("channel-3");

test("USDC: the denom and receipt amount match Go's", () => {
  assert.equal(USDC.denom, v.usdc.denom);
  assert.equal(receiptAmount(50_000n, USDC.denom), v.usdc.receiptAmount);
  assert.equal(receiptAmount(50_000n, AETH.denom), "50000", "AETH receipts read as they always did");
  assert.throws(() => usdc("3"), /channel-3/);
});

test("USDC: amounts name their asset, and USDC is unknown until a channel is given", () => {
  const off = new Assets();
  assert.throws(() => off.parse("5 USDC"), /unknown unit "USDC"/);
  assert.throws(() => off.parse("5"), /no unit/);
  const on = new Assets({ usdcChannel: "channel-3" });
  assert.deepEqual(on.parse("2.25 usdc"), { asset: USDC, amount: 2_250_000n });
  assert.deepEqual(on.parse("5000000uusdc"), { asset: USDC, amount: 5_000_000n });
  assert.deepEqual(on.parse("1.5 AETH"), { asset: AETH, amount: 1_500_000n });
  assert.throws(() => on.parse("0.0000001 USDC"), /decimal places/);
  assert.equal(formatAmount(USDC, 50_000n), "0.05 USDC");
  assert.equal(on.byDenom(USDC.denom)?.symbol, "USDC");
});

/** A node that puts every transaction straight into a block, recording the coins each message moves. */
class Chain {
  txs = new Map<string, unknown>();
  msgs: { typeUrl: string; denom: string; amount: bigint }[] = [];
  seq = 0n;

  fetch = (async (_url: unknown, init?: { body?: unknown }) => {
    const { method, params } = JSON.parse(String(init?.body)) as { method: string; params: Record<string, string> };
    const ok = (result: unknown) => new Response(JSON.stringify({ jsonrpc: "2.0", id: 1, result }));
    if (method === "tx") {
      const t = this.txs.get(Buffer.from(params.hash, "base64").toString("hex").toUpperCase());
      return t ? ok(t) : new Response(JSON.stringify({ jsonrpc: "2.0", id: 1, error: { code: -32603, message: "Internal error", data: "not found" } }));
    }
    if (method === "abci_query" && params.path === "/cosmos.auth.v1beta1.Query/AccountInfo") {
      const info = new Writer().uint64(3, 9n).uint64(4, this.seq).finish();
      return ok({ response: { code: 0, log: "", value: base64.encode(new Writer().message(1, info).finish()) } });
    }
    if (method === "broadcast_tx_sync") {
      const tx = base64.decode(params.tx);
      const hash = bytesToHex(sha256(tx)).toUpperCase();
      for (const m of readFields(first(readFields(tx), 1)!.bytes!).filter((x) => x.field === 1)) {
        const any = readFields(m.bytes!);
        const typeUrl = text(first(any, 1)?.bytes);
        const f = readFields(first(any, 2)!.bytes!);
        // MsgSend: coin at field 3. MsgGrant: grant(3) > authorization(1) > value(2) > spend limit coin(1).
        const coin = typeUrl === MSG_SEND_TYPE_URL
          ? first(f, 3)!.bytes!
          : first(readFields(first(readFields(first(readFields(first(f, 3)!.bytes!), 1)!.bytes!), 2)!.bytes!), 1)!.bytes!;
        const c = readFields(coin);
        this.msgs.push({ typeUrl, denom: text(first(c, 1)?.bytes), amount: BigInt(text(first(c, 2)?.bytes)) });
      }
      this.seq++;
      this.txs.set(hash, { hash, height: "9", tx: params.tx, tx_result: { code: 0, log: "", events: [] } });
      return ok({ code: 0, codespace: "", log: "", hash });
    }
    throw new Error("unexpected " + method + " " + (params.path ?? ""));
  }) as unknown as typeof fetch;
}

/**
 * A service charging 0.05 USDC per request, by memo or pull. It takes any
 * memo proof, and a pull request once any allowance is on chain: the buyer
 * is what's tested.
 */
async function usdcService(payTo: string, chain: Chain) {
  const grantee = Key.random().address;
  const srv = createServer((req, res) => {
    const payment = req.headers["x-payment"];
    const scheme = payment ? (JSON.parse(new TextDecoder().decode(base64.decode(String(payment)))) as { scheme: string }).scheme : "";
    const granted = chain.msgs.some((m) => m.typeUrl === MSG_GRANT_TYPE_URL);
    if (scheme === "aether-memo" || (scheme === "aether-pull" && granted)) return res.end("paid in dollars");
    const offer = (scheme: string, extra: Record<string, string>) => ({
      scheme, network: CHAIN, maxAmountRequired: "50000", asset: USDC.denom, payTo, resource: "", description: "", maxTimeoutSeconds: 300,
      extra: { symbol: "USDC", amount: "0.05", ...extra },
    });
    res.statusCode = 402;
    res.end(JSON.stringify({ x402Version: 1, error: scheme === "aether-pull" ? "no_grant" : "payment_required", accepts: [offer("aether-memo", { invoice: "inv-1" }), offer("aether-pull", { grantee, owed: "0" })] }));
  });
  await new Promise<void>((r) => srv.listen(0, "127.0.0.1", r));
  return { url: `http://127.0.0.1:${(srv.address() as { port: number }).port}/q`, grantee, close: () => srv.close() };
}

test("USDC: send moves USDC, never uaeth", async () => {
  const chain = new Chain();
  const client = new AetherClient({ rpc: "http://node", chainId: CHAIN, fetch: chain.fetch, usdcChannel: "channel-3" });
  await client.send(Key.random(), Key.random().address, "2 USDC");
  await client.send(Key.random(), Key.random().address, "0.5 AETH");
  assert.deepEqual(chain.msgs.map((m) => [m.denom, m.amount]), [[USDC.denom, 2_000_000n], ["uaeth", 500_000n]]);
});

test("USDC: fetchPaid pays a USDC price in USDC, and refuses a maxAmount in another asset unpaid", async () => {
  const chain = new Chain();
  const client = new AetherClient({ rpc: "http://node", chainId: CHAIN, fetch: chain.fetch, usdcChannel: "channel-3" });
  const svc = await usdcService(Key.random().address, chain);
  try {
    const key = Key.random();
    await assert.rejects(fetchPaid(client, key, svc.url, { maxAmount: "1 AETH" }), (e: PaymentError) => e.code === "ASSET_MISMATCH" && /0\.05 USDC/.test(e.message));
    await assert.rejects(fetchPaid(client, key, svc.url, { maxAmount: "0.04 USDC" }), (e: PaymentError) => e.code === "PRICE_EXCEEDS_MAX");
    const blind = new AetherClient({ rpc: "http://node", chainId: CHAIN, fetch: chain.fetch });
    await assert.rejects(fetchPaid(blind, key, svc.url, { maxAmount: "1 AETH" }), (e: PaymentError) => e.code === "PAYMENT_UNSUPPORTED" && e.message.includes(USDC.denom));
    assert.equal(chain.msgs.length, 0, "nothing was paid");

    const r = await fetchPaid(client, key, svc.url, { maxAmount: "0.10 USDC", confirmTimeoutMs: 1000 });
    assert.equal(r.status, "paid");
    assert.equal(await r.response!.text(), "paid in dollars");
    assert.equal(r.asset?.symbol, "USDC");
    assert.equal(r.amount, 50_000n);
    assert.equal(r.amountUaeth, undefined, "a USDC amount never shows up as uaeth");
    assert.deepEqual(chain.msgs, [{ typeUrl: MSG_SEND_TYPE_URL, denom: USDC.denom, amount: 50_000n }]);
  } finally {
    svc.close();
  }
});

test("USDC: a pull allowance for a USDC service is a limit in USDC only", async () => {
  const chain = new Chain();
  const client = new AetherClient({ rpc: "http://node", chainId: CHAIN, fetch: chain.fetch, usdcChannel: "channel-3" });
  const svc = await usdcService(Key.random().address, chain);
  try {
    const key = Key.random();
    await assert.rejects(fetchPaid(client, key, svc.url, { maxAmount: "0.10 USDC", pullAllowance: "1 AETH" }), (e: PaymentError) => e.code === "ASSET_MISMATCH");
    assert.equal(chain.msgs.length, 0);
    const r = await fetchPaid(client, key, svc.url, { maxAmount: "0.10 USDC", pullAllowance: "1 USDC", confirmTimeoutMs: 1000 });
    assert.equal(r.status, "paid");
    assert.equal(r.scheme, "aether-pull");
    assert.equal(r.asset?.symbol, "USDC");
    assert.ok(r.grantTxHash);
    assert.deepEqual(chain.msgs, [{ typeUrl: MSG_GRANT_TYPE_URL, denom: USDC.denom, amount: 1_000_000n }], "one allowance, in USDC: the seller can't collect AETH under it");

    await fetchPaid(client, key, svc.url, { maxAmount: "0.10 USDC", pullAllowance: "1 USDC" });
    assert.equal(chain.msgs.length, 1, "no transaction per request");
  } finally {
    svc.close();
  }
});
