// Aether for the GOAT SDK: a plugin to pass with the others.
//
//   import { getOnChainTools } from "@goat-sdk/adapter-vercel-ai";
//   import { aether } from "aether-chain-client/goat";
//   const tools = await getOnChainTools({ wallet, plugins: [aether(kit), ...] });
//
// It pays from the Aether key in the toolkit, within its limits, whatever
// chain the GOAT wallet is on.

import type { AetherToolkit } from "./agentTools.js";
import { zodTools } from "./zodTools.js";

export function aether(kit: AetherToolkit) {
  return {
    name: "aether",
    toolProviders: [],
    supportsChain: () => true,
    getTools: () => zodTools(kit).map((t) => ({
      name: t.name, description: t.description, parameters: t.schema,
      execute: (args: Record<string, unknown>) => t.run(args),
    })),
  };
}
