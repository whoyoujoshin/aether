import { lookup } from "node:dns/promises";
import { isIP } from "node:net";
import { sha256 } from "@noble/hashes/sha2.js";
import { bech32 } from "@scure/base";
import { AetherClient } from "./client.js";
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
}

export interface Service {
  url: string;
  announcer: string;
  height: number;
  manifest: Manifest;
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

/** Verified paid services from the on-chain directory, newest first. */
export async function findServices(client: AetherClient, opts: { query?: string; maxPrice?: string; allowPrivate?: boolean } = {}): Promise<Service[]> {
  const current = new Map<string, { url: string; announcer: string; height: number }>();
  for (const p of await client.incomingPayments(DIRECTORY_ADDRESS)) {
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
  return results.filter((s): s is Service => !!s).sort((x, y) => y.height - x.height);
}
