import assert from "node:assert/strict";
import test from "node:test";
import { AGENT_LIMIT_AETH, RESERVE_AETH, RESERVE_USDC, TEST_VECTOR, WALLET_AETH, WALLET_USDC } from "./constants.ts";
import { decide, parseInstruction } from "./agent.ts";
import type { DexState } from "./types.ts";

function fixture(limitAeth = AGENT_LIMIT_AETH): DexState {
  return {
    v: 2,
    reserveAeth: RESERVE_AETH.toString(),
    reserveUsdc: RESERVE_USDC.toString(),
    lpSupply: "1",
    walletAeth: WALLET_AETH.toString(),
    walletUsdc: WALLET_USDC.toString(),
    walletLp: "0",
    slippageBps: 50,
    agentLimitAeth: limitAeth.toString(),
    agentLimitUsdc: (800_000_000n).toString(),
    trades: [],
    prices: [],
    calls: [],
    nextId: 1,
  };
}

test("quote instruction is a tool call", () => {
  const parsed = parseInstruction("quote 1 aeth to usdc");
  assert.ok(!("error" in parsed));
  if ("error" in parsed) return;
  assert.equal(parsed.tool, "quote_swap");
  assert.equal(parsed.args.offer_amount, TEST_VECTOR.amountIn.toString());
});

test("quote matches the published vector and does not settle", () => {
  const parsed = parseInstruction("quote 1 aeth to usdc");
  if ("error" in parsed) throw new Error(parsed.error);
  const decision = decide(parsed, fixture());
  assert.equal(decision.effect, null);
  assert.equal(decision.result.amount_out, TEST_VECTOR.amountOut.toString());
  assert.equal(decision.result.needs_confirm, false);
});

test("swap over the per-tx limit does not settle", () => {
  const parsed = parseInstruction("swap 5000 aeth to usdc");
  if ("error" in parsed) throw new Error(parsed.error);
  const decision = decide(parsed, fixture());
  assert.equal(decision.effect, null);
  assert.equal(decision.result.error, "over_per_tx_limit");
});

test("high impact waits for confirm", () => {
  const open = parseInstruction("swap 5000 aeth to usdc");
  const armed = parseInstruction("swap 5000 aeth to usdc confirm");
  if ("error" in open || "error" in armed) throw new Error("parse");
  const raised = fixture(12_000_000_000n);
  assert.equal(decide(open, raised).result.error, "needs_confirm");
  assert.equal(decide(open, raised).effect, null);
  assert.equal(decide(armed, raised).effect?.type, "swap");
});

test("unknown instruction is rejected", () => {
  const parsed = parseInstruction("buy the dip");
  assert.ok("error" in parsed);
});
