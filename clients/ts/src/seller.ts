import type { IncomingMessage, ServerResponse } from "node:http";
import { hmac } from "@noble/hashes/hmac.js";
import { sha256 } from "@noble/hashes/sha2.js";
import { bytesToHex, randomBytes } from "@noble/hashes/utils.js";
import { base64, base64urlnopad } from "@scure/base";
import { AetherClient } from "./client.js";
import { RpcError } from "./rpc.js";
import { Key, addressOf, isAddress } from "./keys.js";
import { buildSend } from "./tx.js";
import { formatAeth, parseAmount, parseUaeth } from "./amount.js";
import { MANIFEST_PATH, type Manifest } from "./directory.js";
import { DEPOSIT_MEMO_PREFIX, SCHEME_MEMO, SCHEME_PREPAID, signingMessage, type PaymentRequired, type PaymentRequirements } from "./paywall.js";
import { FileLedger, LedgerError, type Ledger, type Withdrawal } from "./ledger.js";
import { RECEIPT_HEADER, receiptFieldsOK, sha256Hex, signReceipt, verifyReceipt, type ReceiptDelegation } from "./receipt.js";

// Selling: charge AETH per HTTP request from a Node service, compatible
// with the Go paywall (package paywall) and every Aether buyer --
// agentmcp, these clients, or a person paying an invoice by hand.
//
//   const pw = new Paywall({ client, payTo, price: "0.01 AETH", prepaid: { ledger: "ledger.json" } });
//   app.use(pw.middleware({ free: ["/health"] }));   // Express, Connect, or node:http
//
// aether-memo: an unpaid request gets 402 with a one-time invoice; the
// buyer pays it on chain (memo = invoice) and repeats the request with
// proof, which is checked on chain and served once. aether-prepaid (bots):
// deposit once, then sign each request; the price is deducted instantly.
// With prepaid.payoutKey, buyers can withdraw what they haven't spent.

export const WITHDRAW_PATH = "/.well-known/x402/withdraw";

const INVOICE_PREFIX = "x402-";
const SIGNED_WINDOW_S = 300;
const REQUEST_ID_RETENTION_MS = 24 * 3600_000;
const MAX_SIGNED_BODY = 10 << 20;
const SEQUENCE_SPENT_GRACE_MS = 120_000;
const MAX_RECEIPT_BODY = 4 << 20;
const HASH = /^[0-9A-F]{64}$/;

const MEMO_INSTRUCTIONS =
  "Send maxAmountRequired uaeth to payTo with memo set to exactly this invoice, wait until the transaction is in a block, " +
  "then repeat this request with header X-PAYMENT: base64 of the JSON " +
  '{"x402Version":1,"scheme":"aether-memo","network":"<network>","payload":{"invoice":"<invoice>","txHash":"<hash>"}}. ' +
  "Each invoice pays for one response, and must be presented by expiresAt.";
const PREPAID_INSTRUCTIONS =
  "For many requests: deposit at least minDeposit uaeth to payTo with memo depositMemo " +
  '("prepaid:" + the address to credit). Then sign each request with that account\'s ML-DSA key and send X-PAYMENT: base64 of ' +
  '{"x402Version":1,"scheme":"aether-prepaid","network":"<network>","payload":{account,pubKey,timestamp,requestId,maxPrice,depositTx?,signature}}' +
  " -- see package paywall's SigningMessage. The first request after a deposit names it in depositTx. " +
  'Each requestId is charged once. Unspent balance stays with the seller; if withdrawPath is set, POST a request signed the same way there (body {"amount":"all"}) to get it back.';

export interface PaywallConfig {
  /** Looks payments up on chain (and pays withdrawals). Its chainId is the network. */
  client: AetherClient;
  /** Address payments go to. */
  payTo: string;
  /** Per request, with its unit: "0.01 AETH". */
  price: string;
  name?: string;
  description?: string;
  mimeType?: string;
  /** How long a buyer has to pay an invoice and present the payment. Default 24h. */
  invoiceTtlSeconds?: number;
  /** Signs invoices. Default: random, so invoices die with the process. */
  secret?: Uint8Array;
  /** Offer aether-prepaid (for bots). */
  prepaid?: {
    /** A Ledger, or a file path for a FileLedger. It holds customers' balances: back it up. */
    ledger: Ledger | string;
    /** Smallest deposit (and partial withdrawal), with unit. Default: the price. */
    minDeposit?: string;
    /** Pays back unspent balances on request. Keep only a small float in this account. */
    payoutKey?: Key;
  };
  /**
   * Sign a receipt for every paid response, covering the request, the
   * payment and the response. key is payTo's own key, or one payTo
   * delegated receipts to (createReceiptDelegation / `paywall
   * delegate-receipts`) so payTo's key can stay offline.
   */
  receipts?: { key: Key; delegation?: ReceiptDelegation };
  /** Milliseconds; for tests. */
  now?: () => number;
}

/** Who paid for a request, as the middleware sets it on req.aether. */
export interface Payment {
  payer: string;
  scheme: string;
  txHash?: string; // aether-memo
  balanceUaeth?: bigint; // aether-prepaid: left with this seller
}

export interface SellerRequest {
  method: string;
  host: string;
  /** Decoded path, no query. */
  path: string;
  https?: boolean;
  header(name: string): string | undefined;
}

