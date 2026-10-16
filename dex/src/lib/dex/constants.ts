import { isqrt } from "./math.ts";
import type { PricePoint } from "./types.ts";

export const CHAIN_ID = "aether-testnet-1";
export const AETH_DISPLAY = "aeth";
export const AETH_DENOM = "uaeth";
export const USDC_DECIMALS = 6;

/** Injective testnet (`injective-888`) bank denom. EIP-55 case is significant. */
export const USDC_BASE_DENOM = "erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d";
export const USDC_PATH = "transfer/channel-2";
export const USDC_TRACE = `${USDC_PATH}/${USDC_BASE_DENOM}`;

/**
 * ICS-20 denom registered in aether-testnet-1 bank metadata.
 * SHA-256 of `transfer/channel-2/erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d`.
 */
export const USDC_IBC_DENOM =
  "ibc/064D82A67318DD30F54A4E17B6E487C2864E8B075A5871D3ACAD7A7129F32C5C";

/** Documented counterparty for channel-2. See docs/USDC-PLAN.md. */
export const USDC_COUNTERPARTY = "injective-888 channel-77152";

export const SCALE = 1_000_000n;
export const RESERVE_AETH = 50_000n * SCALE;
export const RESERVE_USDC = 20_000n * SCALE;
export const AGENT_LIMIT_AETH = 2_000n * SCALE;
export const AGENT_LIMIT_USDC = 800n * SCALE;
export const WALLET_AETH = 12_000n * SCALE;
export const WALLET_USDC = 4_800n * SCALE;
export const SNAPSHOT_PRICE_E6 = (RESERVE_USDC * SCALE) / RESERVE_AETH;
export const LP_SUPPLY = isqrt(RESERVE_AETH * RESERVE_USDC);

/** 5% size impact, past the flat fee, asks for a second click. */
export const HIGH_IMPACT_BPS = 500n;

export const STORAGE_KEY = "aether-dex-ref-v1";

/** Checked by math.test.ts. Do not derive this from quoteExactIn. */
export const TEST_VECTOR = {
  amountIn: 1_000_000n,
  amountOut: 398792n,
} as const;

function buildSeed(): PricePoint[] {
  let s = 20261015;
  const rand = () => {
    s = (Math.imul(s, 1664525) + 1013904223) >>> 0;
    return s / 4294967296;
  };
  const points: PricePoint[] = [];
  let price = 0.386;
  for (let i = 0; i < 47; i++) {
    price += (rand() - 0.48) * 0.006;
    if (price < 0.36) price = 0.36;
    if (price > 0.44) price = 0.44;
    points.push({ i, price: Math.round(price * 1e6) / 1e6 });
  }
  points.push({ i: 47, price: 0.4 });
  return points;
}

/** Illustrative path that ends on the snapshot mid. Not chain history. */
export const SEED_PRICES: readonly PricePoint[] = buildSeed();
