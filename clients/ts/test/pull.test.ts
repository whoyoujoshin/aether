import { test } from "node:test";
import assert from "node:assert/strict";
import { createServer, type Server } from "node:http";
import { base64 } from "@scure/base";
import { sha256 } from "@noble/hashes/sha2.js";
import { bytesToHex } from "@noble/hashes/utils.js";
import { Writer, readFields, first, text } from "../src/proto.js";
import {
  AetherClient, FileLedger, Key, Paywall, fetchPaid, pullPaymentHeader, signingMessage, MSG_GRANT_TYPE_URL, MSG_EXEC_TYPE_URL,
  PaymentError, type Payment,
} from "../src/index.js";

const CHAIN = "aether-testnet-1";
const SEND_AUTH = "/cosmos.bank.v1beta1.SendAuthorization";

type Grant = { limit: bigint; allowList: string[]; expiration: number };

/**
 * A node that applies the x/authz messages broadcast to it: MsgGrant
 * stores (replaces) an allowance, MsgExec spends under one or fails.
 * Every accepted transaction goes straight into a block.
 */
class AuthzChain {
  txs = new Map<string, unknown>();
  grants = new Map<string, Grant>(); // `${granter}/${grantee}`
  balances = new Map<string, bigint>();
  broadcasts: { typeUrl: string; hash: string }[] = [];
  seq = new Map<string, bigint>();

  fetch = (async (_url: unknown, init?: { body?: unknown }) => {
    const { method, params } = JSON.parse(String(init?.body)) as { method: string; params: Record<string, string> };
    const ok = (result: unknown) => new Response(JSON.stringify({ jsonrpc: "2.0", id: 1, result }));
    const fail = (message: string) => new Response(JSON.stringify({ jsonrpc: "2.0", id: 1, error: { code: -32603, message: "Internal error", data: message } }));
    if (method === "tx") {
      const hash = Buffer.from(params.hash, "base64").toString("hex").toUpperCase();
      const t = this.txs.get(hash);
      return t ? ok(t) : fail(`tx (${hash}) not found`);
    }
    if (method === "abci_query") {
      const data = Buffer.from(params.data, "hex");
      if (params.path === "/cosmos.auth.v1beta1.Query/AccountInfo") {
        const addr = text(first(readFields(data), 1)?.bytes);
        const info = new Writer().uint64(3, 9n).uint64(4, this.seq.get(addr) ?? 0n).finish();
        return ok({ response: { code: 0, log: "", value: base64.encode(new Writer().message(1, info).finish()) } });
      }
      if (params.path === "/cosmos.authz.v1beta1.Query/Grants") {
        const f = readFields(data);
        const g = this.grants.get(`${text(first(f, 1)?.bytes)}/${text(first(f, 2)?.bytes)}`);
        if (!g) return ok({ response: { code: 2, log: "authorization not found: key not found", value: null } });
        const auth = new Writer().message(1, new Writer().string(1, "uaeth").string(2, g.limit.toString()).finish());
        for (const a of g.allowList) auth.string(2, a);
        const any = new Writer().string(1, SEND_AUTH).bytes(2, auth.finish()).finish();
        const grant = new Writer().message(1, any).message(2, new Writer().uint64(1, BigInt(g.expiration)).finish()).finish();
        return ok({ response: { code: 0, log: "", value: base64.encode(new Writer().message(1, grant).finish()) } });
      }
      throw new Error("unexpected query " + params.path);
    }
    if (method === "broadcast_tx_sync") return ok(this.apply(base64.decode(params.tx)));
    throw new Error("unexpected " + method);
  }) as unknown as typeof fetch;

