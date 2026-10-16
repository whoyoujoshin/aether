import { useSyncExternalStore } from "react";
import {
  AGENT_LIMIT_AETH,
  AGENT_LIMIT_USDC,
  LP_SUPPLY,
  RESERVE_AETH,
  RESERVE_USDC,
  SEED_PRICES,
  STORAGE_KEY,
  WALLET_AETH,
  WALLET_USDC,
} from "./constants.ts";
import { applySwap, executionPriceE6, quoteDeposit, quoteWithdraw, spotPriceE6 } from "./math.ts";
import type { AgentTurn, Asset, DexState, PricePoint, Trade } from "./types.ts";

function createGenesis(): DexState {
  return {
    v: 2,
    reserveAeth: RESERVE_AETH.toString(),
    reserveUsdc: RESERVE_USDC.toString(),
    lpSupply: LP_SUPPLY.toString(),
    walletAeth: WALLET_AETH.toString(),
    walletUsdc: WALLET_USDC.toString(),
    walletLp: "0",
    slippageBps: 50,
    agentLimitAeth: AGENT_LIMIT_AETH.toString(),
    agentLimitUsdc: AGENT_LIMIT_USDC.toString(),
    trades: [],
    prices: SEED_PRICES.map((point) => ({ ...point })),
    calls: [],
    nextId: 1,
  };
}

const genesis = createGenesis();
let state: DexState = genesis;
const listeners = new Set<() => void>();
let hydrated = false;

function emit() {
  for (const listener of listeners) listener();
}

function persist() {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(state));
  } catch {
    /* private mode or a full quota — the session still works */
  }
}

function commit(next: DexState) {
  state = next;
  persist();
  emit();
}

export function subscribeDex(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function getDexState(): DexState {
  return state;
}

export function getDexServerSnapshot(): DexState {
  return genesis;
}

function isSlip(n: unknown): n is 10 | 50 | 100 {
  return n === 10 || n === 50 || n === 100;
}

function isCore(o: { v?: number; reserveAeth?: string; reserveUsdc?: string; lpSupply?: string; walletAeth?: string; walletUsdc?: string; walletLp?: string; slippageBps?: number; trades?: unknown; prices?: unknown; nextId?: unknown }): boolean {
  return (
    (o.v === 1 || o.v === 2) &&
    typeof o.reserveAeth === "string" &&
    typeof o.reserveUsdc === "string" &&
    typeof o.lpSupply === "string" &&
    typeof o.walletAeth === "string" &&
    typeof o.walletUsdc === "string" &&
    typeof o.walletLp === "string" &&
    isSlip(o.slippageBps) &&
    Array.isArray(o.trades) &&
    Array.isArray(o.prices) &&
    typeof o.nextId === "number"
  );
}

function normalize(raw: unknown): DexState | null {
  if (!raw || typeof raw !== "object") return null;
  const o = raw as {
    v?: number;
    reserveAeth?: string;
    reserveUsdc?: string;
    lpSupply?: string;
    walletAeth?: string;
    walletUsdc?: string;
    walletLp?: string;
    slippageBps?: number;
    agentLimitAeth?: string;
    agentLimitUsdc?: string;
    trades?: Trade[];
    prices?: PricePoint[];
    calls?: AgentTurn[];
    nextId?: number;
  };
  if (!isCore(o)) return null;
  return {
    v: 2,
    reserveAeth: o.reserveAeth as string,
    reserveUsdc: o.reserveUsdc as string,
    lpSupply: o.lpSupply as string,
    walletAeth: o.walletAeth as string,
    walletUsdc: o.walletUsdc as string,
    walletLp: o.walletLp as string,
    slippageBps: o.slippageBps as 10 | 50 | 100,
    agentLimitAeth: typeof o.agentLimitAeth === "string" ? o.agentLimitAeth : AGENT_LIMIT_AETH.toString(),
    agentLimitUsdc: typeof o.agentLimitUsdc === "string" ? o.agentLimitUsdc : AGENT_LIMIT_USDC.toString(),
    trades: o.trades as Trade[],
    prices: o.prices as PricePoint[],
    calls: Array.isArray(o.calls) ? o.calls.slice(0, 24) : [],
    nextId: o.nextId as number,
  };
}

export function hydrateDex() {
  if (hydrated || typeof localStorage === "undefined") return;
  hydrated = true;
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return;
    const parsed: unknown = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object") return;
    const next = normalize(parsed);
    if (!next) return;
    state = next;
    emit();
  } catch {
    /* corrupt snapshot — keep genesis */
  }
}

export type ActionResult = { ok: true; amountOut: bigint } | { ok: false; error: string };

function pushMid(prices: PricePoint[], reserveAeth: bigint, reserveUsdc: bigint): PricePoint[] {
  const mid = spotPriceE6(reserveAeth, reserveUsdc);
  const next = [...prices, { i: prices.length, price: Number(mid) / 1_000_000 }];
  return next.length > 80 ? next.slice(next.length - 80) : next;
}

/**
 * Recomputes the fill at commit and refuses it below `minAmountOut`, as a
 * chain handler would: the reserves may have moved since the quote.
 */
