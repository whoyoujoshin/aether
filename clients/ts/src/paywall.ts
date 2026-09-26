import { sha256 } from "@noble/hashes/sha2.js";
import { bytesToHex } from "@noble/hashes/utils.js";
import { base64 } from "@scure/base";
import { AetherClient } from "./client.js";
import { Key } from "./keys.js";
import { parseAmount, parseUaeth } from "./amount.js";
import { RECEIPT_HEADER, checkReceipt, decodeReceipt, type Receipt, type ReceiptExpectation } from "./receipt.js";

// Buying from paid APIs (package paywall, x402 wire format):
//  - aether-memo: pay the quoted invoice (memo = invoice), wait for the
//    block, repeat the request with proof. Works for any payer.
//  - aether-prepaid (bots): deposit once, then sign each request with the
//    account's key; the seller deducts the price instantly.

export const SCHEME_MEMO = "aether-memo";
export const SCHEME_PREPAID = "aether-prepaid";
export const DEPOSIT_MEMO_PREFIX = "prepaid:";
const SIGNING_DOMAIN = "aether-prepaid-request/v1\n";

export interface PaymentRequirements {
  scheme: string;
  network: string;
  maxAmountRequired: string;
  asset: string;
  payTo: string;
  resource: string;
  description: string;
  maxTimeoutSeconds: number;
  extra: { invoice?: string; amountAeth: string; expiresAt?: string; depositMemo?: string; minDeposit?: string; balance?: string; withdrawPath?: string };
}

export interface PaymentRequired {
  x402Version: number;
  error: string;
  message?: string;
  accepts: PaymentRequirements[];
}

export interface RequestFields {
  network: string;
  payTo: string;
  host: string;
  method: string;
  path: string;
  body: Uint8Array;
  maxPrice: bigint;
  timestamp: number;
  requestId: string;
  depositTx?: string;
}

/** The exact bytes an aether-prepaid request signs (matches the Go seller). */
export function signingMessage(f: RequestFields): Uint8Array {
  const lines = [f.network, f.payTo, f.host, f.method, f.path, bytesToHex(sha256(f.body)), f.maxPrice.toString(), String(f.timestamp), f.requestId, f.depositTx ?? ""];
  return new TextEncoder().encode(SIGNING_DOMAIN + lines.map((l) => l + "\n").join(""));
}

const encodeHeader = (v: unknown) => base64.encode(new TextEncoder().encode(JSON.stringify(v)));

export function memoPaymentHeader(network: string, invoice: string, txHash: string): string {
  return encodeHeader({ x402Version: 1, scheme: SCHEME_MEMO, network, payload: { invoice, txHash } });
}

export function prepaidPaymentHeader(key: Key, f: RequestFields): string {
  const sig = key.sign(signingMessage(f));
  return encodeHeader({
    x402Version: 1, scheme: SCHEME_PREPAID, network: f.network,
    payload: {
      account: key.address, pubKey: base64.encode(key.publicKey), timestamp: f.timestamp, requestId: f.requestId,
      maxPrice: f.maxPrice.toString(), depositTx: f.depositTx || undefined, signature: base64.encode(sig),
    },
  });
}

export class PaymentError extends Error {
  constructor(readonly code: string, message: string, readonly txHash?: string) {
    super(message);
  }
}

export interface FetchPaidOptions {
  /** Most you'll pay per request, with unit ("0.05 AETH"). A higher quote is refused unpaid. */
  maxAmount: string;
  /** Bots making many requests: if the seller offers prepaid, deposit this much when the balance runs out. */
  prepay?: string;
  method?: string;
  body?: string | Uint8Array;
  headers?: Record<string, string>;
  /** Prepaid: the seller charges each requestId once, so reuse it when retrying the same request. */
  requestId?: string;
  confirmTimeoutMs?: number;
}

export interface FetchPaidResult {
  status: "ok" | "paid" | "payment_pending";
  response?: Response;
  scheme?: string;
  txHash?: string; // memo payment or prepaid deposit
  invoice?: string;
  amountUaeth?: bigint;
  balanceUaeth?: bigint; // prepaid: left with the seller
  /** The seller's signed receipt, if it gives them, and whether it matches exactly what was sent and received. */
  receipt?: { receipt?: Receipt; verified: boolean; problem?: string };
}