export type Decision =
  | { kind: "respond"; status: number; headers: Record<string, string>; body: string }
  | {
    kind: "serve"; payment: Payment; headers: Record<string, string>; finish(status: number): void;
    /** With receipts on: the X-PAYMENT-RECEIPT value for the response (omit the body if it was too big or streamed). */
    receipt?: (status: number, responseBody?: Uint8Array) => string;
  };

interface PrepaidPayload {
  account: string;
  pubKey: string;
  timestamp: number;
  requestId: string;
  maxPrice: string;
  depositTx?: string;
  signature: string;
}

interface Signed {
  pay: PrepaidPayload;
  maxPrice: bigint;
}

export type Respond = Extract<Decision, { kind: "respond" }>;

const json = (status: number, v: unknown, headers: Record<string, string> = {}): Respond => ({
  kind: "respond", status, headers: { "Content-Type": "application/json", ...headers }, body: JSON.stringify(v) + "\n",
});

function decodeHeader<T>(s: string): T | undefined {
  try {
    return JSON.parse(new TextDecoder().decode(base64.decode(s))) as T;
  } catch {
    return undefined;
  }
}

const encodeHeader = (v: unknown) => base64.encode(new TextEncoder().encode(JSON.stringify(v)));

function u64(b: Uint8Array, off: number, v: bigint) {
  new DataView(b.buffer, b.byteOffset).setBigUint64(off, v);
}

export class Paywall {
  readonly price: bigint;
  readonly network: string;
  private readonly secret: Uint8Array;
  private readonly ttlMs: number;
  private readonly ledger?: Ledger;
  private readonly minDeposit: bigint = 0n;
  private readonly payout?: KeyPayout;
  private readonly redeemed = new Map<string, number>();
  private lastPrune = 0;
  private withdrawQueue: Promise<unknown> = Promise.resolve();

  constructor(private readonly cfg: PaywallConfig) {
    if (!isAddress(cfg.payTo)) throw new Error(`invalid payTo address "${cfg.payTo}"`);
    this.price = parseAmount(cfg.price);
    this.network = cfg.client.chainId;
    this.secret = cfg.secret ?? randomBytes(32);
    this.ttlMs = (cfg.invoiceTtlSeconds ?? 86_400) * 1000;
    if (cfg.prepaid) {
      this.ledger = typeof cfg.prepaid.ledger === "string" ? new FileLedger(cfg.prepaid.ledger) : cfg.prepaid.ledger;
      const min = cfg.prepaid.minDeposit ? parseAmount(cfg.prepaid.minDeposit) : this.price;
      this.minDeposit = min < this.price ? this.price : min;
      if (cfg.prepaid.payoutKey) this.payout = new KeyPayout(cfg.client, cfg.prepaid.payoutKey);
    }
    if (cfg.receipts) {
      const probe = signReceipt(cfg.receipts.key, {
        network: this.network, payTo: cfg.payTo, payer: "", scheme: "", payment: "", amount: "", method: "", host: "", path: "",
        requestHash: sha256Hex(new Uint8Array()), status: 0, at: Math.floor(this.now() / 1000),
      }, cfg.receipts.delegation);
      const bad = verifyReceipt(probe);
      if (bad) throw new Error(`receipts wouldn't verify: ${bad}`);
    }
  }

  private receiptFor(req: SellerRequest, body: Uint8Array, scheme: string, payer: string, payment: string) {
    const r = this.cfg.receipts;
    if (!r) return undefined;
    return (status: number, responseBody?: Uint8Array) => {
      const fields = {
        network: this.network, payTo: this.cfg.payTo, payer, scheme, payment, amount: this.price.toString(), method: req.method,
        host: req.host, path: req.path, requestHash: sha256Hex(body), status,
        responseHash: responseBody ? sha256Hex(responseBody) : undefined, at: Math.floor(this.now() / 1000),
      };
      // e.g. a path with a line break: no receipt rather than an ambiguous one
      return receiptFieldsOK(fields) ? encodeHeader(signReceipt(r.key, fields, r.delegation)) : "";
    };
  }

  private now() {
    return this.cfg.now ? this.cfg.now() : Date.now();
  }

  private schemes() {
    return this.ledger ? [SCHEME_MEMO, SCHEME_PREPAID] : [SCHEME_MEMO];
  }

  /** The service's self-description, served (free) at MANIFEST_PATH. */
  manifest(): Manifest {
    return {
      x402Version: 1, name: this.cfg.name ?? "", description: this.cfg.description ?? "", network: this.network, payTo: this.cfg.payTo,
      price: this.price.toString(), priceAeth: formatAeth(this.price), schemes: this.schemes(),
      ...(this.ledger ? { minDeposit: this.minDeposit.toString() } : {}),
      ...(this.payout ? { withdrawPath: WITHDRAW_PATH } : {}),
    };
  }

  /** Whether handle/withdraw needs the request body (to check a signature, or for a receipt). */
  needsBody(req: SellerRequest): boolean {
    if (req.path === WITHDRAW_PATH) return true;
    if (this.cfg.receipts && req.header("x-payment")) return true;
    const pay = decodeHeader<{ scheme?: string }>(req.header("x-payment") ?? "");
    return pay?.scheme === SCHEME_PREPAID;
  }

