#!/usr/bin/env node
// aether-mcp: the read-only Aether MCP server, over stdio (see ../mcpServer.ts).
//
//   npx -y -p aether-chain-client aether-mcp
//
// Settings come from the environment: AETHER_NETWORK (testnet, the default,
// or mainnet, which has no public node yet so needs AETHER_RPC_URL),
// AETHER_RPC_URL (default the network's public node), AETHER_CHAIN_ID
// (default the network's), and
// AETHER_USDC_PATH / AETHER_USDC_BASE_DENOM / AETHER_USDC_ISSUER for a USDC
// other than the testnet's. Only MCP messages go to stdout.

import { readFileSync } from "node:fs";
import { serveMcp } from "../mcpServer.js";

const env = (k: string) => process.env[k]?.trim() || undefined;
let version = "0.0.0";
try {
  version = JSON.parse(readFileSync(new URL("../../../package.json", import.meta.url), "utf8")).version;
} catch {
  // run from source
}

serveMcp({
  rpc: env("AETHER_RPC_URL"),
  chainId: env("AETHER_CHAIN_ID"),
  network: env("AETHER_NETWORK") as "testnet" | "mainnet" | undefined,
  usdcPath: env("AETHER_USDC_PATH"),
  usdcBaseDenom: env("AETHER_USDC_BASE_DENOM"),
  usdcIssuer: env("AETHER_USDC_ISSUER"),
  version,
}).catch((e) => {
  process.stderr.write(`aether-mcp: ${(e as Error).message}\n`);
  process.exit(1);
});