/** Checks a paid response's receipt against the purchase (reads a clone of the body). */
async function withReceipt(r: FetchPaidResult, want: Omit<ReceiptExpectation, "status" | "responseBody">): Promise<FetchPaidResult> {
  const header = r.response?.headers.get(RECEIPT_HEADER);
  if (!r.response || !header) return r;
  const receipt = decodeReceipt(header);
  if (!receipt) return { ...r, receipt: { verified: false, problem: "the receipt is unreadable" } };
  const responseBody = new Uint8Array(await r.response.clone().arrayBuffer());
  const problem = checkReceipt(receipt, { ...want, status: r.response.status, responseBody });
  return { ...r, receipt: { receipt, verified: !problem, problem } };
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/**
 * Requests url and, if it answers 402 with an Aether payment option, pays
 * and returns the response. Response bodies come from the seller: untrusted.
 * If it returns payment_pending (or throws with a txHash), the payment was
 * made: finish with presentPayment rather than paying again.
 */
export async function fetchPaid(client: AetherClient, key: Key, url: string, opts: FetchPaidOptions): Promise<FetchPaidResult> {
  const maxAmount = parseAmount(opts.maxAmount);
  const method = (opts.method ?? "GET").toUpperCase();
  const body = typeof opts.body === "string" ? new TextEncoder().encode(opts.body) : opts.body ?? new Uint8Array();
  const send = (payment?: string) =>
    fetch(url, {
      method, redirect: "manual",
      headers: { ...(opts.headers ?? {}), ...(payment ? { "X-PAYMENT": payment } : {}), ...(body.length && !opts.headers?.["Content-Type"] ? { "Content-Type": "application/json" } : {}) },
      body: body.length ? (body as unknown as BodyInit) : undefined,
    });

  const first = await send();
  if (first.status !== 402) return { status: "ok", response: first };
  const quote = (await first.json()) as PaymentRequired;
  const pick = (scheme: string) => quote.accepts?.find((a) => a.scheme === scheme && a.network === client.chainId && a.asset === "uaeth");

  const prepaid = pick(SCHEME_PREPAID);
  if (opts.prepay && prepaid) return fetchPrepaid(client, key, url, method, body, prepaid, maxAmount, parseAmount(opts.prepay), send, opts);

  const memo = pick(SCHEME_MEMO);
  if (!memo?.extra.invoice) throw new PaymentError("PAYMENT_UNSUPPORTED", `the server doesn't accept ${SCHEME_MEMO} payments on ${client.chainId}`);
  const price = parseUaeth(memo.maxAmountRequired);
  if (price > maxAmount) throw new PaymentError("PRICE_EXCEEDS_MAX", `the server asks ${price} uaeth; maxAmount is ${maxAmount}. Nothing was paid`);
  const sent = await client.send(key, memo.payTo, `${price}uaeth`, { memo: memo.extra.invoice });
  if (sent.status === "failed") throw new PaymentError("TX_REJECTED", `the payment was rejected: ${sent.log}`, sent.hash);
  const conf = await client.waitForTransaction(sent.hash, opts.confirmTimeoutMs ?? 150_000);
  if (conf.status === "failed") throw new PaymentError("TX_FAILED", `the payment failed on chain: ${conf.log}`, sent.hash);
  const base = { scheme: SCHEME_MEMO, txHash: sent.hash, invoice: memo.extra.invoice, amountUaeth: price };
  if (conf.status === "pending") return { status: "payment_pending", ...base };
  const u = new URL(url);
  return withReceipt({ ...(await presentPayment(client, url, memo.extra.invoice, sent.hash, send)), ...base }, {
    network: client.chainId, payTo: memo.payTo, payer: key.address, scheme: SCHEME_MEMO, payment: sent.hash, amount: price,
    method, host: u.host, path: decodeURIComponent(u.pathname || "/"), requestBody: body,
  });
}

/** Repeats a request with proof of an aether-memo payment (e.g. after payment_pending). */
export async function presentPayment(client: AetherClient, url: string, invoice: string, txHash: string, send?: (p: string) => Promise<Response>): Promise<FetchPaidResult> {
  const doSend = send ?? ((p: string) => fetch(url, { headers: { "X-PAYMENT": p }, redirect: "manual" }));
  const proof = memoPaymentHeader(client.chainId, invoice, txHash);
  for (let attempt = 0; ; attempt++) {
    const resp = await doSend(proof);
    if (resp.status !== 402) return { status: "paid", response: resp };
    const pr = (await resp.json()) as PaymentRequired;
    if (pr.error === "payment_not_confirmed" && attempt < 4) {
      await sleep(3000);
      continue;
    }
    throw new PaymentError(pr.error === "invoice_already_redeemed" ? "PAYMENT_ALREADY_REDEEMED" : "PAYMENT_REJECTED", `the server refused the payment (${pr.error}): ${pr.message ?? ""}`, txHash);
  }
}

async function fetchPrepaid(
  client: AetherClient, key: Key, url: string, method: string, body: Uint8Array, req: PaymentRequirements,
  maxAmount: bigint, prepay: bigint, send: (p?: string) => Promise<Response>, opts: FetchPaidOptions,
): Promise<FetchPaidResult> {
  const price = parseUaeth(req.maxAmountRequired);
  if (price > maxAmount) throw new PaymentError("PRICE_EXCEEDS_MAX", `the server asks ${price} uaeth per request; maxAmount is ${maxAmount}. Nothing was paid`);
  if (req.extra.minDeposit && prepay < parseUaeth(req.extra.minDeposit)) throw new PaymentError("PAYMENT_UNSUPPORTED", `the minimum deposit is ${req.extra.minDeposit} uaeth`);
  const u = new URL(url);
  const requestId = opts.requestId ?? bytesToHex(crypto.getRandomValues(new Uint8Array(16)));
  const attempt = (depositTx?: string) =>
    send(prepaidPaymentHeader(key, {
      network: client.chainId, payTo: req.payTo, host: u.host, method, path: decodeURIComponent(u.pathname || "/"),
      body, maxPrice: price, timestamp: Math.floor(Date.now() / 1000), requestId, depositTx,
    }));
  const done = (resp: Response, depositTx?: string): Promise<FetchPaidResult> => {
    const s = resp.headers.get("X-PAYMENT-RESPONSE");
    let balance: bigint | undefined;
    if (s) balance = BigInt((JSON.parse(new TextDecoder().decode(base64.decode(s))) as { balance?: string }).balance ?? "0");
    return withReceipt({ status: "paid", response: resp, scheme: SCHEME_PREPAID, amountUaeth: price, balanceUaeth: balance, txHash: depositTx }, {
      network: client.chainId, payTo: req.payTo, payer: key.address, scheme: SCHEME_PREPAID, payment: requestId, amount: price,
      method, host: u.host, path: decodeURIComponent(u.pathname || "/"), requestBody: body,
    });
  };

  let resp = await attempt();
  if (resp.status !== 402) return done(resp);
  let pr = (await resp.json()) as PaymentRequired;
  if (pr.error !== "insufficient_balance") throw new PaymentError(pr.error === "invoice_already_redeemed" ? "PAYMENT_ALREADY_REDEEMED" : "PAYMENT_REJECTED", `${pr.error}: ${pr.message ?? ""}`);

  const dep = await client.send(key, req.payTo, `${prepay}uaeth`, { memo: DEPOSIT_MEMO_PREFIX + key.address });
  if (dep.status === "failed") throw new PaymentError("TX_REJECTED", `the deposit was rejected: ${dep.log}`, dep.hash);
  const conf = await client.waitForTransaction(dep.hash, opts.confirmTimeoutMs ?? 150_000);
  if (conf.status === "failed") throw new PaymentError("TX_FAILED", `the deposit failed on chain: ${conf.log}`, dep.hash);
  if (conf.status === "pending") return { status: "payment_pending", scheme: SCHEME_PREPAID, txHash: dep.hash, amountUaeth: prepay };
  for (let i = 0; ; i++) {
    resp = await attempt(dep.hash);
    if (resp.status !== 402) return done(resp, dep.hash);
    pr = (await resp.json()) as PaymentRequired;
    if (pr.error === "payment_not_confirmed" && i < 4) {
      await sleep(3000);
      continue;
    }
    throw new PaymentError("PAYMENT_REJECTED", `${pr.error}: ${pr.message ?? ""}`, dep.hash);
  }
}
