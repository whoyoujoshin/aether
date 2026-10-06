import { sha256 } from "@noble/hashes/sha2.js";
import { bytesToHex } from "@noble/hashes/utils.js";

// Amounts carry a unit: "1.5 AETH" or "1500000uaeth" (1 AETH = 1,000,000
// uaeth). A bare number is refused rather than guessed at -- mixing the
// two up is a million-fold error with real money.

export const DENOM = "uaeth";
const DECIMALS = 6;
const PATTERN = /^\s*([0-9]+)(?:\.([0-9]+))?\s*([A-Za-z]+)\s*$/;
const MAX = (1n << 63n) - 1n;

/** Parses an amount with its unit into uaeth. */
export function parseAmount(s: string): bigint {
  const m = PATTERN.exec(s);
  if (!m) {
    if (/^\s*[0-9]+\s*$/.test(s)) {
      throw new Error(`amount "${s}" has no unit: write e.g. "1.5 AETH" or "1500000uaeth" (1 AETH = 1,000,000 uaeth)`);
    }
    throw new Error(`invalid amount "${s}": write e.g. "1.5 AETH" or "1500000uaeth"`);
  }
  const [, whole, frac = "", unit] = m;
  let v: bigint;
  switch (unit.toLowerCase()) {
    case "aeth":
      if (frac.length > DECIMALS) throw new Error(`amount "${s}" has more than ${DECIMALS} decimal places`);
      v = BigInt(whole + frac.padEnd(DECIMALS, "0"));
      break;
    case DENOM:
      if (frac) throw new Error(`amount "${s}": uaeth can't be fractional`);
      v = BigInt(whole);
      break;
    default:
      throw new Error(`amount "${s}" has unknown unit "${unit}": use AETH or uaeth`);
  }
  if (v <= 0n) throw new Error(`amount "${s}" must be greater than zero`);
  if (v > MAX) throw new Error(`amount "${s}" is too large`);
  return v;
}

/** Renders uaeth as AETH without trailing zeros: 1500000n -> "1.5". */
export function formatAeth(uaeth: bigint): string {
  const neg = uaeth < 0n;
  const a = neg ? -uaeth : uaeth;
  const whole = a / 1_000_000n;
  const frac = (a % 1_000_000n).toString().padStart(DECIMALS, "0").replace(/0+$/, "");
  return (neg ? "-" : "") + whole.toString() + (frac ? "." + frac : "");
}

/** Parses a strictly base-10 whole number of uaeth ("1500000"), as wire formats carry it. */
export function parseUaeth(s: string): bigint {
  if (!/^[0-9]+$/.test(s)) throw new Error(`invalid uaeth amount "${s}"`);
  return BigInt(s);
}

/**
 * A token the client knows by name. Amounts name their asset by unit
 * ("5 USDC", "1.5 AETH", "5000000uusdc"), so a USDC amount can never be
 * read as AETH or the other way round.
 */
export interface Asset {
  /** What people write: "AETH", "USDC". */
  symbol: string;
  /** The chain's name for it: "uaeth", or "ibc/<hash>" for a token that arrived over IBC. */
  denom: string;
  /** The smallest unit's name, which people may also write: "uaeth", "uusdc". */
  baseUnit: string;
  /** How many base units make one symbol, as a power of ten. */
  decimals: number;
  /** Where it comes from: "Aether"; for USDC, its issuer and route, like "Noble over transfer/channel-3". */
  origin: string;
}

export const AETH: Asset = { symbol: "AETH", denom: DENOM, baseUnit: DENOM, decimals: DECIMALS, origin: "Aether" };

/** USDC's denom on Noble, which issues it. */
export const DEFAULT_USDC_BASE_DENOM = "uusdc";

/**
 * Noble's USDC as it exists on Aether after crossing channel, the Aether
 * end of Aether's own channel to Noble. It's usdcAt with a single hop and
 * Noble's base denom.
 */
export function usdc(channel: string): Asset {
  if (!/^channel-[0-9]+$/.test(channel)) throw new Error(`USDC channel "${channel}": want Aether's end of its channel to Noble, like channel-3`);
  return usdcAt(`transfer/${channel}`, DEFAULT_USDC_BASE_DENOM);
}