  /** Decides a request to a paid route: serve it (then call finish with the response status) or answer it. */
  async handle(req: SellerRequest, body: Uint8Array = new Uint8Array()): Promise<Decision> {
    const header = req.header("x-payment");
    if (!header) return this.paymentRequired(req, "payment_required", `this resource costs ${formatAeth(this.price)} AETH per request`);
    const pay = decodeHeader<{ scheme?: string; network?: string; payload?: unknown }>(header);
    if (!pay) return this.paymentRequired(req, "invalid_payment", "X-PAYMENT must be base64-encoded JSON");
    if (pay.network === this.network && pay.scheme === SCHEME_PREPAID && this.ledger) return this.servePrepaid(req, pay.payload, body);
    if (pay.scheme !== SCHEME_MEMO || pay.network !== this.network) {
      return this.paymentRequired(req, "unsupported_scheme", `this server accepts ${this.schemes().join(" or ")} on network "${this.network}"`);
    }
    const p = pay.payload as { invoice?: unknown; txHash?: unknown } | undefined;
    if (typeof p?.invoice !== "string" || typeof p?.txHash !== "string") return this.paymentRequired(req, "invalid_payment", "payload must be {invoice, txHash}");
    const invoice = p.invoice;
    const txHash = p.txHash.trim().toUpperCase();
    const inv = this.parseInvoice(invoice);
    if (!inv) return this.paymentRequired(req, "invalid_invoice", "invoice was not issued by this server");
    if (bytesToHex(inv.resource) !== bytesToHex(resourceKey(req.method, req.path))) {
      return this.paymentRequired(req, "invoice_for_other_resource", "this invoice was issued for a different request");
    }
    if (this.now() > inv.expiryMs) return this.paymentRequired(req, "invoice_expired", "this invoice can no longer be redeemed");
    if (!HASH.test(txHash)) return this.paymentRequired(req, "invalid_payment", "txHash must be a transaction hash");

    let t;
    try {
      t = await this.cfg.client.getTransaction(txHash);
    } catch (e) {
      console.error(`paywall: looking up ${txHash}: ${(e as Error).message}`);
      return { kind: "respond", status: 503, headers: { "Retry-After": "10", "Content-Type": "text/plain" }, body: "could not reach the chain to verify payment; retry with the same X-PAYMENT\n" };
    }
    if (t.status === "pending") {
      return this.paymentRequired(req, "payment_not_confirmed", `transaction ${txHash} is not in a block yet; retry shortly with the same X-PAYMENT`, invoice, "", { "Retry-After": "10" });
    }
    if (t.status === "failed") return this.paymentRequired(req, "payment_failed", `transaction ${txHash} failed on chain`);
    if (t.memo !== invoice) return this.paymentRequired(req, "memo_mismatch", "the transaction's memo must be exactly the invoice");
    const { paid, payer } = this.received(t.transfers);
    if (paid < inv.price) return this.paymentRequired(req, "insufficient_payment", `paid ${paid} uaeth to ${this.cfg.payTo}; the invoice is for ${inv.price} uaeth`);
    if (!this.redeem(invoice, inv.expiryMs)) return this.paymentRequired(req, "invoice_already_redeemed", "this invoice has already been used for a response");

    const settlement = encodeHeader({ success: true, transaction: txHash, network: this.network, payer });
    return {
      kind: "serve", payment: { payer, scheme: SCHEME_MEMO, txHash }, headers: { "X-PAYMENT-RESPONSE": settlement },
      receipt: this.receiptFor(req, body, SCHEME_MEMO, payer, txHash),
      // The server failed, not the buyer: the same payment may be used again.
      finish: (status) => { if (status >= 500) this.redeemed.delete(invoice); },
    };
  }

  private received(transfers: { from: string; to: string; amountUaeth: bigint }[]) {
    let paid = 0n;
    let payer = "";
    for (const t of transfers) {
      if (t.to !== this.cfg.payTo) continue;
      paid += t.amountUaeth;
      payer ||= t.from;
    }
    return { paid, payer };
  }

  private redeem(invoice: string, untilMs: number): boolean {
    const now = this.now();
    if (now - this.lastPrune > 60_000) {
      for (const [k, t] of this.redeemed) if (now > t) this.redeemed.delete(k);
      this.lastPrune = now;
    }
    if (this.redeemed.has(invoice)) return false;
    this.redeemed.set(invoice, untilMs);
    return true;
  }

  // --- invoices: HMAC-signed, so issuing one stores nothing (the Go paywall's format) ---

  private newInvoice(expiryMs: number, resource: Uint8Array): string {
    const b = new Uint8Array(1 + 8 + 8 + 12 + 16);
    b[0] = 1;
    u64(b, 1, BigInt(Math.floor(expiryMs / 1000)));
    u64(b, 9, this.price);
    b.set(randomBytes(12), 17);
    b.set(resource, 29);
    const out = new Uint8Array(b.length + 16);
    out.set(b);
    out.set(hmac(sha256, this.secret, b).subarray(0, 16), b.length);
    return INVOICE_PREFIX + base64urlnopad.encode(out);
  }

