/**
 * Tool surface for the DEX agent. An instruction becomes one call.
 * Settlement still goes through the integer AMM. The agent refuses a
 * spend over its per-tx limit, and a high-impact swap unless the
 * instruction ends in `confirm`.
 */

import { AGENT_LIMIT_AETH, AGENT_LIMIT_USDC, HIGH_IMPACT_BPS, USDC_IBC_DENOM } from "./constants.ts";
import { formatBase, formatPrice, parseBase } from "./format.ts";
import {
  feePaid,
  minOut,
  priceImpactBps,
  quoteDeposit,
  quoteExactIn,
  quoteWithdraw,
  spotPriceE6,
} from "./math.ts";
import { appendTurn, deposit, getDexState, setAgentLimit, setSlippage, swap, withdraw } from "./store.ts";
import type { AgentTurn, Asset, DexState } from "./types.ts";

export const INSTRUCTION_HINT =
  "pool | quote <amount> <aeth|usdc> to <aeth|usdc> | swap <amount> <aeth|usdc> to <aeth|usdc> [confirm] | deposit <amount> aeth | withdraw <25|50|75|100> | limit <amount> <aeth|usdc> | slippage <0.10|0.50|1.00>";

const ASSET = "aeth|usdc";
const AMOUNT = "\\d+(?:\\.\\d+)?";

type ToolCall = NonNullable<AgentTurn["call"]>;
type Result = AgentTurn["result"];

type Effect =
  | { type: "swap"; tokenIn: Asset; amountIn: bigint }
  | { type: "deposit"; amountAeth: bigint }
  | { type: "withdraw"; lpIn: bigint }
  | { type: "limit"; asset: Asset; amount: bigint }
  | { type: "slippage"; bps: 10 | 50 | 100 };

type Decision = { call: ToolCall; result: Result; effect: Effect | null };

export function parseInstruction(input: string): ToolCall | { error: string } {
  const text = input.trim().replace(/\s+/g, " ").toLowerCase();
  if (text === "pool") return { tool: "get_pool", args: {} };

  const quote = new RegExp(`^(quote|swap)\\s+(${AMOUNT})\\s+(${ASSET})\\s+to\\s+(${ASSET})(?:\\s+(confirm))?$`).exec(text);
  if (quote) {
    const tool = quote[1] === "quote" ? "quote_swap" : "swap";
    const offer = quote[3] as Asset;
    const ask = quote[4] as Asset;
    if (offer === ask) return { error: "offer_and_ask_must_differ" };
    const amount = parseBase(quote[2] ?? "");
    if (amount === null || amount <= 0n) return { error: "bad_amount" };
    return {
      tool,
      args: {
        offer_denom: denom(offer),
        offer_amount: amount.toString(),
        ask_denom: denom(ask),
        confirm: quote[5] === "confirm",
      },
    };
  }

  const dep = new RegExp(`^deposit\\s+(${AMOUNT})\\s+aeth$`).exec(text);
  if (dep) {
    const amount = parseBase(dep[1] ?? "");
    if (amount === null || amount <= 0n) return { error: "bad_amount" };
    return { tool: "deposit", args: { aeth: amount.toString() } };
  }

  const wd = /^withdraw\s+(25|50|75|100)%?$/.exec(text);
  if (wd) return { tool: "withdraw", args: { percent: Number(wd[1]) } };

  const limit = new RegExp(`^limit\\s+(${AMOUNT})\\s+(${ASSET})$`).exec(text);
  if (limit) {
    const amount = parseBase(limit[1] ?? "");
    if (amount === null || amount <= 0n) return { error: "bad_amount" };
    return { tool: "set_limit", args: { asset: limit[2] ?? "aeth", amount: amount.toString() } };
  }

  const slip = /^slippage\s+(0\.10|0\.50|1\.00|0\.1|0\.5|1)$/.exec(text);
  if (slip) {
    const bps = slip[1] === "0.10" || slip[1] === "0.1" ? 10 : slip[1] === "1.00" || slip[1] === "1" ? 100 : 50;
    return { tool: "set_slippage", args: { slippage_bps: bps } };
  }

  return { error: "unknown_instruction" };
}

