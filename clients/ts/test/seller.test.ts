import { test } from "node:test";
import assert from "node:assert/strict";
import { createServer, type Server } from "node:http";
import { mkdtempSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { base64 } from "@scure/base";
import { sha256 } from "@noble/hashes/sha2.js";
import { Writer } from "../src/proto.js";
import {
  AetherClient, FileLedger, Key, Paywall, WITHDRAW_PATH, buildSend, memoPaymentHeader, prepaidPaymentHeader, withdrawPrepaid, fetchPaid,
  type Payment,
} from "../src/index.js";

const CHAIN = "aether-testnet-1";

/** A node that knows the transactions tests put in blocks, over the client's JSON-RPC. */
class FakeChain {
  txs = new Map<string, unknown>();
  broadcasts: Uint8Array[] = [];
  broadcast: (tx: Uint8Array) => { code: number; codespace?: string; log?: string } = () => ({ code: 0 });
  seq = 4n;
  lookups = 0;

  fetch = (async (_url: unknown, init?: { body?: unknown }) => {
    const { method, params } = JSON.parse(String(init?.body)) as { method: string; params: Record<string, string> };
    const ok = (result: unknown) => new Response(JSON.stringify({ jsonrpc: "2.0", id: 1, result }));
    if (method === "tx") {
      this.lookups++;
      const hash = Buffer.from(params.hash, "base64").toString("hex").toUpperCase();
      const t = this.txs.get(hash);
      if (!t) return new Response(JSON.stringify({ jsonrpc: "2.0", id: 1, error: { code: -32603, message: "Internal error", data: `tx (${hash}) not found` } }));
      return ok(t);
    }
    if (method === "abci_query") {
      const info = new Writer().uint64(3, 9n).uint64(4, this.seq).finish();
      return ok({ response: { code: 0, log: "", value: base64.encode(new Writer().message(1, info).finish()) } });
    }
    if (method === "broadcast_tx_sync") {
      const tx = base64.decode(params.tx);
      this.broadcasts.push(tx);
      const r = this.broadcast(tx);
      return ok({ code: r.code, codespace: r.codespace ?? "", log: r.log ?? "", hash: "" });
    }
    throw new Error("unexpected " + method);
  }) as unknown as typeof fetch;

  /** Puts a payment in a block; returns its hash. */
  pay(to: string, uaeth: bigint, memo: string, code = 0, from = Key.random()): string {
    const s = buildSend(from, { chainId: CHAIN, accountNumber: 1, sequence: 0, to, amountUaeth: uaeth, memo });
    this.include(s.txBytes, s.hash, code, [{ sender: from.address, recipient: to, amount: `${uaeth}uaeth` }]);
    return s.hash;
  }

  include(txBytes: Uint8Array, hash: string, code = 0, transfers: { sender: string; recipient: string; amount: string }[] = []) {
    this.txs.set(hash, {
      hash, height: "7", tx: base64.encode(txBytes),
      tx_result: {
        code, log: code ? "failed" : "", events: transfers.map((t) => ({
          type: "transfer", attributes: Object.entries(t).map(([key, value]) => ({ key, value })),
        })),
      },
    });
  }
}

interface Setup {
  chain: FakeChain;
  client: AetherClient;
  pw: Paywall;
  url: string;
  seller: Key;
  served: Payment[];
  now: { t: number };
  close(): void;
}

async function setup(opts: { payout?: boolean; ledgerPath?: string; failWith?: number } = {}): Promise<Setup> {
  const chain = new FakeChain();
  const client = new AetherClient({ rpc: "http://node", chainId: CHAIN, fetch: chain.fetch });
  const seller = Key.random();
  const now = { t: Date.now() };
  const pw = new Paywall({
    client, payTo: seller.address, price: "0.01 AETH", name: "Weather", description: "forecasts",
    prepaid: { ledger: opts.ledgerPath ?? "", minDeposit: "0.03 AETH", payoutKey: opts.payout ? Key.random() : undefined },
    now: () => now.t,
  });
  const served: Payment[] = [];
  const mw = pw.middleware({ free: ["/health"] });
  const server: Server = createServer((req, res) =>
    mw(req, res, (err) => {
      if (err) {
        res.writeHead(500).end(String(err));
        return;
      }
      const r = req as typeof req & { aether?: Payment; rawBody?: Uint8Array; body?: unknown };
      if (r.aether) served.push(r.aether);
      res.writeHead(opts.failWith ?? 200, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ path: req.url, body: r.body ?? null }));
    }));
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const port = (server.address() as { port: number }).port;
  return { chain, client, pw, url: `http://127.0.0.1:${port}`, seller, served, now, close: () => server.close() };
}