  private parseInvoice(s: string): { expiryMs: number; price: bigint; resource: Uint8Array } | undefined {
    if (!s.startsWith(INVOICE_PREFIX)) return undefined;
    let b: Uint8Array;
    try {
      b = base64urlnopad.decode(s.slice(INVOICE_PREFIX.length));
    } catch {
      return undefined;
    }
    if (b.length !== 61 || b[0] !== 1) return undefined;
    const mac = hmac(sha256, this.secret, b.subarray(0, 45)).subarray(0, 16);
    let diff = 0;
    for (let i = 0; i < 16; i++) diff |= mac[i] ^ b[45 + i];
    if (diff !== 0) return undefined;
    const dv = new DataView(b.buffer, b.byteOffset);
    return { expiryMs: Number(dv.getBigUint64(1)) * 1000, price: dv.getBigUint64(9), resource: b.slice(29, 45) };
  }

  /** A 402, offering invoice if set (a payment that may still confirm) or a fresh one. */
  paymentRequired(req: SellerRequest, code: string, message: string, invoice = "", account = "", headers: Record<string, string> = {}): Respond {
    let expiryMs = Math.floor((this.now() + this.ttlMs) / 1000) * 1000;
    if (invoice) {
      const inv = this.parseInvoice(invoice);
      if (inv) expiryMs = inv.expiryMs;
    } else {
      invoice = this.newInvoice(expiryMs, resourceKey(req.method, req.path));
    }
    const resource = `${req.https ? "https" : "http"}://${req.host}${req.path}`;
    const memo: PaymentRequirements & { mimeType: string } = {
      scheme: SCHEME_MEMO, network: this.network, maxAmountRequired: this.price.toString(), asset: "uaeth", payTo: this.cfg.payTo,
      resource, description: this.cfg.description ?? "", mimeType: this.cfg.mimeType ?? "", maxTimeoutSeconds: this.ttlMs / 1000,
      extra: { invoice, amountAeth: formatAeth(this.price), expiresAt: new Date(expiryMs).toISOString().replace(".000Z", "Z"), instructions: MEMO_INSTRUCTIONS } as PaymentRequirements["extra"],
    };
    const body: PaymentRequired = { x402Version: 1, error: code, message, accepts: [memo] };
    if (this.ledger) {
      const extra: Record<string, string> = {
        amountAeth: formatAeth(this.price), depositMemo: DEPOSIT_MEMO_PREFIX + "<address>", minDeposit: this.minDeposit.toString(), instructions: PREPAID_INSTRUCTIONS,
      };
      if (account) extra.balance = this.ledger.balance(account).toString();
      if (this.payout) extra.withdrawPath = WITHDRAW_PATH;
      body.accepts.push({ ...memo, scheme: SCHEME_PREPAID, extra: extra as PaymentRequirements["extra"] });
    }
    return json(402, body, headers);
  }

  // --- aether-prepaid ---

  private verify(req: SellerRequest, raw: unknown, body: Uint8Array): Signed | { code: string; message: string } {
    const pay = raw as PrepaidPayload;
    if (!pay || typeof pay !== "object" || typeof pay.account !== "string" || typeof pay.pubKey !== "string" || typeof pay.signature !== "string"
      || typeof pay.timestamp !== "number" || typeof pay.requestId !== "string" || typeof pay.maxPrice !== "string") {
      return { code: "invalid_payment", message: "payload must be a signed prepaid request" };
    }
    if (!isAddress(pay.account)) return { code: "invalid_signature", message: "invalid account address" };
    let pub: Uint8Array, sig: Uint8Array;
    try {
      pub = base64.decode(pay.pubKey);
    } catch {
      return { code: "invalid_signature", message: "pubKey must be a base64 ML-DSA-44 public key" };
    }
    if (pub.length !== 1312) return { code: "invalid_signature", message: "pubKey must be a base64 ML-DSA-44 public key" };
    if (addressOf(pub) !== pay.account) return { code: "invalid_signature", message: "pubKey does not belong to account" };
    try {
      sig = base64.decode(pay.signature);
    } catch {
      return { code: "invalid_signature", message: "signature must be base64" };
    }
    if (Math.abs(this.now() / 1000 - pay.timestamp) > SIGNED_WINDOW_S) {
      return { code: "stale_request", message: `sign each request fresh: timestamp must be within ${SIGNED_WINDOW_S / 60}m0s of the server's clock` };
    }
    if (!pay.requestId || pay.requestId.length > 128) return { code: "invalid_payment", message: "requestId is required (1-128 characters)" };
    let maxPrice = 0n;
    if (pay.maxPrice !== "0") {
      try {
        maxPrice = parseUaeth(pay.maxPrice);
      } catch (e) {
        return { code: "invalid_payment", message: "maxPrice: " + (e as Error).message };
      }
    }
    if (body.length > MAX_SIGNED_BODY) return { code: "request_too_large", message: `signed requests' bodies are limited to ${MAX_SIGNED_BODY} bytes` };
    const msg = signingMessage({
      network: this.network, payTo: this.cfg.payTo, host: req.host, method: req.method, path: req.path, body,
      maxPrice, timestamp: pay.timestamp, requestId: pay.requestId, depositTx: pay.depositTx ?? "",
    });
    if (!Key.verify(pub, msg, sig)) return { code: "invalid_signature", message: "signature does not match this request" };
    return { pay, maxPrice };
  }

