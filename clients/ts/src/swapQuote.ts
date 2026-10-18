// quote_swap: what an amount of AETH would fetch in OSMO from the AETH/OSMO
// pool on Osmosis testnet, from Osmosis's own estimate (which includes its
// taker fee as well as the pool's swap fee). Read-only. The same tool as
// agentmcp's (cmd/agentmcp/swapquote.go): the same reads, cache, pacing,
// fields and rounding; clients/testdata/swapquote.json keeps them alike.

import { sha256 } from "@noble/hashes/sha2.js";
import { bytesToHex } from "@noble/hashes/utils.js";
import { formatAeth, parseAmount } from "./amount.js";

export const TESTNET_OSMOSIS_POOL = 1370;
export const TESTNET_OSMOSIS_REST = ["https://lcd.osmotest5.osmosis.zone", "https://lcd.testnet.osmosis.zone"];
const CHANNEL_ON_AETHER = "channel-3";
const CHANNEL_ON_OSMOSIS = "channel-11852";
const QUOTE_TTL_MS = 10_000;
const UPSTREAM_PER_MIN = 30; // Osmosis's public endpoints ban callers that poll hard
const MAX_SLIPPAGE = 50;

const ibcDenom = (trace: string) => "ibc/" + bytesToHex(sha256(new TextEncoder().encode(trace))).toUpperCase();
/** AETH's denom on Osmosis, and OSMO's on Aether. */
export const AETH_ON_OSMOSIS = ibcDenom(`transfer/${CHANNEL_ON_OSMOSIS}/uaeth`);
export const OSMO_ON_AETHER = ibcDenom(`transfer/${CHANNEL_ON_AETHER}/uosmo`);

type Result = Record<string, unknown>;
const error = (code: string, message: string, extra: Record<string, unknown> = {}): Result => ({ error: { code, message, ...extra } });

/** n/d as a decimal with places digits, halves rounded away from zero (Go's big.Rat FloatString). */
function fixed(n: bigint, d: bigint, places: number): string {
  if (d < 0n) [n, d] = [-n, -d];
  const neg = n < 0n;
  const a = neg ? -n : n;
  const scale = 10n ** BigInt(places);
  let q = (a * scale) / d;
  if (((a * scale) % d) * 2n >= d) q += 1n;
  const s = q.toString().padStart(places + 1, "0");
  return (neg ? "-" : "") + (places ? `${s.slice(0, -places)}.${s.slice(-places)}` : s);
}

/** A decimal string as a fraction: "0.003" -> [3n, 1000n]. */
function ratOf(s: string): [bigint, bigint] | undefined {
  const m = /^(-?)([0-9]+)(?:\.([0-9]+))?$/.exec(s);
  if (!m) return undefined;
  const frac = m[3] ?? "";
  const n = BigInt(m[2] + frac) * (m[1] ? -1n : 1n);
  return [n, 10n ** BigInt(frac.length)];
}

/** u without surrounding space or trailing slashes (a loop: /\/+$/ backtracks on long runs of "/"). */
function trimSlashes(u: string): string {
  let s = u.trim();
  while (s.endsWith("/")) s = s.slice(0, -1);
  return s;
}

/** A 6-decimal base amount in whole units, trailing zeros trimmed. */
function formatMicro(n: bigint): string {
  return fixed(n, 1_000_000n, 6).replace(/0+$/, "").replace(/\.$/, "");
}

const osmo = (uosmo: bigint) => ({ osmo: formatMicro(uosmo), uosmo: uosmo.toString(), denomOnAether: OSMO_ON_AETHER });

interface RawQuote {
  pool: number;
  reserveIn: bigint; reserveOut: bigint; weightIn: bigint; weightOut: bigint;
  swapFee: [bigint, bigint];
  out: bigint;
  at: number;
}

export interface SwapQuoterOptions {
  /** Osmosis REST endpoints, tried in order. */
  rest?: string[];
  pool?: number;
  fetch?: typeof fetch;
  now?: () => number;
}