const quote = async (url: string, init?: RequestInit) => {
  const r = await fetch(url, init);
  assert.equal(r.status, 402);
  return (await r.json()) as { error: string; accepts: { scheme: string; payTo: string; maxAmountRequired: string; extra: Record<string, string> }[] };
};

test("unpaid requests get a 402 offering both schemes; free paths and the manifest don't", async () => {
  const s = await setup({ payout: true });
  try {
    const q = await quote(s.url + "/forecast");
    assert.equal(q.error, "payment_required");
    assert.deepEqual(q.accepts.map((a) => a.scheme), ["aether-memo", "aether-prepaid"]);
    assert.equal(q.accepts[0].maxAmountRequired, "10000");
    assert.match(q.accepts[0].extra.invoice, /^x402-/);
    assert.equal(q.accepts[1].extra.minDeposit, "30000");
    assert.equal(q.accepts[1].extra.withdrawPath, WITHDRAW_PATH);
    assert.equal((await fetch(s.url + "/health")).status, 200);
    const m = await (await fetch(s.url + "/.well-known/x402")).json();
    assert.deepEqual(m, {
      x402Version: 1, name: "Weather", description: "forecasts", network: CHAIN, payTo: s.seller.address, price: "10000", priceAeth: "0.01",
      schemes: ["aether-memo", "aether-prepaid"], minDeposit: "30000", withdrawPath: WITHDRAW_PATH,
    });
  } finally {
    s.close();
  }
});

test("aether-memo: a confirmed payment of the invoice is served exactly once", async () => {
  const s = await setup();
  try {
    const invoice = (await quote(s.url + "/forecast")).accepts[0].extra.invoice;
    const present = (hash: string, inv = invoice, path = "/forecast") => fetch(s.url + path, { headers: { "X-PAYMENT": memoPaymentHeader(CHAIN, inv, hash) } });

    const unseen = s.chain.pay(s.seller.address, 10_000n, invoice);
    s.chain.txs.delete(unseen); // not in a block yet
    let r = await present(unseen);
    const pending = (await r.json()) as { error: string; accepts: { extra: { invoice: string } }[] };
    assert.equal(pending.error, "payment_not_confirmed");
    assert.equal(pending.accepts[0].extra.invoice, invoice, "the same invoice: the payment may still land");
    assert.equal(r.headers.get("retry-after"), "10");

    const hash = s.chain.pay(s.seller.address, 10_000n, invoice);
    r = await present(hash);
    assert.equal(r.status, 200);
    const settlement = JSON.parse(Buffer.from(r.headers.get("x-payment-response")!, "base64").toString());
    assert.equal(settlement.transaction, hash);
    assert.equal(s.served[0].scheme, "aether-memo");
    assert.equal(s.served[0].txHash, hash);

    assert.equal(((await (await present(hash)).json()) as { error: string }).error, "invoice_already_redeemed");
    assert.equal(((await (await present(hash, invoice, "/other")).json()) as { error: string }).error, "invoice_for_other_resource");

    const inv2 = (await quote(s.url + "/forecast")).accepts[0].extra.invoice;
    const cases: [string, string][] = [
      [s.chain.pay(s.seller.address, 9_999n, inv2), "insufficient_payment"],
      [s.chain.pay(s.seller.address, 10_000n, "something else"), "memo_mismatch"],
      [s.chain.pay(Key.random().address, 10_000n, inv2), "insufficient_payment"],
      [s.chain.pay(s.seller.address, 10_000n, inv2, 5), "payment_failed"],
    ];
    for (const [h, want] of cases) assert.equal(((await (await present(h, inv2)).json()) as { error: string }).error, want);
    const forged = inv2.slice(0, -2) + (inv2.endsWith("AA") ? "AB" : "AA");
    assert.equal(((await (await present(hash, forged)).json()) as { error: string }).error, "invalid_invoice");

    s.now.t += 25 * 3600_000;
    assert.equal(((await (await present(s.chain.pay(s.seller.address, 10_000n, inv2), inv2)).json()) as { error: string }).error, "invoice_expired");
  } finally {
    s.close();
  }
});