  private async servePrepaid(req: SellerRequest, raw: unknown, body: Uint8Array): Promise<Decision> {
    const ledger = this.ledger!;
    const v = this.verify(req, raw, body);
    if ("code" in v) return this.paymentRequired(req, v.code, v.message);
    const { pay, maxPrice } = v;
    const refuse = (code: string, msg: string, headers: Record<string, string> = {}) => this.paymentRequired(req, code, msg, "", pay.account, headers);
    if (maxPrice < this.price) return refuse("price_above_signed_max", `the price is ${this.price} uaeth; the request allows at most ${maxPrice}`);

    if (pay.depositTx) {
      const refused = await this.creditDeposit(req, pay.depositTx.trim().toUpperCase(), pay.account);
      if (refused) return refused;
    }
    const c = ledger.charge(pay.account, pay.requestId, this.price, this.now() + REQUEST_ID_RETENTION_MS);
    if (!c.fresh) return refuse("invoice_already_redeemed", "this requestId was already charged and served");
    if (!c.ok) {
      return refuse("insufficient_balance", `balance ${c.balance} uaeth is less than the price ${this.price} uaeth: deposit to ${this.cfg.payTo} with memo ${DEPOSIT_MEMO_PREFIX}${pay.account}`);
    }
    const settlement = encodeHeader({ success: true, network: this.network, payer: pay.account, balance: c.balance.toString() });
    return {
      kind: "serve", payment: { payer: pay.account, scheme: SCHEME_PREPAID, balanceUaeth: c.balance }, headers: { "X-PAYMENT-RESPONSE": settlement },
      receipt: this.receiptFor(req, body, SCHEME_PREPAID, pay.account, pay.requestId),
      finish: (status) => {
        if (status < 500) return;
        try {
          ledger.refund(pay.account, pay.requestId, this.price);
        } catch (e) {
          console.error(`paywall: refunding ${pay.account} for a failed request: ${(e as Error).message}`);
        }
      },
    };
  }

  /** Credits depositTx to the account its memo names; a Decision if it can't be used. */
  private async creditDeposit(req: SellerRequest, txHash: string, account: string): Promise<Decision | undefined> {
    const refuse = (code: string, msg: string, headers: Record<string, string> = {}) => this.paymentRequired(req, code, msg, "", account, headers);
    if (!HASH.test(txHash)) return refuse("invalid_deposit", "depositTx must be a transaction hash");
    let t;
    try {
      t = await this.cfg.client.getTransaction(txHash);
    } catch (e) {
      console.error(`paywall: looking up deposit ${txHash}: ${(e as Error).message}`);
      return { kind: "respond", status: 503, headers: { "Retry-After": "10", "Content-Type": "text/plain" }, body: "could not reach the chain to verify the deposit; retry\n" };
    }
    if (t.status === "pending") return refuse("payment_not_confirmed", `deposit ${txHash} is not in a block yet; retry shortly`, { "Retry-After": "10" });
    if (t.status === "failed") return refuse("payment_failed", `deposit ${txHash} failed on chain`);
    const memo = t.memo ?? "";
    if (!memo.startsWith(DEPOSIT_MEMO_PREFIX)) return refuse("invalid_deposit", `a deposit's memo must be ${DEPOSIT_MEMO_PREFIX}<address>`);
    const beneficiary = memo.slice(DEPOSIT_MEMO_PREFIX.length);
    if (!isAddress(beneficiary)) return refuse("invalid_deposit", "the deposit's memo names an invalid address");
    const { paid } = this.received(t.transfers);
    if (paid < this.minDeposit) return refuse("insufficient_payment", `deposits must be at least ${this.minDeposit} uaeth to ${this.cfg.payTo}`);
    // Credit whoever the memo names: presenting someone else's deposit only credits them.
    this.ledger!.credit(txHash, beneficiary, paid);
    return undefined;
  }

  // --- withdrawals ---

  /** Answers a POST to WITHDRAW_PATH. */
  async withdraw(req: SellerRequest, body: Uint8Array): Promise<Respond> {
    const fail = (status: number, error: string, message: string, extra: Record<string, unknown> = {}) => json(status, { x402Version: 1, ...extra, error, message });
    if (!this.ledger || !this.payout) return fail(501, "withdrawals_unavailable", "this service doesn't pay back prepaid balances; ask its operator");
    if (req.method !== "POST") return { ...fail(405, "invalid_payment", "POST a signed withdrawal request"), headers: { "Content-Type": "application/json", Allow: "POST" } };
    const pay = decodeHeader<{ scheme?: string; network?: string; payload?: unknown }>(req.header("x-payment") ?? "");
    if (!pay) return fail(400, "invalid_payment", "sign the withdrawal like a prepaid request: X-PAYMENT must be base64-encoded JSON");
    if (pay.scheme !== SCHEME_PREPAID || pay.network !== this.network) return fail(400, "unsupported_scheme", `withdrawals are signed with ${SCHEME_PREPAID} on network "${this.network}"`);
    const v = this.verify(req, pay.payload, body);
    if ("code" in v) return fail(v.code === "invalid_signature" || v.code === "stale_request" ? 403 : 400, v.code, v.message);
    let amount: bigint | undefined;
    const text = new TextDecoder().decode(body).trim();
    if (text) {
      let parsed: { amount?: unknown };
      try {
        parsed = JSON.parse(text) as { amount?: unknown };
      } catch {
        return fail(400, "invalid_payment", 'the body must be {"amount":"all"} or {"amount":"<uaeth>"}');
      }
      const a = typeof parsed?.amount === "string" ? parsed.amount.trim() : parsed?.amount === undefined ? "" : null;
      if (a === null) return fail(400, "invalid_payment", 'amount must be "all" or a positive whole number of uaeth');
      if (a && a !== "all") {
        try {
          amount = parseUaeth(a);
        } catch {
          return fail(400, "invalid_payment", 'amount must be "all" or a positive whole number of uaeth');
        }
      }
    }
    // One at a time: payouts come from one account, and an ID must never be worked on twice at once.
    const run = this.withdrawQueue.then(() => this.doWithdraw(v.pay.account, v.pay.requestId, amount));
    this.withdrawQueue = run.catch(() => undefined);
    return run;
  }