/** Quotes AETH into OSMO, caching answers for 10 s and asking Osmosis at most 30 times a minute. */
export class SwapQuoter {
  private readonly rest: string[];
  private readonly pool: number;
  private readonly fetch: typeof fetch;
  private readonly now: () => number;
  private readonly answers = new Map<string, RawQuote>();
  private upstream: number[] = [];

  constructor(opts: SwapQuoterOptions = {}) {
    this.rest = (opts.rest ?? TESTNET_OSMOSIS_REST).map(trimSlashes).filter(Boolean);
    this.pool = opts.pool ?? TESTNET_OSMOSIS_POOL;
    this.fetch = opts.fetch ?? fetch;
    this.now = opts.now ?? Date.now;
  }

  async quote(args: { amount?: string; slippagePercent?: number }): Promise<Result> {
    if (!this.rest.length) return error("SWAP_NOT_CONFIGURED", "quote_swap is off: no Osmosis endpoint is set");
    let amount: bigint;
    const text = String(args.amount ?? "");
    if (/usdc/i.test(text)) return error("INVALID_AMOUNT", 'quote_swap quotes AETH into OSMO; amount must be AETH, e.g. "1 AETH"');
    try {
      amount = parseAmount(text);
    } catch (e) {
      return error("INVALID_AMOUNT", (e as Error).message);
    }
    let slip = args.slippagePercent ?? 0;
    if (typeof slip !== "number" || Number.isNaN(slip)) return error("INVALID_ARGUMENT", "slippagePercent must be a number");
    if (slip === 0) slip = 1;
    if (slip < 0 || slip > MAX_SLIPPAGE) return error("INVALID_ARGUMENT", `slippagePercent must be between 0 and ${MAX_SLIPPAGE}`);

    const q = await this.get(amount);
    if ("error" in q) return q;
    return this.output(q as RawQuote, amount, slip);
  }

  private async get(amount: bigint): Promise<RawQuote | Result> {
    const key = `${this.pool}/${amount}`;
    const now = this.now();
    const cached = this.answers.get(key);
    if (cached && now - cached.at < QUOTE_TTL_MS) return cached;
    for (const [k, v] of this.answers) if (now - v.at >= QUOTE_TTL_MS) this.answers.delete(k);
    this.upstream = this.upstream.filter((t) => now - t < 60_000);
    if (this.upstream.length >= UPSTREAM_PER_MIN) {
      const wait = Math.floor((60_000 - (now - this.upstream[0])) / 1000) + 1;
      return error("QUOTE_UNAVAILABLE", "too many quotes this minute; try again shortly", { retryAfterSeconds: wait });
    }
    this.upstream.push(now);
    let last = "";
    for (const base of this.rest) {
      const q = await this.fetchFrom(base, amount);
      if (!("error" in q)) {
        this.answers.set(key, q as RawQuote);
        return q;
      }
      const e = (q as { error: { code: string; message: string } }).error;
      if (e.code === "INVALID_AMOUNT") return q; // Osmosis refused this amount; another endpoint won't differ
      last = e.message;
    }
    return error("QUOTE_UNAVAILABLE", `no Osmosis endpoint answered: ${last}`);
  }

  private async getJSON(url: string): Promise<{ ok: true; body: any } | { ok: false; result: Result }> {
    let resp: Response;
    try {
      resp = await this.fetch(url, { headers: { Accept: "application/json" }, signal: AbortSignal.timeout(10_000) });
    } catch (e) {
      return { ok: false, result: error("UNAVAILABLE", `${url}: ${(e as Error).message}`) };
    }
    const text = await resp.text();
    if (resp.status === 400) {
      let msg = text.trim();
      try {
        msg = JSON.parse(text).message || msg;
      } catch {}
      return { ok: false, result: error("INVALID_AMOUNT", `Osmosis refused the estimate: ${msg}`) };
    }
    if (resp.status !== 200) return { ok: false, result: error("UNAVAILABLE", `${url}: HTTP ${resp.status}`) };
    try {
      return { ok: true, body: JSON.parse(text) };
    } catch (e) {
      return { ok: false, result: error("UNAVAILABLE", `${url}: ${(e as Error).message}`) };
    }
  }