test("aether-memo: a failed response lets the same payment be used again", async () => {
  const s = await setup({ failWith: 503 });
  try {
    const invoice = (await quote(s.url + "/forecast")).accepts[0].extra.invoice;
    const hash = s.chain.pay(s.seller.address, 10_000n, invoice);
    const h = { "X-PAYMENT": memoPaymentHeader(CHAIN, invoice, hash) };
    assert.equal((await fetch(s.url + "/forecast", { headers: h })).status, 503);
    await new Promise((r) => setTimeout(r, 20));
    assert.equal((await fetch(s.url + "/forecast", { headers: h })).status, 503, "not invoice_already_redeemed");
  } finally {
    s.close();
  }
});

function signed(s: Setup, key: Key, o: { path?: string; method?: string; body?: string; requestId: string; depositTx?: string; maxPrice?: bigint; host?: string }) {
  const u = new URL(s.url);
  const body = new TextEncoder().encode(o.body ?? "");
  return prepaidPaymentHeader(key, {
    network: CHAIN, payTo: s.seller.address, host: o.host ?? u.host, method: o.method ?? "POST", path: o.path ?? "/forecast", body,
    maxPrice: o.maxPrice ?? 10_000n, timestamp: Math.floor(s.now.t / 1000), requestId: o.requestId, depositTx: o.depositTx,
  });
}

test("aether-prepaid: deposit once, then each signed request is charged once", async () => {
  const s = await setup();
  try {
    const agent = Key.random();
    const post = (h: string, body = '{"q":1}') => fetch(s.url + "/forecast", { method: "POST", headers: { "X-PAYMENT": h, "Content-Type": "application/json" }, body });

    let r = await post(signed(s, agent, { requestId: "r0", body: '{"q":1}' }));
    let q = (await r.json()) as { error: string; accepts: { extra: Record<string, string> }[] };
    assert.equal(q.error, "insufficient_balance");
    assert.equal(q.accepts[1].extra.balance, "0");

    const tooSmall = s.chain.pay(s.seller.address, 29_999n, "prepaid:" + agent.address);
    q = (await (await post(signed(s, agent, { requestId: "r0", body: '{"q":1}', depositTx: tooSmall }))).json()) as typeof q;
    assert.equal(q.error, "insufficient_payment");

    const dep = s.chain.pay(s.seller.address, 50_000n, "prepaid:" + agent.address);
    r = await post(signed(s, agent, { requestId: "r1", body: '{"q":1}', depositTx: dep }));
    assert.equal(r.status, 200);
    const echoed = (await r.json()) as { body: unknown };
    assert.deepEqual(echoed.body, { q: 1 }, "the route still gets the parsed body");
    assert.equal(s.served.at(-1)!.payer, agent.address);
    assert.equal(s.served.at(-1)!.balanceUaeth, 40_000n);

    // The same deposit again credits nothing; a new request is charged from the balance.
    r = await post(signed(s, agent, { requestId: "r2", body: '{"q":1}', depositTx: dep }));
    assert.equal(r.status, 200);
    assert.equal(s.served.at(-1)!.balanceUaeth, 30_000n);

    q = (await (await post(signed(s, agent, { requestId: "r2", body: '{"q":1}' }))).json()) as typeof q;
    assert.equal(q.error, "invoice_already_redeemed", "a replayed request is never charged twice");

    const refusals: [string, string][] = [
      [signed(s, agent, { requestId: "r3", body: '{"q":2}' }), "invalid_signature"], // body differs from what's sent
      [signed(s, agent, { requestId: "r3", body: '{"q":1}', path: "/other" }), "invalid_signature"],
      [signed(s, agent, { requestId: "r3", body: '{"q":1}', host: "evil.example" }), "invalid_signature"],
      [signed(s, agent, { requestId: "r3", body: '{"q":1}', maxPrice: 9_999n }), "price_above_signed_max"],
    ];
    for (const [h, want] of refusals) assert.equal(((await (await post(h)).json()) as { error: string }).error, want);

    // Someone else's deposit credits them, not the presenter.
    const other = Key.random();
    const theirs = s.chain.pay(s.seller.address, 50_000n, "prepaid:" + other.address);
    r = await post(signed(s, agent, { requestId: "r4", body: '{"q":1}', depositTx: theirs }));
    assert.equal(r.status, 200);
    assert.equal(s.served.at(-1)!.balanceUaeth, 20_000n, "charged from its own balance, not credited");
    r = await post(signed(s, other, { requestId: "o1", body: '{"q":1}' }));
    assert.equal(r.status, 200);
    assert.equal(s.served.at(-1)!.balanceUaeth, 40_000n, "the deposit went to the account its memo names");

    s.now.t += 10 * 60_000;
    const stale = signed(s, agent, { requestId: "r5", body: '{"q":1}' });
    s.now.t -= 10 * 60_000;
    assert.equal(((await (await post(stale)).json()) as { error: string }).error, "stale_request");
  } finally {
    s.close();
  }
});