  private async doWithdraw(account: string, id: string, amount: bigint | undefined): Promise<Respond> {
    const ledger = this.ledger!;
    const payout = this.payout!;
    const respond = (status: number, w: Withdrawal, message: string) =>
      json(status, {
        x402Version: 1, withdrawalId: w.id, account, amount: w.amount, amountAeth: formatAeth(BigInt(w.amount)), status: w.status,
        ...(w.txHash ? { txHash: w.txHash } : {}), balance: ledger.balance(account).toString(), message,
      });
    const fail = (status: number, error: string, message: string) =>
      json(status, { x402Version: 1, withdrawalId: id, account, balance: ledger.balance(account).toString(), error, message });

    let r;
    try {
      r = ledger.reserveWithdrawal(account, id, amount, this.minDeposit, this.now());
    } catch (e) {
      if (e instanceof LedgerError && e.code === "insufficient") return fail(409, "insufficient_balance", "the balance doesn't cover that withdrawal");
      if (e instanceof LedgerError) return fail(409, "below_minimum_withdrawal", `withdraw at least ${this.minDeposit} uaeth, or the whole balance`);
      console.error(`paywall: reserving withdrawal ${account}/${id}: ${(e as Error).message}`);
      return fail(500, "payout_unavailable", "failed to record the withdrawal; nothing was taken");
    }
    let w = r.withdrawal;
    const requested = amount === undefined ? "all" : amount.toString();
    if (!r.fresh && w.requested !== requested) return fail(409, "withdrawal_id_reused", `withdrawal "${id}" was for ${w.requested}; use a new ID for a new withdrawal`);

    for (let attempt = 0; attempt < 2; attempt++) {
      if (w.status === "confirmed") return respond(200, w, "paid back");
      if (w.status === "reserved") {
        let signed;
        try {
          signed = await payout.sign(account, BigInt(w.amount), "prepaid-withdrawal:" + id);
        } catch (e) {
          console.error(`paywall: signing withdrawal ${account}/${id}: ${(e as Error).message}`);
          return respond(503, w, "the payout couldn't be signed right now; the amount is set aside -- ask again with the same withdrawal ID");
        }
        w = { ...w, status: "pending", txBytes: base64.encode(signed.txBytes), sequence: Number(signed.sequence), txHash: signed.hash, sequenceSpentAt: undefined };
        // Saved before it's broadcast: from here it's only re-sent.
        try {
          ledger.saveWithdrawal(w);
        } catch (e) {
          console.error(`paywall: saving withdrawal ${account}/${id}: ${(e as Error).message}`);
          return respond(503, { ...r.withdrawal, status: "reserved" }, "failed to record the payout; ask again with the same withdrawal ID");
        }
      }
      let state;
      try {
        state = await payout.submit(base64.decode(w.txBytes!), BigInt(w.sequence ?? 0));
      } catch (e) {
        console.error(`paywall: submitting withdrawal ${account}/${id}: ${(e as Error).message}`);
        return respond(202, w, "the payout is signed but the chain couldn't be reached to confirm it was sent; ask again with the same withdrawal ID");
      }
      switch (state.status) {
        case "confirmed":
          w = { ...w, status: "confirmed", txBytes: undefined };
          try {
            ledger.saveWithdrawal(w);
          } catch (e) {
            console.error(`paywall: saving withdrawal ${account}/${id}: ${(e as Error).message}`);
          }
          return respond(200, w, "paid back");
        case "pending":
          return respond(200, w, "sent; in a block within about a minute");
        case "failed":
          try {
            ledger.cancelWithdrawal(account, id);
          } catch (e) {
            console.error(`paywall: cancelling withdrawal ${account}/${id}: ${(e as Error).message}`);
            return respond(503, w, "the payout failed; ask again with the same withdrawal ID");
          }
          console.error(`paywall: withdrawal ${account}/${id} failed: ${state.log}`);
          return fail(503, "payout_failed", "the service couldn't pay this out right now, so nothing was paid and the balance is back; try again later");
        case "sequence_spent": {
          const now = this.now();
          if (!w.sequenceSpentAt || w.sequenceSpentAt.startsWith("0001-")) {
            w = { ...w, sequenceSpentAt: new Date(now).toISOString() };
            try {
              ledger.saveWithdrawal(w);
            } catch (e) {
              console.error(`paywall: saving withdrawal ${account}/${id}: ${(e as Error).message}`);
            }
          }
          if (now - Date.parse(w.sequenceSpentAt!) < SEQUENCE_SPENT_GRACE_MS) return respond(200, w, "sent; not visible on chain yet -- ask again with the same withdrawal ID");
          w = { ...w, status: "reserved" }; // long enough: these bytes can never land. Sign afresh.
        }
      }
    }
    return respond(200, w, "sent");
  }