export function swap(tokenIn: Asset, amountIn: bigint, minAmountOut: bigint): ActionResult {
  if (amountIn <= 0n) return { ok: false, error: "Enter an amount" };
  const walletIn = BigInt(tokenIn === "aeth" ? state.walletAeth : state.walletUsdc);
  if (amountIn > walletIn) {
    return { ok: false, error: tokenIn === "aeth" ? "Not enough AETH" : "Not enough USDC" };
  }
  const reserveIn = BigInt(tokenIn === "aeth" ? state.reserveAeth : state.reserveUsdc);
  const reserveOut = BigInt(tokenIn === "aeth" ? state.reserveUsdc : state.reserveAeth);
  let filled: ReturnType<typeof applySwap>;
  try {
    filled = applySwap(reserveIn, reserveOut, amountIn);
  } catch {
    return { ok: false, error: "Pool can't fill that size" };
  }
  if (filled.amountOut < minAmountOut) {
    return { ok: false, error: "slippage_exceeded" };
  }
  const reserveAeth = tokenIn === "aeth" ? filled.reserveIn : filled.reserveOut;
  const reserveUsdc = tokenIn === "aeth" ? filled.reserveOut : filled.reserveIn;
  const price = executionPriceE6(amountIn, filled.amountOut, tokenIn === "aeth");
  const trade: Trade = {
    id: `t${state.nextId.toString()}`,
    ts: Date.now(),
    tokenIn,
    amountIn: amountIn.toString(),
    amountOut: filled.amountOut.toString(),
    priceE6: price.toString(),
  };
  const walletAeth = BigInt(state.walletAeth) + (tokenIn === "aeth" ? -amountIn : filled.amountOut);
  const walletUsdc = BigInt(state.walletUsdc) + (tokenIn === "usdc" ? -amountIn : filled.amountOut);
  commit({
    ...state,
    reserveAeth: reserveAeth.toString(),
    reserveUsdc: reserveUsdc.toString(),
    walletAeth: walletAeth.toString(),
    walletUsdc: walletUsdc.toString(),
    trades: [trade, ...state.trades].slice(0, 40),
    prices: pushMid(state.prices, reserveAeth, reserveUsdc),
    nextId: state.nextId + 1,
  });
  return { ok: true, amountOut: filled.amountOut };
}

export function deposit(amountAeth: bigint): ActionResult {
  if (amountAeth <= 0n) return { ok: false, error: "Enter an amount" };
  const reserveAeth = BigInt(state.reserveAeth);
  const reserveUsdc = BigInt(state.reserveUsdc);
  const supply = BigInt(state.lpSupply);
  const walletAeth = BigInt(state.walletAeth);
  const walletUsdc = BigInt(state.walletUsdc);
  if (amountAeth > walletAeth) return { ok: false, error: "Not enough AETH" };
  const quoted = quoteDeposit(amountAeth, reserveAeth, reserveUsdc, supply);
  if (quoted.amountUsdc <= 0n || quoted.lpOut <= 0n) return { ok: false, error: "Amount is too small" };
  if (quoted.amountUsdc > walletUsdc) return { ok: false, error: "Not enough USDC for the pool ratio" };
  const nextAeth = reserveAeth + amountAeth;
  const nextUsdc = reserveUsdc + quoted.amountUsdc;
  commit({
    ...state,
    reserveAeth: nextAeth.toString(),
    reserveUsdc: nextUsdc.toString(),
    lpSupply: (supply + quoted.lpOut).toString(),
    walletAeth: (walletAeth - amountAeth).toString(),
    walletUsdc: (walletUsdc - quoted.amountUsdc).toString(),
    walletLp: (BigInt(state.walletLp) + quoted.lpOut).toString(),
    prices: pushMid(state.prices, nextAeth, nextUsdc),
  });
  return { ok: true, amountOut: quoted.lpOut };
}

export function withdraw(lpIn: bigint): ActionResult {
  const have = BigInt(state.walletLp);
  if (lpIn <= 0n) return { ok: false, error: "No shares selected" };
  if (lpIn > have) return { ok: false, error: "Not enough shares" };
  const reserveAeth = BigInt(state.reserveAeth);
  const reserveUsdc = BigInt(state.reserveUsdc);
  const supply = BigInt(state.lpSupply);
  const out = quoteWithdraw(lpIn, reserveAeth, reserveUsdc, supply);
  if (out.amountAeth <= 0n && out.amountUsdc <= 0n) return { ok: false, error: "Amount is too small" };
  const nextAeth = reserveAeth - out.amountAeth;
  const nextUsdc = reserveUsdc - out.amountUsdc;
  commit({
    ...state,
    reserveAeth: nextAeth.toString(),
    reserveUsdc: nextUsdc.toString(),
    lpSupply: (supply - lpIn).toString(),
    walletAeth: (BigInt(state.walletAeth) + out.amountAeth).toString(),
    walletUsdc: (BigInt(state.walletUsdc) + out.amountUsdc).toString(),
    walletLp: (have - lpIn).toString(),
    prices: pushMid(state.prices, nextAeth, nextUsdc),
  });
  return { ok: true, amountOut: lpIn };
}

export function setSlippage(bps: 10 | 50 | 100) {
  if (state.slippageBps === bps) return;
  commit({ ...state, slippageBps: bps });
}

export function setAgentLimit(asset: Asset, amount: bigint) {
  commit({
    ...state,
    agentLimitAeth: asset === "aeth" ? amount.toString() : state.agentLimitAeth,
    agentLimitUsdc: asset === "usdc" ? amount.toString() : state.agentLimitUsdc,
  });
}

export function appendTurn(turn: Omit<AgentTurn, "id">): AgentTurn {
  const full: AgentTurn = { ...turn, id: `a${state.nextId.toString()}` };
  commit({
    ...state,
    nextId: state.nextId + 1,
    calls: [full, ...state.calls].slice(0, 24),
  });
  return full;
}

export function resetDemo() {
  state = createGenesis();
  try {
    localStorage.removeItem(STORAGE_KEY);
  } catch {
    /* ignore */
  }
  emit();
}

export function useDex(): DexState {
  return useSyncExternalStore(subscribeDex, getDexState, getDexServerSnapshot);
}
