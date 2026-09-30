import { sha256 } from "@noble/hashes/sha2.js";
import { bytesToHex } from "@noble/hashes/utils.js";
import { base64 } from "@scure/base";
import { AetherClient } from "./client.js";
import { grantSendMsg } from "./tx.js";
import { Key } from "./keys.js";
import { DENOM, formatAmount, parseUaeth, type Asset } from "./amount.js";
import { RECEIPT_HEADER, checkReceipt, decodeReceipt, type Receipt, type ReceiptExpectation } from "./receipt.js";

// Buying from paid APIs (package paywall, x402 wire format):
//  - aether-memo: pay the quoted invoice (memo = invoice), wait for the
//    block, repeat the request with proof. Works for any payer.
//  - aether-prepaid (bots): deposit once, then sign each request with the
//    account's key; the seller deducts the price instantly.
//  - aether-pull (bots): grant the seller a capped, expiring allowance on
//    chain, then sign each request; the seller collects what's owed from
//    your account in batches, so nothing is deposited with it.

export const SCHEME_MEMO = "aether-memo";
export const SCHEME_PREPAID = "aether-prepaid";
export const SCHEME_PULL = "aether-pull";
export const DEPOSIT_MEMO_PREFIX = "prepaid:";
const SIGNING_DOMAIN = "aether-prepaid-request/v1\n";
const PULL_SIGNING_DOMAIN = "aether-pull-request/v1\n";
/** How long an allowance fetchPaid grants lasts. */
export const PULL_GRANT_SECONDS = 7 * 86_400;

export interface PaymentRequirements {
  scheme: string;
  network: string;
  maxAmountRequired: string;
  asset: string;
  payTo: string;
  resource: string;
  description: string;
  maxTimeoutSeconds: number;
  extra: {
    /** symbol and amount state the price in the asset's own unit ("USDC", "0.05"); amountAeth is set only for an AETH price. */
    symbol?: string; amount?: string;
    invoice?: string; amountAeth?: string; expiresAt?: string; depositMemo?: string; minDeposit?: string; balance?: string; withdrawPath?: string;
    /** aether-pull: grant this address the allowance; credit is the most owed before collection; owed is what you owe now. */
    grantee?: string; credit?: string; owed?: string;
  };
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
  return message(SIGNING_DOMAIN, f);
}

/** The exact bytes an aether-pull request signs: the same fields, no deposit, under its own domain. */
export function pullSigningMessage(f: RequestFields): Uint8Array {
  return message(PULL_SIGNING_DOMAIN, { ...f, depositTx: "" });
}

