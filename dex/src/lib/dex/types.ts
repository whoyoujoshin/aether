export type Asset = "aeth" | "usdc";

export type Trade = {
  id: string;
  ts: number;
  tokenIn: Asset;
  amountIn: string;
  amountOut: string;
  priceE6: string;
};

export type PricePoint = {
  i: number;
  price: number;
};

export type AgentTurn = {
  id: string;
  ts: number;
  instruction: string;
  call: {
    tool: string;
    args: Record<string, string | number | boolean>;
  } | null;
  result: Record<string, string | number | boolean>;
};

export type DexState = {
  v: 2;
  reserveAeth: string;
  reserveUsdc: string;
  lpSupply: string;
  walletAeth: string;
  walletUsdc: string;
  walletLp: string;
  slippageBps: 10 | 50 | 100;
  agentLimitAeth: string;
  agentLimitUsdc: string;
  trades: Trade[];
  prices: PricePoint[];
  calls: AgentTurn[];
  nextId: number;
};