import { SCALE } from "./constants.ts";
import type { Asset } from "./types.ts";

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

export function assetSymbol(asset: Asset): "AETH" | "USDC" {
  return asset === "aeth" ? "AETH" : "USDC";
}

function withCommas(int: string): string {
  return int.replace(/\B(?=(\d{3})+(?!\d))/g, ",");
}

/** Accepts a decimal string with at most 6 fractional digits. */
export function parseBase(input: string): bigint | null {
  const s = input.trim();
  if (!/^\d+(\.\d*)?$/.test(s)) return null;
  const [whole, frac = ""] = s.split(".");
  if (frac.length > 6) return null;
  const padded = (frac + "000000").slice(0, 6);
  return BigInt(whole) * SCALE + BigInt(padded);
}

export function sanitizeAmount(raw: string): string {
  const cleaned = raw.replace(/[^\d.]/g, "");
  const dot = cleaned.indexOf(".");
  const wholeRaw = dot === -1 ? cleaned : cleaned.slice(0, dot);
  const fracRaw = dot === -1 ? null : cleaned.slice(dot + 1).replace(/\./g, "");
  let whole = wholeRaw.replace(/^0+(?=\d)/, "");
  if (whole.length > 12) whole = whole.slice(0, 12);
  if (fracRaw === null) return whole;
  if (whole === "") whole = "0";
  return `${whole}.${fracRaw.slice(0, 6)}`;
}

export function baseToInput(amount: bigint): string {
  if (amount <= 0n) return "";
  const whole = amount / SCALE;
  const frac = (amount % SCALE).toString().padStart(6, "0").replace(/0+$/, "");
  return frac ? `${whole.toString()}.${frac}` : whole.toString();
}

export function formatBase(amount: bigint, maxFrac = 6): string {
  const neg = amount < 0n;
  const v = neg ? -amount : amount;
  const whole = v / SCALE;
  const fracFull = (v % SCALE).toString().padStart(6, "0").slice(0, maxFrac);
  const frac = fracFull.replace(/0+$/, "");
  const body = frac ? `${withCommas(whole.toString())}.${frac}` : withCommas(whole.toString());
  return neg ? `−${body}` : body;
}

/** USDC-per-AETH price stored at 1e6. Keeps at least 4 fractional digits. */
export function formatPrice(e6: bigint): string {
  const neg = e6 < 0n;
  const v = neg ? -e6 : e6;
  const whole = v / SCALE;
  const fracFull = (v % SCALE).toString().padStart(6, "0");
  let frac = fracFull.replace(/0+$/, "");
  if (frac.length < 4) frac = fracFull.slice(0, 4);
  const body = `${withCommas(whole.toString())}.${frac}`;
  return neg ? `−${body}` : body;
}

/** Absolute percent from a bps integer. 31 → "0.31%". */
export function formatPercentFromBps(bps: bigint): string {
  const v = bps < 0n ? -bps : bps;
  const whole = v / 100n;
  const frac = (v % 100n).toString().padStart(2, "0");
  return `${whole.toString()}.${frac}%`;
}

export function formatK(k: bigint): string {
  const s = k.toString();
  if (s.length <= 6) return s;
  const exp = s.length - 1;
  const digits = s.slice(0, 7);
  return `${digits[0]}.${digits.slice(1)}×10^${exp.toString()}`;
}

export function formatUtc(ts: number): string {
  const d = new Date(ts);
  const mon = MONTHS[d.getUTCMonth()] ?? "Jan";
  const day = String(d.getUTCDate());
  const hh = String(d.getUTCHours()).padStart(2, "0");
  const mm = String(d.getUTCMinutes()).padStart(2, "0");
  const ss = String(d.getUTCSeconds()).padStart(2, "0");
  return `${mon} ${day}, ${hh}:${mm}:${ss} UTC`;
}