test("the ledger file is the Go paywall's format and survives a restart", async () => {
  const path = join(mkdtempSync(join(tmpdir(), "ledger-")), "ledger.json");
  const a = new FileLedger(path);
  a.credit("DEP1", "aether1abc", 50_000n);
  assert.deepEqual(a.charge("aether1abc", "r1", 10_000n, Date.now() + 60_000), { balance: 40_000n, ok: true, fresh: true });
  const onDisk = JSON.parse(readFileSync(path, "utf8"));
  assert.deepEqual(Object.keys(onDisk).sort(), ["balances", "deposits", "requests", "withdrawals"]);
  assert.equal(onDisk.balances["aether1abc"], "40000");
  const b = new FileLedger(path);
  assert.equal(b.balance("aether1abc"), 40_000n);
  assert.deepEqual(b.charge("aether1abc", "r1", 10_000n, Date.now() + 60_000), { balance: 40_000n, ok: false, fresh: false });
  assert.deepEqual(b.credit("DEP1", "aether1abc", 50_000n), { balance: 40_000n, credited: false });
});

test("withdrawals: paid back once to the signer, via the TS buyer client", async () => {
  const s = await setup({ payout: true });
  try {
    const agent = Key.random();
    const dep = s.chain.pay(s.seller.address, 50_000n, "prepaid:" + agent.address);
    let r = await fetch(s.url + "/forecast", { method: "POST", headers: { "X-PAYMENT": signed(s, agent, { requestId: "r1", depositTx: dep }) } });
    assert.equal(r.status, 200);

    const w = await withdrawPrepaid(s.client, agent, s.url + "/anything", { withdrawalId: "w1" });
    assert.equal(w.status, "pending");
    assert.equal(w.amountUaeth, 40_000n);
    assert.equal(w.balanceUaeth, 0n);
    assert.equal(s.chain.broadcasts.length, 1);
    const payout = s.chain.broadcasts[0];

    // Once it's in a block, asking again reports it without sending anything new.
    s.chain.include(payout, w.txHash!, 0, []);
    const again = await withdrawPrepaid(s.client, agent, s.url, { withdrawalId: "w1" });
    assert.equal(again.status, "confirmed");
    assert.equal(again.txHash, w.txHash);
    assert.equal(s.chain.broadcasts.length, 1);

    await assert.rejects(withdrawPrepaid(s.client, agent, s.url, { withdrawalId: "w2" }), { code: "INSUFFICIENT_PREPAID_BALANCE" });
    await assert.rejects(withdrawPrepaid(s.client, agent, s.url, { withdrawalId: "w1", amount: "0.03 AETH" }), { code: "IDEMPOTENCY_CONFLICT" });
  } finally {
    s.close();
  }
});