function message(domain: string, f: RequestFields): Uint8Array {
  const lines = [f.network, f.payTo, f.host, f.method, f.path, bytesToHex(sha256(f.body)), f.maxPrice.toString(), String(f.timestamp), f.requestId, f.depositTx ?? ""];
  return new TextEncoder().encode(domain + lines.map((l) => l + "\n").join(""));
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

export function pullPaymentHeader(key: Key, f: RequestFields): string {
  const sig = key.sign(pullSigningMessage(f));
  return encodeHeader({
    x402Version: 1, scheme: SCHEME_PULL, network: f.network,
    payload: {
      account: key.address, pubKey: base64.encode(key.publicKey), timestamp: f.timestamp, requestId: f.requestId,
      maxPrice: f.maxPrice.toString(), signature: base64.encode(sig),
    },
  });
}

export class PaymentError extends Error {
  constructor(readonly code: string, message: string, readonly txHash?: string) {
    super(message);
  }
}

export interface FetchPaidOptions {
  /**
   * Most you'll pay per request, with unit, in the asset the service
   * charges: "0.05 AETH", or "0.10 USDC" with a client that has a
   * usdcChannel. A higher quote, or one in another asset, is refused unpaid.
   */
  maxAmount: string;
  /** Bots making many requests: if the seller offers prepaid, deposit this much (in maxAmount's asset) when the balance runs out. */
  prepay?: string;
  /**
   * Bots making many requests, preferred over prepay: if the seller offers
   * aether-pull, grant it an on-chain allowance of this much ("1 AETH", in
   * maxAmount's asset;
   * payable only to it, for 7 days, revocable) when it has none or it runs
   * low. Nothing is deposited: the seller collects what you owe later.
   */
  pullAllowance?: string;
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
  /** The asset paid in; amount, balance, owed and allowance are in its base units. */
  asset?: Asset;
  amount?: bigint;
  balance?: bigint; // prepaid: left with the seller
  owed?: bigint; // pull: owed to the seller, not yet collected
  allowance?: bigint; // pull: what the allowance still covers beyond that (undefined if unlimited)
  /** The same four, set only when the asset is AETH. */
  amountUaeth?: bigint;
  balanceUaeth?: bigint;
  owedUaeth?: bigint;
  allowanceUaeth?: bigint;
  grantTxHash?: string; // pull: an allowance granted by this call
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

/** The amounts of a result in asset, with the uaeth-named copies for AETH. */
function amounts(asset: Asset, a: { amount?: bigint; balance?: bigint; owed?: bigint; allowance?: bigint }): Partial<FetchPaidResult> {
  const out: Partial<FetchPaidResult> = { asset };
  const aeth = asset.denom === DENOM;
  for (const [k, v] of Object.entries(a) as [keyof typeof a, bigint | undefined][]) {
    if (v === undefined) continue;
    out[k] = v;
    if (aeth) out[`${k}Uaeth`] = v;
  }
  return out;
}

/**
 * Requests url and, if it answers 402 with an Aether payment option, pays
 * and returns the response. Response bodies come from the seller: untrusted.
 * If it returns payment_pending (or throws with a txHash), the payment was
 * made: finish with presentPayment rather than paying again.
 */
export async function fetchPaid(client: AetherClient, key: Key, url: string, opts: FetchPaidOptions): Promise<FetchPaidResult> {
  const { asset, amount: maxAmount } = client.assets.parse(opts.maxAmount);
  // prepay and pullAllowance are in maxAmount's asset: the one the service is expected to charge.
  const same = (field: string, s: string) => {
    const p = client.assets.parse(s);
    if (p.asset.denom !== asset.denom) throw new PaymentError("ASSET_MISMATCH", `${field} is in ${p.asset.symbol} but maxAmount is in ${asset.symbol}: give both in the asset the service charges`);
    return p.amount;
  };
  const pullAllowance = opts.pullAllowance ? same("pullAllowance", opts.pullAllowance) : undefined;
  const prepay = opts.prepay ? same("prepay", opts.prepay) : undefined;
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
  // Only an offer in maxAmount's asset is taken.
  const pick = (scheme: string) => quote.accepts?.find((a) => a.scheme === scheme && a.network === client.chainId && a.asset === asset.denom);

  const pull = pick(SCHEME_PULL);
  if (pullAllowance !== undefined && pull) return fetchPull(client, key, url, method, body, pull, asset, maxAmount, pullAllowance, send, opts);
  const prepaid = pick(SCHEME_PREPAID);
  if (prepay !== undefined && prepaid) return fetchPrepaid(client, key, url, method, body, prepaid, asset, maxAmount, prepay, send, opts);

  const memo = pick(SCHEME_MEMO);
  if (!memo?.extra.invoice) {
    const other = quote.accepts?.find((a) => a.scheme === SCHEME_MEMO && a.network === client.chainId);
    const known = other && client.assets.byDenom(other.asset);
    if (other && known && /^[0-9]+$/.test(other.maxAmountRequired)) {
      throw new PaymentError("ASSET_MISMATCH", `the server charges ${formatAmount(known, BigInt(other.maxAmountRequired))}; maxAmount is in ${asset.symbol}. Nothing was paid: give maxAmount in ${known.symbol}`);
    }
    if (other) throw new PaymentError("PAYMENT_UNSUPPORTED", `the server charges in ${other.asset}, an asset this client doesn't know. Nothing was paid`);
    throw new PaymentError("PAYMENT_UNSUPPORTED", `the server doesn't accept ${SCHEME_MEMO} payments on ${client.chainId}`);
  }
  const price = parseUaeth(memo.maxAmountRequired);
  if (price > maxAmount) throw new PaymentError("PRICE_EXCEEDS_MAX", `the server asks ${formatAmount(asset, price)}; maxAmount is ${formatAmount(asset, maxAmount)}. Nothing was paid`);
  const sent = await client.send(key, memo.payTo, `${price}${asset.baseUnit}`, { memo: memo.extra.invoice });
  if (sent.status === "failed") throw new PaymentError("TX_REJECTED", `the payment was rejected: ${sent.log}`, sent.hash);
  const conf = await client.waitForTransaction(sent.hash, opts.confirmTimeoutMs ?? 150_000);
  if (conf.status === "failed") throw new PaymentError("TX_FAILED", `the payment failed on chain: ${conf.log}`, sent.hash);
  const base = { scheme: SCHEME_MEMO, txHash: sent.hash, invoice: memo.extra.invoice, ...amounts(asset, { amount: price }) };
  if (conf.status === "pending") return { status: "payment_pending", ...base };
  const u = new URL(url);
  return withReceipt({ ...(await presentPayment(client, url, memo.extra.invoice, sent.hash, send)), ...base }, {
    network: client.chainId, payTo: memo.payTo, payer: key.address, scheme: SCHEME_MEMO, payment: sent.hash, amount: price, denom: asset.denom,
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
  asset: Asset, maxAmount: bigint, prepay: bigint, send: (p?: string) => Promise<Response>, opts: FetchPaidOptions,
): Promise<FetchPaidResult> {
  const price = parseUaeth(req.maxAmountRequired);
  if (price > maxAmount) throw new PaymentError("PRICE_EXCEEDS_MAX", `the server asks ${formatAmount(asset, price)} per request; maxAmount is ${formatAmount(asset, maxAmount)}. Nothing was paid`);
  if (req.extra.minDeposit && prepay < parseUaeth(req.extra.minDeposit)) throw new PaymentError("PAYMENT_UNSUPPORTED", `the minimum deposit is ${formatAmount(asset, parseUaeth(req.extra.minDeposit))}`);
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
    return withReceipt({ status: "paid", response: resp, scheme: SCHEME_PREPAID, ...amounts(asset, { amount: price, balance }), txHash: depositTx }, {
      network: client.chainId, payTo: req.payTo, payer: key.address, scheme: SCHEME_PREPAID, payment: requestId, amount: price, denom: asset.denom,
      method, host: u.host, path: decodeURIComponent(u.pathname || "/"), requestBody: body,
    });
  };

  let resp = await attempt();
  if (resp.status !== 402) return done(resp);
  let pr = (await resp.json()) as PaymentRequired;
  if (pr.error !== "insufficient_balance") throw new PaymentError(pr.error === "invoice_already_redeemed" ? "PAYMENT_ALREADY_REDEEMED" : "PAYMENT_REJECTED", `${pr.error}: ${pr.message ?? ""}`);

  const dep = await client.send(key, req.payTo, `${prepay}${asset.baseUnit}`, { memo: DEPOSIT_MEMO_PREFIX + key.address });
  if (dep.status === "failed") throw new PaymentError("TX_REJECTED", `the deposit was rejected: ${dep.log}`, dep.hash);
  const conf = await client.waitForTransaction(dep.hash, opts.confirmTimeoutMs ?? 150_000);
  if (conf.status === "failed") throw new PaymentError("TX_FAILED", `the deposit failed on chain: ${conf.log}`, dep.hash);
  if (conf.status === "pending") return { status: "payment_pending", scheme: SCHEME_PREPAID, txHash: dep.hash, ...amounts(asset, { amount: prepay }) };
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

const PULL_GRANT_ERRORS = new Set(["no_grant", "grant_too_low", "pull_unpaid"]);

async function fetchPull(
  client: AetherClient, key: Key, url: string, method: string, body: Uint8Array, req: PaymentRequirements,
  asset: Asset, maxAmount: bigint, allowance: bigint, send: (p?: string) => Promise<Response>, opts: FetchPaidOptions,
): Promise<FetchPaidResult> {
  const price = parseUaeth(req.maxAmountRequired);
  if (price > maxAmount) throw new PaymentError("PRICE_EXCEEDS_MAX", `the server asks ${formatAmount(asset, price)} per request; maxAmount is ${formatAmount(asset, maxAmount)}. Nothing was paid`);
  if (!req.extra.grantee) throw new PaymentError("PAYMENT_UNSUPPORTED", "the server's aether-pull offer names no grantee");
  if (allowance < price) throw new PaymentError("INVALID_ARGUMENT", `pullAllowance ${formatAmount(asset, allowance)} doesn't cover one request (${formatAmount(asset, price)})`);
  const u = new URL(url);
  const path = decodeURIComponent(u.pathname || "/");
  const requestId = opts.requestId ?? bytesToHex(crypto.getRandomValues(new Uint8Array(16)));
  const attempt = () =>
    send(pullPaymentHeader(key, { network: client.chainId, payTo: req.payTo, host: u.host, method, path, body, maxPrice: price, timestamp: Math.floor(Date.now() / 1000), requestId }));
  let grantTxHash: string | undefined;
  const done = (resp: Response): Promise<FetchPaidResult> => {
    const s = resp.headers.get("X-PAYMENT-RESPONSE");
    const settle = s ? (JSON.parse(new TextDecoder().decode(base64.decode(s))) as { owed?: string; allowance?: string }) : {};
    return withReceipt({
      status: "paid", response: resp, scheme: SCHEME_PULL, grantTxHash,
      ...amounts(asset, { amount: price, owed: settle.owed ? BigInt(settle.owed) : undefined, allowance: settle.allowance ? BigInt(settle.allowance) : undefined }),
    }, { network: client.chainId, payTo: req.payTo, payer: key.address, scheme: SCHEME_PULL, payment: requestId, amount: price, denom: asset.denom, method, host: u.host, path, requestBody: body });
  };

  let resp = await attempt();
  if (resp.status !== 402) return done(resp);
  const pr = (await resp.json()) as PaymentRequired;
  if (!PULL_GRANT_ERRORS.has(pr.error)) {
    throw new PaymentError(pr.error === "invoice_already_redeemed" ? "PAYMENT_ALREADY_REDEEMED" : "PAYMENT_REJECTED", `${pr.error}: ${pr.message ?? ""}`);
  }
  // The new allowance replaces the old one, so it must cover what's owed plus this request.
  const owed = BigInt(pr.accepts?.find((a) => a.scheme === SCHEME_PULL && a.asset === asset.denom)?.extra.owed ?? "0");
  if (allowance < owed + price) {
    throw new PaymentError("INVALID_ARGUMENT", `you owe this service ${formatAmount(asset, owed)} not yet collected; pullAllowance must be at least ${formatAmount(asset, owed + price)} (${owed + price}${asset.baseUnit})`);
  }
  // Granting again is harmless: a grant replaces the previous one.
  const expiration = Math.floor(Date.now() / 1000) + PULL_GRANT_SECONDS;
  const g = await client.signAndBroadcast(key, [grantSendMsg(key.address, req.extra.grantee, allowance, [req.payTo], expiration, asset.denom)]);
  if (g.status === "failed") throw new PaymentError("TX_REJECTED", `the allowance was rejected: ${g.log}`, g.hash);
  grantTxHash = g.hash;
  const conf = await client.waitForTransaction(g.hash, opts.confirmTimeoutMs ?? 150_000);
  if (conf.status === "failed") throw new PaymentError("TX_FAILED", `the allowance failed on chain: ${conf.log}`, g.hash);
  if (conf.status === "pending") return { status: "payment_pending", scheme: SCHEME_PULL, grantTxHash: g.hash, txHash: g.hash, ...amounts(asset, { amount: allowance }) };
  resp = await attempt();
  if (resp.status !== 402) return done(resp);
  const again = (await resp.json()) as PaymentRequired;
  throw new PaymentError("PAYMENT_REJECTED", `the server still refuses after granting an allowance: ${again.error}: ${again.message ?? ""}`, g.hash);
}
