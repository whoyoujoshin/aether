import { sha256 } from "@noble/hashes/sha2.js";
import { bytesToHex } from "@noble/hashes/utils.js";
import { base64 } from "@scure/base";
import { receiptAmount } from "./amount.js";
import { Key, addressOf } from "./keys.js";
import { signsFor, type AccountKeys } from "./rotation.js";

// Receipts: a seller's signed statement about one paid response -- who
// paid what for which request, and what came back (status and a hash of
// the body). Signed by the key that signs for the payee, or by a key the
// payee delegated receipts to. Without a stored-key lookup, verification
// checks that the key hashes to the payee's address (an account that never
// rotated). Pass account keys to apply the chain's rule: a rotated payee's
// new key is accepted and its retired key is refused. The same format as
// the Go paywall (package paywall).

export const RECEIPT_HEADER = "X-PAYMENT-RECEIPT";
const RECEIPT_DOMAIN = "aether-x402-receipt/v1\n";
const DELEGATION_DOMAIN = "aether-x402-receipt-delegation/v1\n";

export interface ReceiptDelegation {
  payTo: string;
  signer: string; // address of the receipt key
  expires: number; // unix seconds
  payToPubKey: string; // base64
  signature: string; // base64
}

export interface Receipt {
  x402Version: number;
  network: string;
  payTo: string;
  payer: string;
  scheme: string;
  /** Transaction hash (aether-memo) or request ID (aether-prepaid). */
  payment: string;
  amount: string; // uaeth, or the amount followed by the denom for any other asset ("50000ibc/...")
  method: string;
  host: string;
  path: string;
  requestHash: string; // hex SHA-256 of the request body
  status: number;
  /** Hex SHA-256 of the response body; absent if it was too big or streamed. */
  responseHash?: string;
  at: number; // unix seconds
  signer: string; // base64 ML-DSA-44 public key
  delegation?: ReceiptDelegation;
  signature: string; // base64
}

const lines = (domain: string, fields: string[]) => new TextEncoder().encode(domain + fields.map((f) => f + "\n").join(""));

/** The exact bytes a receipt's signer signs. */
export function receiptSigningMessage(r: Omit<Receipt, "signer" | "signature" | "delegation" | "x402Version">): Uint8Array {
  return lines(RECEIPT_DOMAIN, [r.network, r.payTo, r.payer, r.scheme, r.payment, r.amount, r.method, r.host, r.path,
    r.requestHash, String(r.status), r.responseHash ?? "", String(r.at)]);
}

/** The exact bytes a payee signs to delegate receipts. */
export function delegationSigningMessage(payTo: string, signer: string, expires: number): Uint8Array {
  return lines(DELEGATION_DOMAIN, [payTo, signer, String(expires)]);
}

/** Lets the key with address `signer` sign receipts for payee's account until `expires` (unix seconds). */
export function createReceiptDelegation(payee: Key, signer: string, expires: number): ReceiptDelegation {
  return {
    payTo: payee.address, signer, expires, payToPubKey: base64.encode(payee.publicKey),
    signature: base64.encode(payee.sign(delegationSigningMessage(payee.address, signer, expires))),
  };
}

const hex = (b: Uint8Array) => bytesToHex(sha256(b));
const HEX_HASH = /^[0-9a-f]{64}$/;

/** A field with a line break could shift the signed lines after it into other fields. */
function malformed(r: Omit<Receipt, "signer" | "signature" | "delegation" | "x402Version">): string | undefined {
  for (const f of [r.network, r.payTo, r.payer, r.scheme, r.payment, r.amount, r.method, r.host, r.path]) {
    if (typeof f !== "string" || /[\r\n]/.test(f)) return "a field contains a line break";
  }
  if (!HEX_HASH.test(r.requestHash) || (r.responseHash !== undefined && r.responseHash !== "" && !HEX_HASH.test(r.responseHash))) return "hashes must be hex SHA-256";
  if (!Number.isInteger(r.status) || !Number.isInteger(r.at)) return "status and at must be integers";
  return undefined;
}

function decodePub(s: string): Uint8Array | undefined {
  try {
    const b = base64.decode(s);
    return b.length === 1312 ? b : undefined;
  } catch {
    return undefined;
  }
}

function sigOK(pub: Uint8Array, msg: Uint8Array, sig: string): boolean {
  try {
    return Key.verify(pub, msg, base64.decode(sig));
  } catch {
    return false;
  }
}

