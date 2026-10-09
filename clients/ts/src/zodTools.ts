// The toolkit's tools with zod schemas, for agent frameworks that take one
// (Coinbase AgentKit, GOAT). Loaded only through those subpaths, so zod is
// needed only by people who use them.

import { z } from "zod";
import { AetherToolkit, ToolResult, ToolSpec } from "./agentTools.js";

export interface ZodTool {
  name: string;
  description: string;
  schema: z.ZodObject<z.ZodRawShape>;
  run: (args: Record<string, unknown>) => Promise<ToolResult>;
}

export function zodSchema(spec: ToolSpec): z.ZodObject<z.ZodRawShape> {
  const required = new Set(spec.parameters.required ?? []);
  const shape: z.ZodRawShape = {};
  for (const [name, p] of Object.entries(spec.parameters.properties)) {
    const s = z.string().describe(p.description);
    shape[name] = required.has(name) ? s : s.nullish(); // some models send null for "not given"
  }
  return z.object(shape).strict();
}

export function zodTools(kit: AetherToolkit): ZodTool[] {
  return kit.specs().map((t) => ({
    name: t.name,
    description: t.description,
    schema: zodSchema(t),
    // Through call(), which checks the arguments again: the schema is advice to the framework.
    run: (args) => kit.call(t.name, Object.fromEntries(Object.entries(args ?? {}).filter(([, v]) => v !== undefined && v !== null))),
  }));
}
