/**
 * Constant-product AMM for Aether AETH / USDC.
 *
 * Amounts are base units (6 decimals): `uaeth` and the IBC USDC denom pinned
 * on aether-testnet-1. Division truncates toward zero, so the pool keeps the
 * dust. The 0.30% fee stays in the reserves — k grows, and liquidity
 * providers earn it. There is no protocol fee in this reference.
 *
 * Selling and buying are the same function. Pass the offer reserve as
 * `reserveIn` and the ask reserve as `reserveOut`.
 *
 * Published vector (pinned in constants.ts, checked by math.test.ts):
 *   reserves  50_000 AETH / 20_000 USDC
 *   exact in  1 AETH = 1_000_000 uaeth
 *   exact out 398_792 base USDC
 */

export const BPS = 10_000n;
export const FEE_BPS = 30n;
const FEE_FACTOR = BPS - FEE_BPS;

export const FORMULA = [
  "amountInWithFee = amountIn * 9970 / 10000",
  "amountOut = floor(reserveOut * amountInWithFee / (reserveIn * 10000 + amountInWithFee))",
].join("\n");

export function quoteExactIn(amountIn: bigint, reserveIn: bigint, reserveOut: bigint): bigint {
  if (amountIn <= 0n || reserveIn <= 0n || reserveOut <= 0n) return 0n;
  const amountInWithFee = amountIn * FEE_FACTOR;
  return (reserveOut * amountInWithFee) / (reserveIn * BPS + amountInWithFee);
}

/** USDC per AETH, scaled by 1e6. 0.40 prints as 400_000. */
export function spotPriceE6(reserveAeth: bigint, reserveUsdc: bigint): bigint {
  if (reserveAeth <= 0n) return 0n;
  return (reserveUsdc * 1_000_000n) / reserveAeth;
}

/** Execution price in the same USDC-per-AETH 1e6 scale. */
export function executionPriceE6(amountIn: bigint, amountOut: bigint, aethIsIn: boolean): bigint {
  if (amountIn <= 0n || amountOut <= 0n) return 0n;
  if (aethIsIn) return (amountOut * 1_000_000n) / amountIn;
  return (amountIn * 1_000_000n) / amountOut;
}

/**
 * Size impact beyond the flat fee, in bps.
 * Compares the quote to an infinite-liquidity fill that still pays 0.30%.
 */
export function priceImpactBps(
  amountIn: bigint,
  amountOut: bigint,
  reserveIn: bigint,
  reserveOut: bigint,
): bigint {
  if (amountIn <= 0n || amountOut <= 0n || reserveIn <= 0n || reserveOut <= 0n) return 0n;
  const ideal = (amountIn * FEE_FACTOR * reserveOut) / (BPS * reserveIn);
  if (ideal <= 0n || amountOut >= ideal) return 0n;
  return ((ideal - amountOut) * BPS) / ideal;
}

export function minOut(amountOut: bigint, slippageBps: bigint): bigint {
  if (amountOut <= 0n || slippageBps < 0n || slippageBps >= BPS) return 0n;
  return (amountOut * (BPS - slippageBps)) / BPS;
}

/** Fee retained by the pool, in the input asset's base units. */
export function feePaid(amountIn: bigint): bigint {
  if (amountIn <= 0n) return 0n;
  return (amountIn * FEE_BPS) / BPS;
}

export function applySwap(
  reserveIn: bigint,
  reserveOut: bigint,
  amountIn: bigint,
): { amountOut: bigint; reserveIn: bigint; reserveOut: bigint } {
  const amountOut = quoteExactIn(amountIn, reserveIn, reserveOut);
  if (amountOut <= 0n || amountOut >= reserveOut) {
    throw new Error("insufficient liquidity");
  }
  return {
    amountOut,
    reserveIn: reserveIn + amountIn,
    reserveOut: reserveOut - amountOut,
  };
}

export function isqrt(n: bigint): bigint {
  if (n < 0n) throw new Error("sqrt of negative");
  if (n < 2n) return n;
  let x = n;
  let y = (x + 1n) / 2n;
  while (y < x) {
    x = y;
    y = (x + n / x) / 2n;
  }
  return x;
}

export function quoteDeposit(
  amountAeth: bigint,
  reserveAeth: bigint,
  reserveUsdc: bigint,
  lpSupply: bigint,
): { amountUsdc: bigint; lpOut: bigint } {
  if (amountAeth <= 0n || reserveAeth <= 0n || reserveUsdc <= 0n || lpSupply <= 0n) {
    return { amountUsdc: 0n, lpOut: 0n };
  }
  // Shares follow the AETH side, rounded down; the USDC side is rounded up,
  // so a deposit never adds less than its shares are worth.
  const amountUsdc = ceilDiv(amountAeth * reserveUsdc, reserveAeth);
  const lpOut = (amountAeth * lpSupply) / reserveAeth;
  return { amountUsdc, lpOut };
}

function ceilDiv(a: bigint, b: bigint): bigint {
  return (a + b - 1n) / b;
}

/** Largest AETH deposit the wallet can match at the current ratio. */
export function maxDepositAeth(
  walletAeth: bigint,
  walletUsdc: bigint,
  reserveAeth: bigint,
  reserveUsdc: bigint,
): bigint {
  if (reserveAeth <= 0n || reserveUsdc <= 0n || walletAeth <= 0n || walletUsdc <= 0n) return 0n;
  const fromUsdc = (walletUsdc * reserveAeth) / reserveUsdc;
  let cap = walletAeth < fromUsdc ? walletAeth : fromUsdc;
  if (cap > 0n && ceilDiv(cap * reserveUsdc, reserveAeth) > walletUsdc) cap -= 1n;
  return cap < 0n ? 0n : cap;
}

export function quoteWithdraw(
  lpIn: bigint,
  reserveAeth: bigint,
  reserveUsdc: bigint,
  lpSupply: bigint,
): { amountAeth: bigint; amountUsdc: bigint } {
  if (lpIn <= 0n || lpSupply <= 0n) return { amountAeth: 0n, amountUsdc: 0n };
  return {
    amountAeth: (lpIn * reserveAeth) / lpSupply,
    amountUsdc: (lpIn * reserveUsdc) / lpSupply,
  };
}
