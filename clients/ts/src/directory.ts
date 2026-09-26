import { lookup } from "node:dns/promises";
import { isIP } from "node:net";
import { sha256 } from "@noble/hashes/sha2.js";
import { bech32 } from "@scure/base";
import { AetherClient } from "./client.js";
import { Key } from "./keys.js";
import { PREFIX } from "./keys.js";
import { parseAmount, parseUaeth } from "./amount.js";
import { trimEnd } from "./util.js";

// The on-chain service directory (package directory): services announce
// themselves with 1 uaeth to a keyless address, memo "x402-service:<url>".
// A listing counts only if the manifest at that URL names the announcer
// as payee.

export const ANNOUNCE_PREFIX = "x402-service:";
export const DELIST_PREFIX = "x402-delist:";
export const MANIFEST_PATH = "/.well-known/x402";

/** The directory address: sha256("aether-x402-directory")[:20], as Go's address.Module derives it. */
export const DIRECTORY_ADDRESS = bech32.encode(PREFIX, bech32.toWords(sha256(new TextEncoder().encode("aether-x402-directory")).subarray(0, 20)));

export interface Manifest {
  x402Version: number;
  name: string;
  description: string; // set by the service: untrusted
  network: string;
  payTo: string;
  price: string;
  priceAeth: string;
  schemes: string[];
  minDeposit?: string;
  withdrawPath?: string; // set if unspent prepaid balance can be withdrawn
}

export interface Service {
  url: string;
  announcer: string;
  height: number;
  manifest: Manifest;
  reputation?: Reputation;
}

export const RATE_PREFIX = "x402-rate:";
/** How far back reputation looks, in blocks (about a week of ~60s blocks). */
export const DEFAULT_WINDOW = 10_080;

export interface Rating {
  rater: string;
  url: string;
  score: number; // 1-5
  height: number;
  txHash: string;
}

export interface RatingSummary {
  count: number;
  average?: number;
}

/**
 * What the chain says about a service. Fees are zero, so a seller can pay
 * itself from accounts it controls for free: payments, payers and ratings
 * from unknown accounts can be manufactured. trustedRatings -- from
 * accounts you pass as trusted (your own, your owner's) -- can't.
 */
export interface Reputation {
  windowBlocks: number;
  payments: number;
  payers: number;
  volumeUaeth: bigint;
  /** Ratings from accounts that paid the service before rating it. */
  ratings: RatingSummary;
  trustedRatings: RatingSummary;
  raters: Rating[];
}

/** The memo that rates url 1-5. */
export function ratingMemo(url: string, score: number): string {
  if (!Number.isInteger(score) || score < 1 || score > 5) throw new Error("a rating's score is 1 to 5");
  return `${RATE_PREFIX}${score}:${normalizeURL(url)}`;
}

/**
 * Rates a service you've paid (only raters who paid it count). Costs 1 uaeth;
 * your latest rating of a service replaces earlier ones.
 */
export async function rateService(client: AetherClient, key: Key, url: string, score: number) {
  return client.send(key, DIRECTORY_ADDRESS, "1uaeth", { memo: ratingMemo(url, score) });
}

function summarize(ratings: Rating[], include?: (rater: string) => boolean): RatingSummary {
  const picked = ratings.filter((r) => !include || include(r.rater));
  return picked.length ? { count: picked.length, average: picked.reduce((a, r) => a + r.score, 0) / picked.length } : { count: 0 };
}

export function normalizeURL(raw: string): string {
  const u = new URL(raw.trim());
  if ((u.protocol !== "http:" && u.protocol !== "https:") || !u.host || u.username || u.password || u.search || u.hash) {
    throw new Error(`service URL "${raw}" must be a plain http(s) URL`);
  }
  const s = trimEnd(`${u.protocol}//${u.host.toLowerCase()}${u.pathname}`, "/");
  if (s.length > 200) throw new Error("service URL is longer than 200 characters");
  return s;
}

function isInternal(ip: string): boolean {
  const v4 = ip.startsWith("::ffff:") ? ip.slice(7) : ip;
  if (isIP(v4) === 4) {
    const [a, b] = v4.split(".").map(Number);
    return a === 0 || a === 10 || a === 127 || (a === 100 && b >= 64 && b < 128) || (a === 169 && b === 254) ||
      (a === 172 && b >= 16 && b < 32) || (a === 192 && b === 168) || a >= 224;
  }
  const l = ip.toLowerCase();
  return l === "::" || l === "::1" || l.startsWith("fe8") || l.startsWith("fe9") || l.startsWith("fea") || l.startsWith("feb") || l.startsWith("fc") || l.startsWith("fd") || l.startsWith("ff");
}

/**
 * Fetches a manifest. Announced URLs come from anyone, so by default this
 * refuses hosts that resolve to internal addresses. (Resolution is checked
 * before connecting; the Go implementation also pins the dialed address.)
 */