const HOP = /^[a-zA-Z0-9._+\-#[\]<>]{2,128}\/channel-[0-9]+$/;
const BASE_DENOM = /^[a-zA-Z][a-zA-Z0-9/:._-]{2,127}$/;

/**
 * The USDC that reaches Aether along path, its ICS-20 denom trace as
 * Aether records it (Aether's own hop first), with baseDenom its denom on
 * the chain that issues it: "transfer/channel-1/transfer/channel-4280" and
 * "uusdc" for Noble's USDC through Osmosis, say. The same token reaching
 * Aether any other way has a different denom and isn't interchangeable
 * with it, so it's never accepted as USDC. baseDenom is case-sensitive.
 */
export function usdcAt(path: string, baseDenom: string): Asset {
  const hops = path.split("/");
  if (path === "" || hops.length % 2 !== 0) {
    throw new Error(`USDC path "${path}": want port/channel hops from Aether's end, like transfer/channel-1/transfer/channel-4280`);
  }
  for (let i = 0; i < hops.length; i += 2) {
    const hop = `${hops[i]}/${hops[i + 1]}`;
    if (!HOP.test(hop)) throw new Error(`USDC path "${path}": hop "${hop}" isn't port/channel-N`);
  }
  if (baseDenom.startsWith("ibc/") || !BASE_DENOM.test(baseDenom)) {
    throw new Error(`USDC base denom "${baseDenom}": want its denom on the chain that issues it, like uusdc, not an ibc/ hash`);
  }
  const hash = bytesToHex(sha256(new TextEncoder().encode(`${path}/${baseDenom}`))).toUpperCase();
  const issuer = baseDenom === DEFAULT_USDC_BASE_DENOM ? "Noble" : baseDenom;
  return { symbol: "USDC", denom: `ibc/${hash}`, baseUnit: "uusdc", decimals: 6, origin: `${issuer} over ${path}` };
}

/**
 * Which USDC a client accepts: usdcChannel, as shorthand for Noble's USDC
 * over Aether's direct channel to Noble, or usdcPath and usdcBaseDenom for
 * any other route or issuer (usdcBaseDenom defaults to uusdc). None: AETH
 * only. usdcIssuer names the issuer in the asset's origin, which tools
 * label USDC by ("USDC (Injective)"); default Noble for uusdc, else the
 * base denom.
 */
export interface UsdcSetting {
  usdcChannel?: string;
  usdcPath?: string;
  usdcBaseDenom?: string;
  usdcIssuer?: string;
}

/** The USDC the setting names, or undefined if it names none. */
export function usdcFor(s: UsdcSetting): Asset | undefined {
  let u: Asset | undefined;
  if (s.usdcChannel && (s.usdcPath || s.usdcBaseDenom)) throw new Error("set either usdcChannel or usdcPath and usdcBaseDenom, not both");
  if (s.usdcChannel) u = usdc(s.usdcChannel);
  else if (s.usdcPath) u = usdcAt(s.usdcPath, s.usdcBaseDenom || DEFAULT_USDC_BASE_DENOM);
  else if (s.usdcBaseDenom) throw new Error(`USDC base denom "${s.usdcBaseDenom}" needs usdcPath too`);
  if (!s.usdcIssuer) return u;
  if (!u) throw new Error(`USDC issuer "${s.usdcIssuer}" needs usdcChannel or usdcPath too`);
  if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$/.test(s.usdcIssuer)) throw new Error(`USDC issuer "${s.usdcIssuer}": want one word, like Injective`);
  return { ...u, origin: `${s.usdcIssuer} over ${u.origin.slice(u.origin.indexOf(" over ") + 6)}` };
}

/** Renders base units as a decimal amount without trailing zeros or symbol: 1500000n -> "1.5". */
export function decimalOf(asset: Asset, base: bigint): string {
  const neg = base < 0n;
  const a = neg ? -base : base;
  const one = 10n ** BigInt(asset.decimals);
  const frac = asset.decimals ? (a % one).toString().padStart(asset.decimals, "0").replace(/0+$/, "") : "";
  return (neg ? "-" : "") + (a / one).toString() + (frac ? "." + frac : "");
}

/** Renders base units with the asset's symbol: 1500000n -> "1.5 USDC". */
export function formatAmount(asset: Asset, base: bigint): string {
  return `${decimalOf(asset, base)} ${asset.symbol}`;
}

/** The assets a client accepts: AETH always, and the USDC its setting names, if any. */
export class Assets {
  private readonly all: Asset[];

  constructor(opts: UsdcSetting = {}) {
    this.all = [AETH];
    const u = usdcFor(opts);
    if (u) this.all.push(u);
  }

  /** Every asset, AETH first. */
  list(): Asset[] {
    return [...this.all];
  }

  bySymbol(symbol: string): Asset | undefined {
    return this.all.find((a) => a.symbol.toLowerCase() === symbol.toLowerCase());
  }

  byDenom(denom: string): Asset | undefined {
    return this.all.find((a) => a.denom === denom);
  }

  /**
   * Reads an amount with its unit: a symbol with up to its decimals
   * ("1.5 AETH", "2.25usdc") or a whole number of base units
   * ("2250000 uusdc"). A bare number, or a unit it doesn't know, is refused.
   */
  parse(s: string): { asset: Asset; amount: bigint } {
    const m = PATTERN.exec(s);
    if (!m) {
      if (/^\s*[0-9]+(\.[0-9]+)?\s*$/.test(s)) throw new Error(`amount "${s}" has no unit: write e.g. "1.5 AETH" or "1500000uaeth"`);
      throw new Error(`invalid amount "${s}": write e.g. "1.5 AETH" or "1500000uaeth"`);
    }
    const [, whole, frac = "", unit] = m;
    for (const a of this.all) {
      let v: bigint;
      if (unit.toLowerCase() === a.symbol.toLowerCase()) {
        if (frac.length > a.decimals) throw new Error(`amount "${s}" has more than ${a.decimals} decimal places`);
        v = BigInt(whole + frac.padEnd(a.decimals, "0"));
      } else if (unit.toLowerCase() === a.baseUnit.toLowerCase()) {
        if (frac) throw new Error(`amount "${s}": ${a.baseUnit} is the smallest unit and can't be fractional`);
        v = BigInt(whole);
      } else {
        continue;
      }
      if (v <= 0n) throw new Error(`amount "${s}" must be greater than zero`);
      if (v > MAX) throw new Error(`amount "${s}" is too large`);
      return { asset: a, amount: v };
    }
    throw new Error(`amount "${s}" has unknown unit "${unit}": use ${this.all.flatMap((a) => [a.symbol, a.baseUnit]).join(", ")}`);
  }
}

/** How a signed receipt states an amount: bare uaeth for AETH, else the amount followed by the denom. */
export function receiptAmount(amount: bigint, denom: string = DENOM): string {
  return denom === DENOM || denom === "" ? amount.toString() : `${amount}${denom}`;
}
