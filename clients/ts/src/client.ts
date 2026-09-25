import { Writer, readFields, first, text } from "./proto.js";
import { Rpc, RpcError, TxEvent, TxResult } from "./rpc.js";
import { Key, isAddress } from "./keys.js";
import { buildSend, memoOf, SignedTx } from "./tx.js";
import { DENOM, parseAmount } from "./amount.js";

export type TxStatus = "pending" | "confirmed" | "failed";

export interface Transfer {
  from: string;
  to: string;
  amountUaeth: bigint;
}

export interface TransactionInfo {
  hash: string;
  status: TxStatus;
  height?: number;
  code?: number;
  codespace?: string;
  log?: string;
  memo?: string; // set by the sender: untrusted
  transfers: Transfer[];
}

export interface IncomingPayment {
  hash: string;
  height: number;
  code: number;
  from: string;
  amountUaeth: bigint; // everything this transaction moved to the address
  memo: string; // set by the sender: untrusted
}

export interface SendResult {
  hash: string;
  /** "pending": accepted, not yet in a block. "confirmed" only when re-sending bytes already in a block. */
  status: "pending" | "confirmed" | "failed";
  code: number;
  log: string;
  /** The signed transaction: re-broadcast these exact bytes to retry without paying twice. */
  signed: SignedTx;
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

export class AetherClient {
  readonly rpc: Rpc;
  // Sequences this client has used but the chain may not report yet
  // (a second send within one block would otherwise reuse one).
  private nextSeq = new Map<string, bigint>();

  constructor(readonly opts: { rpc: string; chainId?: string; fetch?: typeof fetch }) {
    this.rpc = new Rpc(opts.rpc, opts.fetch);
  }

  get chainId(): string {
    return this.opts.chainId ?? "aether-testnet-1";
  }

  latestHeight(): Promise<number> {
    return this.rpc.latestHeight();
  }

  /** Balance in uaeth. */
  async balance(address: string): Promise<bigint> {
    const req = new Writer().string(1, address).string(2, DENOM).finish();
    const resp = await this.rpc.abciQuery("/cosmos.bank.v1beta1.Query/Balance", req);
    const c = first(readFields(resp), 1)?.bytes;
    return c ? BigInt(text(first(readFields(c), 2)?.bytes) || "0") : 0n;
  }

  /** Account number and next sequence; undefined if the account doesn't exist yet (never received funds). */
  async accountInfo(address: string): Promise<{ accountNumber: bigint; sequence: bigint } | undefined> {
    try {
      const resp = await this.rpc.abciQuery("/cosmos.auth.v1beta1.Query/AccountInfo", new Writer().string(1, address).finish());
      const info = first(readFields(resp), 1)?.bytes ?? new Uint8Array();
      const f = readFields(info);
      return { accountNumber: first(f, 3)?.varint ?? 0n, sequence: first(f, 4)?.varint ?? 0n };
    } catch (e) {
      if (e instanceof RpcError && /not found/i.test(e.message)) return undefined;
      throw e;
    }
  }

  /**
   * Signs and broadcasts a payment. `amount` must carry its unit ("1.5 AETH").
   * Returns once the node accepts it -- call waitForTransaction to confirm.
   * To retry after an error without risking a second payment, re-broadcast
   * result.signed.txBytes (rebroadcast) rather than calling send again.
   */
  async send(key: Key, to: string, amount: string, opts: { memo?: string; gasLimit?: bigint | number } = {}): Promise<SendResult> {
    if (!isAddress(to)) throw new Error(`invalid recipient address "${to}"`);
    const amountUaeth = parseAmount(amount);
    if ((opts.memo ?? "").length > 256) throw new Error("memo is limited to 256 characters");
    const info = await this.accountInfo(key.address);
    if (!info) throw new Error(`account ${key.address} doesn't exist on chain yet: fund it first`);
    const known = this.nextSeq.get(key.address) ?? 0n;
    const sequence = info.sequence > known ? info.sequence : known;
    const signed = buildSend(key, { chainId: this.chainId, accountNumber: info.accountNumber, sequence, to, amountUaeth, memo: opts.memo, gasLimit: opts.gasLimit });
    const r = await this.rebroadcast(signed);
    if (r.status !== "failed") this.nextSeq.set(key.address, sequence + 1n);
    return r;
  }

