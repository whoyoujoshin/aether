import { bytesToHex } from "@noble/hashes/utils.js";
import { AetherClient } from "./client.js";
import { Key } from "./keys.js";
import { parseAmount, parseUaeth } from "./amount.js";
import { MANIFEST_PATH, type Manifest } from "./directory.js";
import { PaymentError, prepaidPaymentHeader } from "./paywall.js";

// Taking back unspent prepaid balance. The seller pays it back on chain
// to the signing account itself; each withdrawalId pays out at most
// once, so retry with the same one.

export interface WithdrawOptions {
  /** "all" (default) or an amount with its unit ("0.5 AETH"). */
  amount?: string;
  /** Reuse it to retry: the seller pays each ID out once. Default: random. */
  withdrawalId?: string;
}

export interface WithdrawResult {
  /** pending: sent, not in a block yet; confirmed; reserved: set aside, not sent yet -- call again with the same withdrawalId. */
  status: "pending" | "confirmed" | "reserved";
  withdrawalId: string;
  amountUaeth: bigint;
  txHash?: string;
  /** Left with the seller. */
  balanceUaeth?: bigint;
  message?: string;
}

interface WithdrawalResponse {
  withdrawalId?: string;
  amount?: string;
  status?: WithdrawResult["status"];
  txHash?: string;
  balance?: string;
  error?: string;
  message?: string;
}

const CODES: Record<string, string> = {
  withdrawals_unavailable: "WITHDRAWALS_UNAVAILABLE",
  insufficient_balance: "INSUFFICIENT_PREPAID_BALANCE",
  below_minimum_withdrawal: "INSUFFICIENT_PREPAID_BALANCE",
  withdrawal_id_reused: "IDEMPOTENCY_CONFLICT",
  payout_failed: "WITHDRAWAL_FAILED", // nothing paid; the balance is intact
  payout_unavailable: "WITHDRAWAL_FAILED",
};

/** Withdraws unspent prepaid balance from the service at `service` (any URL on it). */
export async function withdrawPrepaid(client: AetherClient, key: Key, service: string, opts: WithdrawOptions = {}): Promise<WithdrawResult> {
  const u = new URL(service);
  if (u.protocol !== "http:" && u.protocol !== "https:") throw new PaymentError("INVALID_ARGUMENT", `service ${service} must be an http(s) URL`);
  const amount = !opts.amount || opts.amount.trim().toLowerCase() === "all" ? "all" : parseAmount(opts.amount).toString();
  const withdrawalId = opts.withdrawalId ?? bytesToHex(crypto.getRandomValues(new Uint8Array(16)));

  const mresp = await fetch(u.origin + MANIFEST_PATH, { redirect: "manual" });
  if (mresp.status !== 200) throw new PaymentError("PAYMENT_UNSUPPORTED", `${u.origin}${MANIFEST_PATH} answered HTTP ${mresp.status}: not an Aether paid service`);
  const m = (await mresp.json()) as Manifest;
  if (m.network !== client.chainId) throw new PaymentError("PAYMENT_UNSUPPORTED", `the service is on network ${m.network}, not ${client.chainId}`);
  if (!m.withdrawPath) throw new PaymentError("WITHDRAWALS_UNAVAILABLE", "this service doesn't offer withdrawals: its operator holds the balance");
  // A path, never something that would change the host when appended ("@evil.example/").
  if (!/^\/(?!\/)/.test(m.withdrawPath)) throw new PaymentError("PAYMENT_UNSUPPORTED", "the manifest's withdrawPath is not a path on this service");

  const body = new TextEncoder().encode(JSON.stringify({ amount }));
  const header = prepaidPaymentHeader(key, {
    network: client.chainId, payTo: m.payTo, host: u.host, method: "POST", path: m.withdrawPath,
    body, maxPrice: 0n, timestamp: Math.floor(Date.now() / 1000), requestId: withdrawalId,
  });
  const resp = await fetch(u.origin + m.withdrawPath, {
    method: "POST", redirect: "manual", headers: { "Content-Type": "application/json", "X-PAYMENT": header },
    body: body as unknown as BodyInit,
  });
  let r: WithdrawalResponse;
  try {
    r = (await resp.json()) as WithdrawalResponse;
  } catch {
    throw new PaymentError("HTTP_ERROR", `the service answered HTTP ${resp.status} without a withdrawal result`);
  }
  if (r.error) throw new PaymentError(CODES[r.error] ?? "WITHDRAWAL_REJECTED", `the service refused the withdrawal (${r.error}): ${r.message ?? ""}`);
  return {
    status: r.status ?? "pending", withdrawalId, amountUaeth: r.amount ? parseUaeth(r.amount) : 0n, txHash: r.txHash,
    balanceUaeth: r.balance !== undefined && /^\d+$/.test(r.balance) ? BigInt(r.balance) : undefined, message: r.message,
  };
}