  private apply(tx: Uint8Array) {
    const hash = bytesToHex(sha256(tx)).toUpperCase();
    if (this.txs.has(hash)) return { code: 19, codespace: "sdk", log: "tx already in mempool", hash };
    const body = readFields(first(readFields(tx), 1)!.bytes!);
    const signer = readFields(first(readFields(first(readFields(first(readFields(tx), 2)!.bytes!), 1)!.bytes!), 1)!.bytes!);
    void signer;
    let code = 0;
    const events: unknown[] = [];
    for (const m of body.filter((x) => x.field === 1)) {
      const any = readFields(m.bytes!);
      const typeUrl = text(first(any, 1)?.bytes);
      const v = readFields(first(any, 2)?.bytes ?? new Uint8Array());
      this.broadcasts.push({ typeUrl, hash });
      if (typeUrl === MSG_GRANT_TYPE_URL) {
        const granter = text(first(v, 1)?.bytes), grantee = text(first(v, 2)?.bytes);
        const g = readFields(first(v, 3)!.bytes!);
        const auth = readFields(first(readFields(first(g, 1)!.bytes!), 2)!.bytes!);
        const coin = readFields(first(auth, 1)!.bytes!);
        this.grants.set(`${granter}/${grantee}`, {
          limit: BigInt(text(first(coin, 2)?.bytes)), allowList: auth.filter((x) => x.field === 2).map((x) => text(x.bytes)),
          expiration: Number(first(readFields(first(g, 2)!.bytes!), 1)?.varint ?? 0n),
        });
        this.bump(granter);
      } else if (typeUrl === MSG_EXEC_TYPE_URL) {
        const grantee = text(first(v, 1)?.bytes);
        const send = readFields(first(readFields(first(v, 2)!.bytes!), 2)!.bytes!);
        const from = text(first(send, 1)?.bytes), to = text(first(send, 2)?.bytes);
        const amount = BigInt(text(first(readFields(first(send, 3)!.bytes!), 2)?.bytes));
        const key = `${from}/${grantee}`;
        const g = this.grants.get(key);
        if (!g || g.limit < amount || (g.allowList.length && !g.allowList.includes(to))) code = 4;
        else {
          g.limit -= amount;
          if (g.limit === 0n) this.grants.delete(key); // like the chain: a used-up allowance is removed
          this.balances.set(to, (this.balances.get(to) ?? 0n) + amount);
          events.push({ type: "transfer", attributes: [{ key: "recipient", value: to }, { key: "sender", value: from }, { key: "amount", value: `${amount}uaeth` }] });
        }
        this.bump(grantee);
      }
    }
    this.txs.set(hash, { hash, height: "9", tx: base64.encode(tx), tx_result: { code, log: code ? "failed to get grant: authorization not found" : "", events } });
    return { code: 0, codespace: "", log: "", hash };
  }

  private bump(addr: string) {
    this.seq.set(addr, (this.seq.get(addr) ?? 0n) + 1n);
  }
}

async function setup(credit = "1 AETH") {
  const chain = new AuthzChain();
  const client = new AetherClient({ rpc: "http://node", chainId: CHAIN, fetch: chain.fetch });
  const seller = Key.random();
  const collector = Key.random();
  const pw = new Paywall({ client, payTo: seller.address, price: "0.01 AETH", pull: { collectorKey: collector, ledger: "", credit } });
  const served: Payment[] = [];
  const srv: Server = createServer((req, res) =>
    pw.middleware()(req, res, () => {
      served.push((req as unknown as { aether: Payment }).aether);
      res.end("ok");
    }));
  await new Promise<void>((r) => srv.listen(0, "127.0.0.1", r));
  const url = `http://127.0.0.1:${(srv.address() as { port: number }).port}/q`;
  const ledger = (pw as unknown as { pullLedger: FileLedger }).pullLedger;
  return { chain, client, pw, url, seller, collector, served, ledger, close: () => srv.close() };
}