  /**
   * Middleware for Express, Connect or plain node:http. Mount it at the
   * root, before body parsers: a signed request's body is read here and
   * left on req.rawBody (and req.body). Who paid is on req.aether.
   */
  middleware(opts: { free?: string[] } = {}) {
    return (req: IncomingMessage, res: ServerResponse, next: (err?: unknown) => void) => {
      this.dispatch(req as NodeRequest, res, next, opts.free ?? []).catch(next);
    };
  }

  private async dispatch(req: NodeRequest, res: ServerResponse, next: (err?: unknown) => void, free: string[]) {
    const raw = (req.originalUrl ?? req.url ?? "/").split("?")[0] || "/";
    let path: string;
    try {
      path = decodeURIComponent(raw);
    } catch {
      return write(res, { kind: "respond", status: 400, headers: { "Content-Type": "text/plain" }, body: "invalid path\n" });
    }
    const sreq: SellerRequest = {
      method: (req.method ?? "GET").toUpperCase(), host: req.headers.host ?? "", path,
      https: req.headers["x-forwarded-proto"] === "https" || Boolean((req.socket as { encrypted?: boolean }).encrypted),
      header: (name) => {
        const v = req.headers[name.toLowerCase()];
        return Array.isArray(v) ? v[0] : v;
      },
    };
    if (path === MANIFEST_PATH) {
      if (sreq.method !== "GET" && sreq.method !== "HEAD") return write(res, { kind: "respond", status: 405, headers: { Allow: "GET, HEAD", "Content-Type": "text/plain" }, body: "use GET\n" });
      return write(res, json(200, this.manifest(), { "Cache-Control": "max-age=300" }));
    }
    if (path !== WITHDRAW_PATH && free.some((p) => path.startsWith(p))) return next();

    let body: Uint8Array = new Uint8Array();
    if (this.needsBody(sreq)) {
      const got = await readBody(req, MAX_SIGNED_BODY + 1);
      if (!got) return write(res, { kind: "respond", status: 500, headers: { "Content-Type": "text/plain" }, body: "the request body was consumed before the paywall: mount it before body parsers, or keep the raw body in req.rawBody\n" });
      if (got.length > MAX_SIGNED_BODY && this.cfg.receipts) {
        return write(res, { kind: "respond", status: 413, headers: { "Content-Type": "text/plain" }, body: `paid requests' bodies are limited to ${MAX_SIGNED_BODY} bytes\n` });
      }
      body = got;
      keepBody(req, got);
    }
    if (path === WITHDRAW_PATH) return write(res, await this.withdraw(sreq, body));
    const d = await this.handle(sreq, body);
    if (d.kind === "respond") return write(res, d);
    for (const [k, v] of Object.entries(d.headers)) res.setHeader(k, v);
    req.aether = d.payment;
    res.on("finish", () => d.finish(res.statusCode));
    if (d.receipt) holdForReceipt(res, d.receipt);
    next();
  }
}

/**
 * Holds a paid response until it ends, so its receipt (a header) can cover
 * the body. A response bigger than MAX_RECEIPT_BODY, or one whose headers
 * are flushed early, streams instead, with a receipt that has no response hash.
 */
function holdForReceipt(res: ServerResponse, receipt: (status: number, body?: Uint8Array) => string) {
  const writeHead = res.writeHead.bind(res) as (code: number) => ServerResponse;
  const write = res.write.bind(res) as (chunk: Uint8Array) => boolean;
  const end = res.end.bind(res) as (chunk?: Uint8Array, cb?: () => void) => ServerResponse;
  const flushHeaders = res.flushHeaders.bind(res);
  let chunks: Buffer[] = [];
  let size = 0;
  let streaming = false;
  const setReceipt = (h: string) => {
    if (h) res.setHeader(RECEIPT_HEADER, h);
  };
  const toBuf = (chunk: unknown, enc?: unknown): Buffer | undefined =>
    chunk == null || typeof chunk === "function" ? undefined : typeof chunk === "string" ? Buffer.from(chunk, typeof enc === "string" ? (enc as BufferEncoding) : "utf8") : Buffer.from(chunk as Uint8Array);
  const cbOf = (...args: unknown[]) => args.find((a) => typeof a === "function") as (() => void) | undefined;
  const stream = () => {
    if (streaming) return;
    streaming = true;
    if (!res.headersSent) {
      setReceipt(receipt(res.statusCode));
      writeHead(res.statusCode);
    }
    for (const c of chunks) write(c);
    chunks = [];
  };
  res.writeHead = ((code: number, ...rest: unknown[]) => {
    res.statusCode = code;
    for (const r of rest) {
      if (typeof r === "string") res.statusMessage = r;
      else if (Array.isArray(r)) for (let i = 0; i + 1 < r.length; i += 2) res.setHeader(String(r[i]), r[i + 1] as string);
      else if (r && typeof r === "object") for (const [k, v] of Object.entries(r)) if (v !== undefined) res.setHeader(k, v as string);
    }
    return res;
  }) as typeof res.writeHead;
  res.flushHeaders = () => {
    stream();
    flushHeaders();
  };
  res.write = ((chunk: unknown, enc?: unknown, cb?: unknown) => {
    const b = toBuf(chunk, enc);
    if (streaming) return (write as unknown as (...a: unknown[]) => boolean)(chunk, enc, cb);
    if (b) {
      chunks.push(b);
      size += b.length;
    }
    if (size > MAX_RECEIPT_BODY) stream();
    const done = cbOf(enc, cb);
    if (done) process.nextTick(done);
    return true;
  }) as typeof res.write;
  res.end = ((chunk?: unknown, enc?: unknown, cb?: unknown) => {
    const b = toBuf(chunk, enc);
    const done = cbOf(chunk, enc, cb);
    if (!streaming && b && size + b.length > MAX_RECEIPT_BODY) stream();
    if (streaming) return (end as unknown as (...a: unknown[]) => ServerResponse)(chunk, enc, cb);
    if (b) chunks.push(b);
    const body = Buffer.concat(chunks);
    if (!res.headersSent) {
      setReceipt(receipt(res.statusCode, new Uint8Array(body)));
      writeHead(res.statusCode);
    }
    return end(body, done);
  }) as typeof res.end;
}

