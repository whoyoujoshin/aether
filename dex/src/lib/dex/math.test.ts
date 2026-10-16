import assert from "node:assert/strict";
import test from "node:test";
import { RESERVE_AETH, RESERVE_USDC, TEST_VECTOR } from "./constants.ts";
import {
  FEE_BPS,
  applySwap,
  isqrt,
  priceImpactBps,
  quoteDeposit,
  quoteExactIn,
  quoteWithdraw,
  spotPriceE6,
} from "./math.ts";

test("published 1 AETH vector", () => {
  const out = quoteExactIn(TEST_VECTOR.amountIn, RESERVE_AETH, RESERVE_USDC);
  assert.equal(out, TEST_VECTOR.amountOut);
});

test("spot mid is 0.40 USDC per AETH", () => {
  assert.equal(spotPriceE6(RESERVE_AETH, RESERVE_USDC), 400_000n);
});

test("fee increases k and a round trip loses value", () => {
  const amountIn = 1_000_000_000n;
  const sold = applySwap(RESERVE_AETH, RESERVE_USDC, amountIn);
  assert.ok(sold.reserveIn * sold.reserveOut > RESERVE_AETH * RESERVE_USDC);
  const bought = applySwap(sold.reserveOut, sold.reserveIn, sold.amountOut);
  assert.ok(bought.amountOut < amountIn);
});

test("the other direction is the same function", () => {
  const aethOut = quoteExactIn(1_000_000n, RESERVE_USDC, RESERVE_AETH);
  assert.ok(aethOut > 2_000_000n);
  assert.ok(aethOut < 2_500_000n);
});

test("price impact excludes the flat fee and grows with size", () => {
  const smallIn = 100_000_000n;
  const largeIn = 5_000_000_000n;
  const smallOut = quoteExactIn(smallIn, RESERVE_AETH, RESERVE_USDC);
  const largeOut = quoteExactIn(largeIn, RESERVE_AETH, RESERVE_USDC);
  const smallImpact = priceImpactBps(smallIn, smallOut, RESERVE_AETH, RESERVE_USDC);
  const largeImpact = priceImpactBps(largeIn, largeOut, RESERVE_AETH, RESERVE_USDC);
  assert.equal(largeImpact, 906n);
  assert.ok(largeImpact > smallImpact);
  assert.ok(smallImpact < FEE_BPS);
  assert.equal(priceImpactBps(TEST_VECTOR.amountIn, TEST_VECTOR.amountOut, RESERVE_AETH, RESERVE_USDC), 0n);
});

test("deposit then withdraw rounds in favor of the pool", () => {
  const supply = isqrt(RESERVE_AETH * RESERVE_USDC);
  const amountAeth = 1_000_000_000n;
  const quoted = quoteDeposit(amountAeth, RESERVE_AETH, RESERVE_USDC, supply);
  assert.equal(quoted.amountUsdc, 400_000_000n);
  assert.ok(quoted.lpOut > 0n);
  const back = quoteWithdraw(
    quoted.lpOut,
    RESERVE_AETH + amountAeth,
    RESERVE_USDC + quoted.amountUsdc,
    supply + quoted.lpOut,
  );
  assert.ok(back.amountAeth <= amountAeth);
  assert.ok(back.amountUsdc <= quoted.amountUsdc);
});

test("non-positive input quotes zero", () => {
  assert.equal(quoteExactIn(0n, RESERVE_AETH, RESERVE_USDC), 0n);
  assert.equal(quoteExactIn(-1n, RESERVE_AETH, RESERVE_USDC), 0n);
});