test("aether-pull: the TS buyer grants once, then pays instantly; the TS seller collects in batches", async () => {
  const s = await setup();
  try {
    const buyer = Key.random();
    const r1 = await fetchPaid(s.client, buyer, s.url, { maxAmount: "0.02 AETH", pullAllowance: "0.05 AETH", prepay: "1 AETH", requestId: "r1", confirmTimeoutMs: 5000 });
    assert.equal(r1.status, "paid");
    assert.equal(r1.scheme, "aether-pull");
    assert.ok(r1.grantTxHash, "the allowance it granted");
    assert.equal(r1.owedUaeth, 10_000n);
    assert.equal(r1.allowanceUaeth, 40_000n);
    const g = s.chain.grants.get(`${buyer.address}/${s.collector.address}`)!;
    assert.equal(g.limit, 50_000n);
    assert.deepEqual(g.allowList, [s.seller.address], "payable only to the seller");
    assert.ok(Math.abs(g.expiration - (Date.now() / 1000 + 7 * 86400)) < 60);

    for (const id of ["r2", "r3"]) {
      const r = await fetchPaid(s.client, buyer, s.url, { maxAmount: "0.02 AETH", pullAllowance: "0.05 AETH", requestId: id });
      assert.equal(r.status, "paid");
    }
    assert.equal(s.chain.broadcasts.length, 1, "one transaction: the allowance");
    await assert.rejects(fetchPaid(s.client, buyer, s.url, { maxAmount: "0.02 AETH", pullAllowance: "0.05 AETH", requestId: "r3" }),
      (e: PaymentError) => e.code === "PAYMENT_ALREADY_REDEEMED");

    await s.pw.collectAll(); // signs, saves, sends
    await s.pw.collectAll(); // sees it in a block
    assert.equal(s.chain.balances.get(s.seller.address), 30_000n, "collected in one transfer");
    assert.equal(s.chain.broadcasts.filter((b) => b.typeUrl === MSG_EXEC_TYPE_URL).length, 1);
    assert.equal(s.chain.grants.get(`${buyer.address}/${s.collector.address}`)!.limit, 20_000n);
    const a = s.ledger.pullAccount(buyer.address);
    assert.equal(a.accrued + a.inFlight + a.unpaid, 0n);
    assert.equal(s.served.length, 3);
  } finally {
    s.close();
  }
});

test("aether-pull: a revoked allowance's debt blocks the buyer until a new one covers it", async () => {
  const s = await setup();
  try {
    const buyer = Key.random();
    const opts = (id: string, allowance = "0.05 AETH") => ({ maxAmount: "0.02 AETH", pullAllowance: allowance, requestId: id });
    await fetchPaid(s.client, buyer, s.url, opts("a"));
    await fetchPaid(s.client, buyer, s.url, opts("b"));
    s.chain.grants.delete(`${buyer.address}/${s.collector.address}`); // the buyer revokes
    await s.pw.collectAll();
    await s.pw.collectAll();
    assert.equal(s.ledger.pullAccount(buyer.address).unpaid, 20_000n);

    await assert.rejects(fetchPaid(s.client, buyer, s.url, opts("c", "0.02 AETH")), (e: PaymentError) => e.code === "INVALID_ARGUMENT" && /at least 30000/.test(e.message));
    const r = await fetchPaid(s.client, buyer, s.url, opts("c"));
    assert.equal(r.status, "paid");
    assert.equal(r.owedUaeth, 30_000n, "the old debt is owed again, with this request");
    await s.pw.collectAll();
    await s.pw.collectAll();
    assert.equal(s.chain.balances.get(s.seller.address), 30_000n, "every served request was paid for");
  } finally {
    s.close();
  }
});

test("aether-pull: prepaid signatures don't pass as pull; credit caps what's owed", async () => {
  const s = await setup("0.02 AETH");
  try {
    const buyer = Key.random();
    await fetchPaid(s.client, buyer, s.url, { maxAmount: "0.02 AETH", pullAllowance: "0.1 AETH", requestId: "1" });
    const u = new URL(s.url);
    const f = { network: CHAIN, payTo: s.seller.address, host: u.host, method: "GET", path: "/q", body: new Uint8Array(), maxPrice: 10_000n, timestamp: Math.floor(Date.now() / 1000), requestId: "x" };
    const header = pullPaymentHeader(buyer, f);
    // Swap in a signature over the prepaid message.
    const decoded = JSON.parse(Buffer.from(header, "base64").toString());
    decoded.payload.signature = base64.encode(buyer.sign(signingMessage(f)));
    const forged = await fetch(s.url, { headers: { "X-PAYMENT": Buffer.from(JSON.stringify(decoded)).toString("base64") } });
    assert.equal(forged.status, 402);
    assert.equal(((await forged.json()) as { error: string }).error, "invalid_signature");

    await fetchPaid(s.client, buyer, s.url, { maxAmount: "0.02 AETH", pullAllowance: "0.1 AETH", requestId: "2" });
    const over = await fetch(s.url, { headers: { "X-PAYMENT": pullPaymentHeader(buyer, { ...f, requestId: "3" }) } });
    assert.equal(over.status, 402);
    assert.equal(((await over.json()) as { error: string }).error, "settlement_pending");
  } finally {
    s.close();
  }
});