  /**
   * (Re)broadcasts already-signed bytes: the chain includes them at most
   * once. When the node already knows them (in its cache, its mempool, or a
   * block), reports where they actually stand instead of an error.
   */
  async rebroadcast(signed: SignedTx): Promise<SendResult> {
    let r: { code: number; codespace: string; log: string };
    try {
      r = await this.rpc.broadcastSync(signed.txBytes);
    } catch (e) {
      if (!(e instanceof RpcError && /already exists in cache/i.test(e.message))) throw e;
      return this.known(signed, 19, e.message);
    }
    if (r.code === 0) return { hash: signed.hash, status: "pending", code: 0, log: r.log, signed };
    // 19: already in the mempool; 32: its sequence is used -- possibly by
    // these very bytes, already in a block.
    if (r.codespace === "sdk" && (r.code === 19 || r.code === 32)) return this.known(signed, r.code, r.log);
    return { hash: signed.hash, status: "failed", code: r.code, log: r.log, signed };
  }

  private async known(signed: SignedTx, code: number, log: string): Promise<SendResult> {
    const t = await this.getTransaction(signed.hash);
    if (t.status === "confirmed") return { hash: signed.hash, status: "confirmed", code: 0, log: t.log ?? "", signed };
    if (t.status === "failed") return { hash: signed.hash, status: "failed", code: t.code ?? code, log: t.log ?? log, signed };
    // Not in a block: in flight (19), or superseded by another transaction (32).
    return { hash: signed.hash, status: code === 19 ? "pending" : "failed", code, log, signed };
  }

  async getTransaction(hash: string): Promise<TransactionInfo> {
    const t = await this.rpc.tx(hash);
    if (!t) return { hash: hash.toUpperCase(), status: "pending", transfers: [] };
    return {
      hash: t.hash, status: t.code === 0 ? "confirmed" : "failed", height: t.height, code: t.code, codespace: t.codespace,
      log: t.log, memo: memoOf(t.tx), transfers: transfers(t.events),
    };
  }

  /** Waits until hash is confirmed or failed, or timeoutMs passes (then: pending). */
  async waitForTransaction(hash: string, timeoutMs = 90_000, pollMs = 3_000): Promise<TransactionInfo> {
    const deadline = Date.now() + timeoutMs;
    for (;;) {
      const info = await this.getTransaction(hash);
      if (info.status !== "pending" || Date.now() >= deadline) return info;
      await sleep(Math.min(pollMs, Math.max(0, deadline - Date.now())));
    }
  }

  /** Every payment to address at or above sinceHeight, oldest first (reads all pages). */
  async incomingPayments(address: string, sinceHeight = 1, maxResults = 5000): Promise<IncomingPayment[]> {
    if (!isAddress(address)) throw new Error(`invalid address "${address}"`); // it goes into a query string
    const query = `transfer.recipient='${address}' AND tx.height>=${Math.max(1, sinceHeight)}`;
    const out: IncomingPayment[] = [];
    for (let page = 1; ; page++) {
      const { txs, total } = await this.rpc.txSearch(query, page, 100);
      for (const t of txs) {
        const to = transfers(t.events).filter((x) => x.to === address);
        if (to.length === 0) continue;
        out.push({ hash: t.hash, height: t.height, code: t.code, from: to[0].from, amountUaeth: to.reduce((s, x) => s + x.amountUaeth, 0n), memo: memoOf(t.tx) });
      }
      if (out.length >= maxResults) throw new Error(`more than ${maxResults} incoming transactions since height ${sinceHeight}: pass a later sinceHeight`);
      if (txs.length < 100 || page * 100 >= total) return out;
    }
  }

  /** Waits for a confirmed payment to address with exactly memo and at least minAmount. */
  async waitForPayment(opts: { address: string; memo: string; minAmount: string; sinceHeight?: number; timeoutMs?: number; pollMs?: number }): Promise<IncomingPayment | undefined> {
    const min = parseAmount(opts.minAmount);
    const deadline = Date.now() + (opts.timeoutMs ?? 90_000);
    for (;;) {
      const found = (await this.incomingPayments(opts.address, opts.sinceHeight ?? 1)).find((p) => p.code === 0 && p.memo === opts.memo && p.amountUaeth >= min);
      if (found || Date.now() >= deadline) return found;
      await sleep(Math.min(opts.pollMs ?? 3_000, Math.max(0, deadline - Date.now())));
    }
  }
}

/** The transfers in a transaction's events. */
export function transfers(events: TxEvent[]): Transfer[] {
  const out: Transfer[] = [];
  for (const e of events) {
    if (e.type !== "transfer") continue;
    const a = Object.fromEntries(e.attributes.map((x) => [x.key, x.value]));
    for (const part of (a.amount ?? "").split(",")) {
      const m = /^([0-9]+)uaeth$/.exec(part.trim());
      if (m) out.push({ from: a.sender ?? "", to: a.recipient ?? "", amountUaeth: BigInt(m[1]) });
    }
  }
  return out;
}

export type { TxResult };
