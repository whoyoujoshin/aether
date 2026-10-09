// Aether for Coinbase AgentKit: an action provider to pass with the others.
//
//   import { AgentKit } from "@coinbase/agentkit";
//   import { aetherActionProvider } from "aether-chain-client/agentkit";
//   const agentkit = await AgentKit.from({ walletProvider, actionProviders: [aetherActionProvider(kit), ...] });
//
// It pays from the Aether key in the toolkit, within its limits, whatever
// network the AgentKit wallet is on: Aether isn't one of AgentKit's networks.

import type { AetherToolkit } from "./agentTools.js";
import { zodTools } from "./zodTools.js";

export function aetherActionProvider(kit: AetherToolkit) {
  return {
    name: "aether",
    actionProviders: [],
    supportsNetwork: () => true,
    // AgentKit actions answer with a string: the result as JSON.
    getActions: () => zodTools(kit).map((t) => ({
      name: t.name, description: t.description, schema: t.schema,
      invoke: async (args: Record<string, unknown>) => JSON.stringify(await t.run(args)),
    })),
  };
}
