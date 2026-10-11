import { test } from "node:test";
import assert from "node:assert/strict";
import { PassThrough } from "node:stream";
import { AETH, AetherClient, AetherToolkit, handleMcpMessage, mcpTools, serveMcp } from "../src/index.js";

function tools() {
  const client = new AetherClient({ rpc: "http://127.0.0.1:1" });
  client.balances = async () => [{ asset: AETH, amount: 2_500_000n }];
  return mcpTools(new AetherToolkit(client, undefined, { faucetUrl: null }));
}

const ADDR = "aether1e6r6g8mat6aqax9l29wwmrfhp52hjzrpwevqqq9tcj0lm8egdhjs9q44xl";

test("aether-mcp: initialize negotiates the version; notifications get no reply", async () => {
  const t = tools();
  const init = (await handleMcpMessage(t, { jsonrpc: "2.0", id: 1, method: "initialize", params: { protocolVersion: "2025-03-26" } }, "9.9.9"))!;
  const r = init.result as { protocolVersion: string; capabilities: unknown; serverInfo: { version: string } };
  assert.equal(r.protocolVersion, "2025-03-26");
  assert.equal(r.serverInfo.version, "9.9.9");
  assert.deepEqual(r.capabilities, { tools: { listChanged: false } });
  const newer = (await handleMcpMessage(t, { jsonrpc: "2.0", id: 2, method: "initialize", params: { protocolVersion: "2099-01-01" } }, "1"))!;
  assert.equal((newer.result as { protocolVersion: string }).protocolVersion, "2025-06-18", "an unknown version gets our latest");
  assert.equal(await handleMcpMessage(t, { jsonrpc: "2.0", method: "notifications/initialized" }, "1"), undefined);
  assert.deepEqual(await handleMcpMessage(t, { jsonrpc: "2.0", id: 3, method: "ping" }, "1"), { jsonrpc: "2.0", id: 3, result: {} });
  assert.equal(((await handleMcpMessage(t, { jsonrpc: "2.0", id: 4, method: "resources/list" }, "1"))!.error as { code: number }).code, -32601);
});

test("aether-mcp: three read-only tools, and nothing that spends", async () => {
  const t = tools();
  const list = (await handleMcpMessage(t, { jsonrpc: "2.0", id: 1, method: "tools/list" }, "1"))!;
  const listed = (list.result as { tools: { name: string; annotations: { readOnlyHint: boolean; destructiveHint: boolean }; run?: unknown }[] }).tools;
  assert.deepEqual(listed.map((x) => x.name), ["get_balance", "get_transaction_status", "find_services"]);
  for (const x of listed) {
    assert.equal(x.annotations.readOnlyHint, true);
    assert.equal(x.annotations.destructiveHint, false);
    assert.equal(x.run, undefined, "handlers stay server-side");
  }
});

test("aether-mcp: tool calls return structured results; failures are tool errors", async () => {
  const t = tools();
  const call = async (name: string, args: unknown) => (await handleMcpMessage(t, { jsonrpc: "2.0", id: 7, method: "tools/call", params: { name, arguments: args as Record<string, unknown> } }, "1"))!;
  const ok = (await call("get_balance", { address: ADDR })).result as { isError: boolean; structuredContent: { balances: { amount: string }[] }; content: { text: string }[] };
  assert.equal(ok.isError, false);
  assert.equal(ok.structuredContent.balances[0].amount, "2.5");
  assert.deepEqual(JSON.parse(ok.content[0].text), ok.structuredContent);
  const missing = (await call("get_balance", {})).result as { isError: boolean; structuredContent: { error: { code: string } } };
  assert.equal(missing.isError, true);
  assert.equal(missing.structuredContent.error.code, "INVALID_ARGUMENT");
  const extra = (await call("find_services", { nope: 1 })).result as { structuredContent: { error: { code: string } } };
  assert.equal(extra.structuredContent.error.code, "INVALID_ARGUMENT");
  assert.equal(((await call("send_aeth", {})).error as { code: number }).code, -32602);
});

test("aether-mcp: the stdio loop answers line by line and ends with its input", async () => {
  const input = new PassThrough();
  const output = new PassThrough();
  let text = "";
  output.on("data", (d) => (text += d));
  const done = serveMcp({ rpc: "http://127.0.0.1:1", input, output, version: "1.2.3" });
  input.write(JSON.stringify({ jsonrpc: "2.0", id: 1, method: "initialize", params: { protocolVersion: "2025-06-18" } }) + "\n");
  input.write(JSON.stringify({ jsonrpc: "2.0", method: "notifications/initialized" }) + "\n");
  input.write("not json\n");
  input.write(JSON.stringify({ jsonrpc: "2.0", id: 2, method: "tools/list" }) + "\n");
  input.end();
  await done;
  const replies = text.trim().split("\n").map((l) => JSON.parse(l));
  assert.equal(replies.length, 3);
  assert.equal(replies.find((r) => r.id === 1).result.serverInfo.version, "1.2.3");
  assert.equal(replies.find((r) => r.id === null).error.code, -32700);
  assert.equal(replies.find((r) => r.id === 2).result.tools.length, 3);
});