/** Whether pub signs for account. A lookup error is a refusal, never a pass. */
function signsForPayee(account: string, pub: Uint8Array, keys?: AccountKeys): { ok: boolean; problem?: string } {
  if (!keys) return { ok: addressOf(pub) === account };
  try {
    return { ok: signsFor(account, pub, keys.accountPubKey(account)) };
  } catch (e) {
    return { ok: false, problem: `reading the payee's key: ${(e as Error).message}` };
  }
}

/**
 * Checks that the receipt was signed for its payee (directly or by
 * delegation); returns why not, or undefined if it was. Says nothing
 * about whether it matches a purchase: see checkReceipt.
 *
 * keys omitted checks only that a key hashes to the payee's address.
 * With keys, the chain's rule applies: the key stored on the payee, or
 * the key its address hashes from while none is stored.
 */
export function verifyReceipt(r: Receipt, keys?: AccountKeys): string | undefined {
  if (r?.x402Version !== 1) return "unsupported receipt version";
  const bad = malformed(r);
  if (bad) return bad;
  const pub = decodePub(r.signer);
  if (!pub) return "the signer isn't a base64 ML-DSA-44 public key";
  const direct = signsForPayee(r.payTo, pub, keys);
  if (direct.problem) return direct.problem;
  if (!direct.ok) {
    const signer = addressOf(pub);
    const d = r.delegation;
    if (!d) return "signed by a key that isn't the payee's, with no delegation";
    if (d.payTo !== r.payTo || d.signer !== signer) return "the delegation is for another payee or key";
    const payee = decodePub(d.payToPubKey);
    if (!payee) return "the delegation isn't signed by the payee's key";
    const byPayee = signsForPayee(r.payTo, payee, keys);
    if (byPayee.problem) return byPayee.problem;
    if (!byPayee.ok) return "the delegation isn't signed by the payee's key";
    if (!sigOK(payee, delegationSigningMessage(d.payTo, d.signer, d.expires), d.signature)) return "the delegation's signature is bad";
    if (r.at > d.expires) return "signed after its delegation expired";
  }
  if (!sigOK(pub, receiptSigningMessage(r), r.signature)) return "bad signature";
  return undefined;
}

export interface ReceiptExpectation {
  network: string;
  payTo: string;
  payer: string;
  scheme: string;
  payment: string;
  /** In the base units of denom. */
  amount: bigint;
  /** The asset paid; default uaeth. */
  denom?: string;
  method: string;
  host: string;
  path: string;
  requestBody: Uint8Array;
  status: number;
  /** Omit to skip the response hash. */
  responseBody?: Uint8Array;
}

/** verifyReceipt, and that it describes exactly this purchase; returns the problem, or undefined. */
export function checkReceipt(r: Receipt, want: ReceiptExpectation, keys?: AccountKeys): string | undefined {
  const bad = verifyReceipt(r, keys);
  if (bad) return bad;
  const checks: [string, string, string][] = [
    ["network", r.network, want.network], ["payTo", r.payTo, want.payTo], ["payer", r.payer, want.payer],
    ["scheme", r.scheme, want.scheme], ["payment", r.payment.toUpperCase(), want.payment.toUpperCase()],
    ["amount", r.amount, receiptAmount(want.amount, want.denom)], ["method", r.method, want.method], ["host", r.host, want.host],
    ["path", r.path, want.path], ["requestHash", r.requestHash, hex(want.requestBody)], ["status", String(r.status), String(want.status)],
  ];
  for (const [name, got, exp] of checks) if (got !== exp) return `${name} is "${got}", not "${exp}"`;
  if (r.responseHash && want.responseBody && r.responseHash !== hex(want.responseBody)) return "responseHash doesn't match the response received";
  return undefined;
}

/** Reads an X-PAYMENT-RECEIPT header value (undefined if unreadable). */
export function decodeReceipt(header: string): Receipt | undefined {
  try {
    return JSON.parse(new TextDecoder().decode(base64.decode(header))) as Receipt;
  } catch {
    return undefined;
  }
}

/** Whether these fields make an unambiguous receipt (sellers sign only those). */
export function receiptFieldsOK(fields: Omit<Receipt, "signer" | "signature" | "delegation" | "x402Version">): boolean {
  return !malformed(fields);
}

/** Signs a receipt (sellers). */
export function signReceipt(key: Key, fields: Omit<Receipt, "signer" | "signature" | "delegation" | "x402Version">, delegation?: ReceiptDelegation): Receipt {
  const r: Receipt = { x402Version: 1, ...fields, signer: base64.encode(key.publicKey), signature: "" };
  if (!r.responseHash) delete r.responseHash;
  if (delegation) r.delegation = delegation;
  r.signature = base64.encode(key.sign(receiptSigningMessage(r)));
  return r;
}

export const sha256Hex = hex;
