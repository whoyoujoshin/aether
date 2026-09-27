// Formatting shared by every page: amounts, times, and the colors each
// message type is drawn in.

/** uaeth (a decimal integer string) as AETH, exactly: no floating point. */
export function aethNumber(uaeth: string): string {
  const neg = uaeth.startsWith("-");
  const s = uaeth.replace(/^-/, "").replace(/^0+(?=\d)/, "").padStart(7, "0");
  const whole = s.slice(0, -6);
  const frac = s.slice(-6).replace(/0+$/, "");
  return `${neg ? "−" : ""}${BigInt(whole).toLocaleString("en-US")}${frac ? "." + frac : ""}`;
}

/** uaeth as "12.5 AETH". */
export function aeth(uaeth: string): string {
  return `${aethNumber(uaeth)} AETH`;
}

/** "12500000" as "12,500,000". */
export function int(n: number | string): string {
  try {
    return BigInt(n).toLocaleString("en-US");
  } catch {
    return String(n);
  }
}

/**
 * The uaeth in a Cosmos coin string such as "12500000uaeth" or
 * "5uaeth,3foo" -- "" when it holds no uaeth.
 */
export function uaethOf(coins: string): string {
  for (const c of coins.split(",")) {
    const m = /^(\d+)uaeth$/.exec(c.trim());
    if (m) return m[1];
  }
  return "";
}

/** A coin string as AETH when it's uaeth, else as given. */
export function coins(coinStr: string): string {
  if (!coinStr) return "—";
  const u = uaethOf(coinStr);
  return u ? aeth(u) : coinStr;
}

/** 28622232 as "28.62M". */
export function compact(n: number | string): string {
  const v = Number(n);
  if (!Number.isFinite(v)) return String(n);
  if (v >= 1e12) return (v / 1e12).toFixed(2) + "T";
  if (v >= 1e9) return (v / 1e9).toFixed(2) + "B";
  if (v >= 1e6) return (v / 1e6).toFixed(2) + "M";
  if (v >= 1e4) return (v / 1e3).toFixed(1) + "K";
  return v.toLocaleString("en-US");
}

export function timeAgo(iso: string | number): string {
  if (!iso) return "—";
  const then = typeof iso === "number" ? iso * 1000 : new Date(iso).getTime();
  if (Number.isNaN(then)) return String(iso);
  const seconds = Math.max(0, Math.floor((Date.now() - then) / 1000));
  if (seconds < 60) return `${seconds}s ago`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86400)}d ago`;
}

/** Seconds as "3 days", "4h 10m", "45s". */
export function duration(seconds: number): string {
  if (seconds <= 0) return "0s";
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (d >= 2 && h === 0) return `${d} days`;
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m`;
  return `${Math.floor(seconds)}s`;
}

const dateFmt = new Intl.DateTimeFormat("en-US", {
  month: "short",
  day: "numeric",
  year: "numeric",
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
  hour12: false,
  timeZone: "UTC",
});

/** "Sep 26, 2026, 21:44:18 UTC". */
export function utc(iso: string | number): string {
  const t = typeof iso === "number" ? iso * 1000 : new Date(iso).getTime();
  if (!t || Number.isNaN(t)) return "—";
  return `${dateFmt.format(t)} UTC`;
}

export function truncate(value: string, head = 10, tail = 6): string {
  if (value.length <= head + tail + 3) return value;
  return `${value.slice(0, head)}…${value.slice(-tail)}`;
}

/** aether1 addresses are long; keep the prefix and enough to tell them apart. */
export function shortAddr(addr: string): string {
  return truncate(addr, 12, 5);
}

export function shortHash(hash: string): string {
  return truncate(hash.toUpperCase(), 10, 6);
}

// --- message types ---

const typeColors: Record<string, string> = {
  MsgSend: "#c0503a",
  MsgMultiSend: "#c0503a",
  MsgVote: "oklch(0.76 0.14 300)",
  MsgExec: "oklch(0.76 0.13 235)",
  MsgGrant: "oklch(0.76 0.13 235)",
  MsgRevoke: "oklch(0.76 0.13 235)",
  MsgGrantAllowance: "oklch(0.76 0.13 235)",
  MsgRevokeAllowance: "oklch(0.76 0.13 235)",
  MsgSubmitPoW: "oklch(0.8 0.14 80)",
  MsgRegisterValidatorPubkey: "oklch(0.78 0.11 190)",
  MsgSubmitProposal: "oklch(0.76 0.14 350)",
  MsgSubmitParamChangeProposal: "oklch(0.76 0.14 350)",
  MsgDeposit: "oklch(0.76 0.14 350)",
};

export function typeColor(msgType: string): string {
  return typeColors[msgType] ?? "#9a9186";
}

export function typeBg(msgType: string): string {
  return `color-mix(in oklch, ${typeColor(msgType)} 14%, transparent)`;
}

/** "MsgSubmitPoW" as "SPW": the tile shown next to each tx. */
export function typeAbbr(msgType: string): string {
  const a = msgType.replace(/^Msg/, "").replace(/[a-z]/g, "").slice(0, 3);
  return a || "TX";
}

/** "/cosmos.bank.v1beta1.MsgSend" as ["MsgSend", "cosmos.bank.v1beta1"]. */
export function splitTypeUrl(typeUrl: string): [string, string] {
  const clean = typeUrl.replace(/^\//, "");
  const i = clean.lastIndexOf(".");
  return i < 0 ? [clean, ""] : [clean.slice(i + 1), clean.slice(0, i)];
}

/** An enum name without its prefix: "PROPOSAL_STATUS_PASSED" as "PASSED". */
export function enumShort(value: string, prefix: string): string {
  return value.replace(prefix, "").replace(/_/g, " ");
}