type NodeRequest = IncomingMessage & { originalUrl?: string; rawBody?: Uint8Array; body?: unknown; _body?: boolean; aether?: Payment };

function write(res: ServerResponse, d: Respond) {
  res.writeHead(d.status, d.headers);
  res.end(d.body);
}

async function readBody(req: NodeRequest, limit: number): Promise<Uint8Array | undefined> {
  if (req.rawBody instanceof Uint8Array) return req.rawBody;
  if (req.readableEnded) return req.headers["content-length"] && req.headers["content-length"] !== "0" ? undefined : new Uint8Array();
  const chunks: Buffer[] = [];
  let size = 0;
  for await (const chunk of req) {
    const b = chunk as Buffer;
    size += b.length;
    if (size > limit) break; // over the signed-body limit: refused on size, so the rest isn't needed
    chunks.push(b);
  }
  return new Uint8Array(Buffer.concat(chunks));
}

/** Leaves the body read here for the route: req.rawBody always, req.body parsed like a body parser would. */
function keepBody(req: NodeRequest, body: Uint8Array) {
  req.rawBody = body;
  if (req.body !== undefined || req._body) return;
  const type = String(req.headers["content-type"] ?? "");
  const text = new TextDecoder().decode(body);
  if (/json/i.test(type)) {
    try {
      req.body = text ? JSON.parse(text) : {};
    } catch {
      req.body = text;
    }
  } else if (/^text\//i.test(type) || /x-www-form-urlencoded/i.test(type)) {
    req.body = /x-www-form-urlencoded/i.test(type) ? Object.fromEntries(new URLSearchParams(text)) : text;
  } else {
    req.body = Buffer.from(body);
  }
  req._body = true; // body-parser: already parsed
}

function resourceKey(method: string, path: string): Uint8Array {
  return sha256(new TextEncoder().encode(`${method} ${path}`)).slice(0, 16);
}

/** Pays withdrawals from a key's account: signs first (so the payout is saved before it's sent), then (re)submits. */
export class KeyPayout {
  private next = 0n;

  constructor(private readonly client: AetherClient, private readonly key: Key) {}

  get address() {
    return this.key.address;
  }

  async sign(to: string, amountUaeth: bigint, memo: string): Promise<{ txBytes: Uint8Array; sequence: bigint; hash: string }> {
    const info = await this.client.accountInfo(this.key.address);
    if (!info) throw new Error(`payout account ${this.key.address} doesn't exist on chain yet: fund it`);
    const sequence = info.sequence < this.next ? this.next : info.sequence; // an earlier payout may still be in the mempool
    const s = buildSend(this.key, { chainId: this.client.chainId, accountNumber: info.accountNumber, sequence, to, amountUaeth, memo });
    this.next = sequence + 1n;
    return { txBytes: s.txBytes, sequence, hash: s.hash };
  }

  async submit(txBytes: Uint8Array, sequence: bigint): Promise<{ status: "pending" | "confirmed" | "failed" | "sequence_spent"; log?: string }> {
    const hash = bytesToHex(sha256(txBytes)).toUpperCase();
    const known = async () => {
      const t = await this.client.getTransaction(hash);
      return t.status === "pending" ? undefined : { status: t.status, log: t.log };
    };
    const before = await known();
    if (before) return before;
    let r;
    try {
      r = await this.client.rpc.broadcastSync(txBytes);
    } catch (e) {
      if (e instanceof RpcError && /already exists in cache/i.test(e.message)) return { status: "pending" };
      throw e;
    }
    if (r.code === 0 || (r.codespace === "sdk" && r.code === 19)) return { status: "pending" };
    const after = await known(); // rejected -- unless it has landed since
    if (after) return after;
    if (sequence < this.next) this.next = 0n;
    return { status: r.codespace === "sdk" && r.code === 32 ? "sequence_spent" : "failed", log: r.log };
  }
}