test("withdrawals: a rejected payout returns the balance; an unknown outcome is never paid twice", async () => {
  const path = join(mkdtempSync(join(tmpdir(), "ledger-")), "ledger.json");
  const s = await setup({ payout: true, ledgerPath: path });
  try {
    const agent = Key.random();
    const dep = s.chain.pay(s.seller.address, 50_000n, "prepaid:" + agent.address);
    assert.equal((await fetch(s.url + "/q", { method: "POST", headers: { "X-PAYMENT": signed(s, agent, { path: "/q", requestId: "r1", depositTx: dep }) } })).status, 200);

    s.chain.broadcast = () => ({ code: 5, codespace: "sdk", log: "insufficient funds" });
    await assert.rejects(withdrawPrepaid(s.client, agent, s.url, { withdrawalId: "w1" }), { code: "WITHDRAWAL_FAILED" });
    assert.equal(new FileLedger(path).balance(agent.address), 40_000n, "nothing paid, balance back");

    // The node errors on broadcast: the payout is saved, so a retry re-sends the same bytes.
    s.chain.broadcast = () => { throw new Error("connection reset"); };
    const first = await withdrawPrepaid(s.client, agent, s.url, { withdrawalId: "w1" });
    assert.equal(first.status, "pending");
    assert.equal(first.balanceUaeth, 0n);
    s.chain.broadcast = () => ({ code: 0 });
    const second = await withdrawPrepaid(s.client, agent, s.url, { withdrawalId: "w1" });
    assert.equal(second.txHash, first.txHash);
    const sent = s.chain.broadcasts.slice(-2);
    assert.deepEqual(sent[0], sent[1], "identical bytes");
  } finally {
    s.close();
  }
});

test("fetchPaid (the TS buyer) pays a TS seller", async () => {
  const s = await setup();
  try {
    // The fake chain includes whatever the buyer broadcasts, with its transfer.
    const buyer = Key.random();
    s.chain.broadcast = (tx) => {
      const hash = Buffer.from(sha256(tx)).toString("hex").toUpperCase();
      s.chain.include(tx, hash, 0, [{ sender: buyer.address, recipient: s.seller.address, amount: `${pendingAmount}uaeth` }]);
      return { code: 0 };
    };
    let pendingAmount = 10_000n;
    const res = await fetchPaid(s.client, buyer, s.url + "/forecast", { maxAmount: "0.01 AETH", confirmTimeoutMs: 2000 });
    assert.equal(res.status, "paid");
    assert.equal(res.response!.status, 200);

    pendingAmount = 50_000n;
    const pre = await fetchPaid(s.client, buyer, s.url + "/forecast", { maxAmount: "0.01 AETH", prepay: "0.05 AETH", requestId: "p1", confirmTimeoutMs: 2000 });
    assert.equal(pre.status, "paid");
    assert.equal(pre.balanceUaeth, 40_000n);
    const next = await fetchPaid(s.client, buyer, s.url + "/forecast", { maxAmount: "0.01 AETH", prepay: "0.05 AETH", requestId: "p2" });
    assert.equal(next.balanceUaeth, 30_000n);
    assert.equal(s.served.map((p) => p.scheme).join(","), "aether-memo,aether-prepaid,aether-prepaid");
  } finally {
    s.close();
  }
});

test("a manifest can't point the withdrawal at another host", async () => {
  let hits = 0;
  const server = createServer((req, res) => {
    if (req.url === "/.well-known/x402") {
      res.end(JSON.stringify({ x402Version: 1, network: CHAIN, payTo: Key.random().address, withdrawPath: "@evil.example/w" }));
      return;
    }
    hits++;
    res.end();
  });
  await new Promise<void>((r) => server.listen(0, "127.0.0.1", r));
  try {
    const url = `http://127.0.0.1:${(server.address() as { port: number }).port}`;
    const client = new AetherClient({ rpc: "http://node", chainId: CHAIN });
    await assert.rejects(withdrawPrepaid(client, Key.random(), url), { code: "PAYMENT_UNSUPPORTED" });
    assert.equal(hits, 0);
  } finally {
    server.close();
  }
});
