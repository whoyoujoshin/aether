// What a transaction pays for gas. Every Aether node asks a minimum price
// per gas (minimum-gas-prices; on aether-1 0.0001uaeth, or 0.0005 in the
// USDC voucher) and refuses a fee under it; paying more is a tip, which
// goes first when blocks are full. Same rule as the Go wallet's FeeFor
// (shared vectors: clients/testdata/fees.json).

/** A price per gas: denom and a decimal amount, e.g. { denom: "uaeth", amount: "0.0001" }. */
export interface GasPrice {
  denom: string;
  amount: string;
}

/** A fee: one coin. */
export interface Fee {
  denom: string;
  amount: bigint;
}

const DEC = 18; // the chain's decimal precision
const SCALE = 10n ** BigInt(DEC);

function decToScaled(s: string): bigint {
  const m = /^([0-9]+)(?:\.([0-9]+))?$/.exec(s.trim());
  if (!m) throw new Error(`price "${s}" isn't a decimal`);
  const frac = (m[2] ?? "").slice(0, DEC).padEnd(DEC, "0");
  return BigInt(m[1]) * SCALE + BigInt(frac);
}

/** Parses a price list as nodes print it: "0.000100000000000000uaeth,0.0005ibc/...". */
export function parseGasPrices(s: string): GasPrice[] {
  const out: GasPrice[] = [];
  for (const part of s.split(",")) {
    const p = part.trim();
    if (!p) continue;
    const m = /^([0-9]+(?:\.[0-9]+)?)([a-zA-Z][a-zA-Z0-9/:._-]{2,127})$/.exec(p);
    if (!m) throw new Error(`price "${p}" isn't an amount followed by a denom`);
    out.push({ amount: m[1], denom: m[2] });
  }
  return out;
}

/**
 * The fee a transaction asking gas pays at prices, times tip (at least 1):
 * in denom if prices name it, else in uaeth, else in the first denom priced;
 * rounded up. Undefined when nothing is priced (no fee).
 */
export function feeFor(prices: GasPrice[], denom: string, gas: bigint | number, tip = 1): Fee | undefined {
  if (!(tip >= 1)) throw new Error(`tip ${tip}: at least 1`);
  const positive = prices.filter((p) => decToScaled(p.amount) > 0n);
  const price = positive.find((p) => p.denom === denom) ?? positive.find((p) => p.denom === "uaeth") ?? positive[0];
  if (!price) return undefined;
  const tipScaled = decToScaled(tip.toString());
  const num = decToScaled(price.amount) * BigInt(gas) * tipScaled;
  const den = SCALE * SCALE;
  return { denom: price.denom, amount: (num + den - 1n) / den };
}