export function decide(call: ToolCall, state: DexState): Decision {
  if (call.tool === "get_pool") {
    const reserveAeth = BigInt(state.reserveAeth);
    const reserveUsdc = BigInt(state.reserveUsdc);
    return {
      call,
      effect: null,
      result: {
        ok: true,
        reserve_aeth: state.reserveAeth,
        reserve_usdc: state.reserveUsdc,
        mid_usdc_per_aeth: formatPrice(spotPriceE6(reserveAeth, reserveUsdc)),
        agent_aeth: state.walletAeth,
        agent_usdc: state.walletUsdc,
        limit_aeth: state.agentLimitAeth,
        limit_usdc: state.agentLimitUsdc,
        fee_bps: 30,
      },
    };
  }

  if (call.tool === "quote_swap" || call.tool === "swap") {
    return decideSwap(call, state);
  }

  if (call.tool === "deposit") {
    const amountAeth = BigInt(String(call.args.aeth ?? "0"));
    const quoted = quoteDeposit(amountAeth, BigInt(state.reserveAeth), BigInt(state.reserveUsdc), BigInt(state.lpSupply));
    const over = amountAeth > BigInt(state.agentLimitAeth) || quoted.amountUsdc > BigInt(state.agentLimitUsdc);
    if (over) {
      return { call, effect: null, result: { ok: false, error: "over_per_tx_limit", aeth: amountAeth.toString(), usdc: quoted.amountUsdc.toString() } };
    }
    if (amountAeth > BigInt(state.walletAeth) || quoted.amountUsdc > BigInt(state.walletUsdc) || quoted.lpOut <= 0n) {
      return { call, effect: null, result: { ok: false, error: "rejected" } };
    }
    return {
      call,
      effect: { type: "deposit", amountAeth },
      result: { ok: true, usdc: quoted.amountUsdc.toString(), shares: quoted.lpOut.toString(), usdc_display: formatBase(quoted.amountUsdc) },
    };
  }

  if (call.tool === "withdraw") {
    const percent = Number(call.args.percent ?? 0);
    const lpIn = (BigInt(state.walletLp) * BigInt(percent)) / 100n;
    const out = quoteWithdraw(lpIn, BigInt(state.reserveAeth), BigInt(state.reserveUsdc), BigInt(state.lpSupply));
    if (lpIn <= 0n) return { call, effect: null, result: { ok: false, error: "no_shares" } };
    return {
      call,
      effect: { type: "withdraw", lpIn },
      result: { ok: true, aeth: out.amountAeth.toString(), usdc: out.amountUsdc.toString() },
    };
  }

  if (call.tool === "set_limit") {
    const asset = call.args.asset === "usdc" ? "usdc" : "aeth";
    const amount = BigInt(String(call.args.amount ?? "0"));
    return { call, effect: { type: "limit", asset, amount }, result: { ok: true, asset, amount: amount.toString(), display: formatBase(amount) } };
  }

  if (call.tool === "set_slippage") {
    const bps = call.args.slippage_bps;
    if (bps !== 10 && bps !== 50 && bps !== 100) return { call, effect: null, result: { ok: false, error: "bad_slippage" } };
    return { call, effect: { type: "slippage", bps }, result: { ok: true, slippage_bps: bps } };
  }

  return { call, effect: null, result: { ok: false, error: "unknown_tool" } };
}

function decideSwap(call: ToolCall, state: DexState): Decision {
  const offer = assetFromDenom(String(call.args.offer_denom ?? ""));
  const ask = assetFromDenom(String(call.args.ask_denom ?? ""));
  const amountIn = BigInt(String(call.args.offer_amount ?? "0"));
  const confirm = call.args.confirm === true;
  if (!offer || !ask || offer === ask) return { call, effect: null, result: { ok: false, error: "bad_pair" } };

  const reserveIn = BigInt(offer === "aeth" ? state.reserveAeth : state.reserveUsdc);
  const reserveOut = BigInt(offer === "aeth" ? state.reserveUsdc : state.reserveAeth);
  const balance = BigInt(offer === "aeth" ? state.walletAeth : state.walletUsdc);
  const limit = BigInt(offer === "aeth" ? state.agentLimitAeth : state.agentLimitUsdc);
  const amountOut = quoteExactIn(amountIn, reserveIn, reserveOut);
  const impact = amountOut > 0n ? priceImpactBps(amountIn, amountOut, reserveIn, reserveOut) : 0n;
  const floor = amountOut > 0n ? minOut(amountOut, BigInt(state.slippageBps)) : 0n;
  const quoted: Result = {
    ok: true,
    amount_out: amountOut.toString(),
    amount_out_display: formatBase(amountOut),
    min_receive: floor.toString(),
    price_impact_bps: Number(impact),
    fee: feePaid(amountIn).toString(),
    needs_confirm: impact >= HIGH_IMPACT_BPS,
    over_per_tx_limit: amountIn > limit,
  };

  if (call.tool === "quote_swap") return { call: withMin(call, floor), effect: null, result: quoted };
  if (amountIn <= 0n || amountOut <= 0n || amountIn > balance) {
    return { call, effect: null, result: { ok: false, error: "rejected" } };
  }
  if (amountIn > limit) return { call, effect: null, result: { ok: false, error: "over_per_tx_limit", limit: limit.toString(), offer_amount: amountIn.toString() } };
  if (impact >= HIGH_IMPACT_BPS && !confirm) {
    return { call: withMin(call, floor), effect: null, result: { ok: false, error: "needs_confirm", price_impact_bps: Number(impact), min_receive: floor.toString() } };
  }
  return { call: withMin(call, floor), effect: { type: "swap", tokenIn: offer, amountIn }, result: quoted };
}

function withMin(call: ToolCall, floor: bigint): ToolCall {
  return { ...call, args: { ...call.args, min_receive: floor.toString() } };
}

export function runInstruction(raw: string): AgentTurn {
  const instruction = raw.trim().replace(/\s+/g, " ");
  const parsed = parseInstruction(instruction);
  if ("error" in parsed) {
    return appendTurn({
      ts: Date.now(),
      instruction,
      call: null,
      result: { ok: false, error: parsed.error, hint: INSTRUCTION_HINT },
    });
  }
  const decision = decide(parsed, getDexState());
  if (decision.effect?.type === "swap") swap(decision.effect.tokenIn, decision.effect.amountIn);
  if (decision.effect?.type === "deposit") deposit(decision.effect.amountAeth);
  if (decision.effect?.type === "withdraw") withdraw(decision.effect.lpIn);
  if (decision.effect?.type === "limit") setAgentLimit(decision.effect.asset, decision.effect.amount);
  if (decision.effect?.type === "slippage") setSlippage(decision.effect.bps);
  return appendTurn({
    ts: Date.now(),
    instruction,
    call: decision.call,
    result: decision.result,
  });
}

export function denom(asset: Asset): string {
  return asset === "aeth" ? "uaeth" : USDC_IBC_DENOM;
}

function assetFromDenom(value: string): Asset | null {
  if (value === "uaeth" || value === "aeth") return "aeth";
  if (value === USDC_IBC_DENOM || value === "usdc") return "usdc";
  return null;
}

export const DEFAULT_LIMITS = { aeth: AGENT_LIMIT_AETH, usdc: AGENT_LIMIT_USDC };
