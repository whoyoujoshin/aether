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