export async function fetchManifest(baseURL: string, opts: { allowPrivate?: boolean; timeoutMs?: number } = {}): Promise<Manifest> {
  const u = new URL(baseURL + MANIFEST_PATH);
  if (!opts.allowPrivate) {
    const host = u.hostname.replace(/^\[|\]$/g, "");
    const addrs = isIP(host) ? [{ address: host }] : await lookup(host, { all: true });
    if (addrs.some((a) => isInternal(a.address))) throw new Error("refusing to fetch from a private or internal address");
  }
  const resp = await fetch(u, { redirect: "manual", signal: AbortSignal.timeout(opts.timeoutMs ?? 5000) });
  if (resp.status !== 200) throw new Error(`manifest: HTTP ${resp.status}`);
  const text = await resp.text();
  if (text.length > 64 * 1024) throw new Error("manifest too large");
  return JSON.parse(text) as Manifest;
}

export interface FindServicesOptions {
  query?: string;
  maxPrice?: string;
  allowPrivate?: boolean;
  /** Add each service's reputation (one more scan per service). Default true. */
  reputation?: boolean;
  /** Accounts whose ratings you trust (yours, your owner's...). */
  trusted?: string[];
  windowBlocks?: number;
}

/** Verified paid services from the on-chain directory, newest first. */
export async function findServices(client: AetherClient, opts: FindServicesOptions = {}): Promise<Service[]> {
  const current = new Map<string, { url: string; announcer: string; height: number }>();
  const directoryPayments = await client.incomingPayments(DIRECTORY_ADDRESS);
  for (const p of directoryPayments) {
    if (p.code !== 0 || p.amountUaeth < 1n) continue;
    const delist = p.memo.startsWith(DELIST_PREFIX);
    if (!delist && !p.memo.startsWith(ANNOUNCE_PREFIX)) continue;
    let url: string;
    try {
      url = normalizeURL(p.memo.slice(delist ? DELIST_PREFIX.length : ANNOUNCE_PREFIX.length));
    } catch {
      continue;
    }
    const k = `${p.from}|${url}`;
    if (delist) current.delete(k);
    else current.set(k, { url, announcer: p.from, height: p.height });
  }
  const max = opts.maxPrice ? parseAmount(opts.maxPrice) : undefined;
  const words = (opts.query ?? "").toLowerCase().split(/\s+/).filter(Boolean);
  const results = await Promise.all(
    [...current.values()].map(async (a) => {
      try {
        const m = await fetchManifest(a.url, { allowPrivate: opts.allowPrivate });
        if (m.x402Version !== 1 || m.network !== client.chainId || m.payTo !== a.announcer) return undefined;
        const price = parseUaeth(m.price);
        if (max !== undefined && price > max) return undefined;
        const hay = `${m.name} ${m.description} ${a.url}`.toLowerCase();
        if (!words.every((w) => hay.includes(w))) return undefined;
        return { ...a, manifest: m };
      } catch {
        return undefined;
      }
    }),
  );
  const services = results.filter((s): s is Service => !!s).sort((x, y) => y.height - x.height);
  if (opts.reputation === false || !services.length) return services;

  const window = opts.windowBlocks ?? DEFAULT_WINDOW;
  const since = Math.max(1, (await client.latestHeight()) - window);
  const latest = new Map<string, Rating>();
  for (const p of directoryPayments) {
    if (p.code !== 0 || p.amountUaeth < 1n || p.height < since || !p.memo.startsWith(RATE_PREFIX)) continue;
    const rest = p.memo.slice(RATE_PREFIX.length);
    if (!/^[1-5]:/.test(rest)) continue;
    try {
      const url = normalizeURL(rest.slice(2));
      latest.set(`${p.from}|${url}`, { rater: p.from, url, score: Number(rest[0]), height: p.height, txHash: p.hash });
    } catch {
      continue;
    }
  }
  const trusted = new Set(opts.trusted ?? []);
  await Promise.all(services.map(async (s) => {
    let paid;
    try {
      paid = await client.incomingPayments(s.manifest.payTo, since);
    } catch {
      return;
    }
    const firstPaid = new Map<string, number>();
    let payments = 0;
    let volume = 0n;
    for (const p of paid) {
      if (p.code !== 0 || !p.from || p.from === s.manifest.payTo || p.height < since) continue;
      payments++;
      volume += p.amountUaeth;
      const h = firstPaid.get(p.from);
      if (h === undefined || p.height < h) firstPaid.set(p.from, p.height);
    }
    const raters = [...latest.values()].filter((r) => r.url === s.url && r.rater !== s.manifest.payTo && (firstPaid.get(r.rater) ?? Infinity) <= r.height);
    s.reputation = {
      windowBlocks: window, payments, payers: firstPaid.size, volumeUaeth: volume,
      ratings: summarize(raters), trustedRatings: summarize(raters, (r) => trusted.has(r)), raters,
    };
  }));
  return services;
}