  private async fetchFrom(base: string, amount: bigint): Promise<RawQuote | Result> {
    const p = await this.getJSON(`${base}/osmosis/poolmanager/v1beta1/pools/${this.pool}`);
    if (!p.ok) return p.result;
    const pool = p.body?.pool ?? {};
    if (pool["@type"] !== "/osmosis.gamm.v1beta1.Pool") return error("UNAVAILABLE", `pool ${this.pool} is "${pool["@type"]}", not a balancer pool`);
    let reserveIn = 0n, reserveOut = 0n, weightIn = 0n, weightOut = 0n;
    for (const pa of pool.pool_assets ?? []) {
      const denom = pa?.token?.denom;
      if (!/^[0-9]+$/.test(String(pa?.token?.amount)) || !/^[0-9]+$/.test(String(pa?.weight))) return error("UNAVAILABLE", `pool ${this.pool}: unreadable asset ${denom}`);
      if (denom === AETH_ON_OSMOSIS) [reserveIn, weightIn] = [BigInt(pa.token.amount), BigInt(pa.weight)];
      if (denom === "uosmo") [reserveOut, weightOut] = [BigInt(pa.token.amount), BigInt(pa.weight)];
    }
    if (reserveIn <= 0n || reserveOut <= 0n || weightIn <= 0n || weightOut <= 0n) return error("UNAVAILABLE", `pool ${this.pool} does not hold both AETH and OSMO`);
    const swapFee = ratOf(String(pool.pool_params?.swap_fee));
    if (!swapFee) return error("UNAVAILABLE", `pool ${this.pool}: unreadable swap fee`);

    const tokenIn = encodeURIComponent(`${amount}${AETH_ON_OSMOSIS}`);
    const e = await this.getJSON(`${base}/osmosis/poolmanager/v1beta1/${this.pool}/estimate/single_pool_swap_exact_amount_in?pool_id=${this.pool}&token_in=${tokenIn}&token_out_denom=uosmo`);
    if (!e.ok) return e.result;
    const outText = String(e.body?.token_out_amount ?? "");
    if (!/^[0-9]+$/.test(outText) || BigInt(outText) <= 0n) return error("INVALID_AMOUNT", `Osmosis estimates no OSMO for ${formatAeth(amount)} AETH`);
    return { pool: this.pool, reserveIn, reserveOut, weightIn, weightOut, swapFee, out: BigInt(outText), at: this.now() };
  }

  private output(q: RawQuote, amount: bigint, slip: number): Result {
    // Spot, OSMO per AETH: (Ro/Wo)/(Ri/Wi) = Ro*Wi / (Wo*Ri).
    const spotN = q.reserveOut * q.weightIn, spotD = q.weightOut * q.reserveIn;
    // cost = (1 - price/spot) * 100, price = out/amount.
    const costN = (amount * spotN - q.out * spotD) * 100n, costD = amount * spotN;
    const [sn, sd] = ratOf(String(slip)) ?? [1n, 1n];
    const minOut = (q.out * (100n * sd - sn)) / (100n * sd); // floor
    return {
      chain: "osmo-test-5",
      pool: q.pool,
      in: { asset: "AETH", amount: formatAeth(amount), base: amount.toString(), denom: "uaeth", uaeth: amount.toString(), aeth: formatAeth(amount) },
      out: osmo(q.out),
      spotPrice: fixed(spotN, spotD, 6),
      price: fixed(q.out, amount, 6),
      costPercent: fixed(costN, costD, 2),
      swapFeePercent: fixed(q.swapFee[0] * 100n, q.swapFee[1], 2),
      reserves: { aeth: formatAeth(q.reserveIn), osmo: formatMicro(q.reserveOut) },
      slippagePercent: fixed(sn, sd, 2),
      minOutput: osmo(minOut),
      quotedAt: new Date(q.at).toISOString().replace(/\.\d{3}Z$/, "Z"),
      note: "A quote, not a promise: the pool moves with every trade, and it is small, so a large swap gets a poor price (watch costPercent). " +
        "Swapping from Aether goes through contracts/aether-swap on Osmosis and back over IBC, about 22 s; this tool only quotes it.",
    };
  }
}
