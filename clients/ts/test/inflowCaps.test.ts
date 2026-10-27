import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { AetherClient, AetherToolkit, decodeInflowCaps, inflowRoom, INFLOW_CAPS_PATH, mcpTools } from "../src/index.js";

// clients/testdata/inflowcaps.json: Caps answers as the node encodes them
// (x/inflowcap's TestCapsVectors checks the same file against the Go types).
const file = JSON.parse(readFileSync(new URL("../../../testdata/inflowcaps.json", import.meta.url), "utf8"));

type Row = { channel: string; denom: string; voucherDenom: string; cap: string; supply: string; room: string; inFlight: string };

test("inflow caps: the shared vectors decode as the node's Go types do", () => {
  for (const v of file.vectors as { name: string; value: string; expected: Row[] }[]) {
    const got = decodeInflowCaps(Buffer.from(v.value, "base64")).map((c) => ({
      channel: c.channel, denom: c.denom, voucherDenom: c.voucherDenom, cap: c.cap.toString(), supply: c.supply.toString(), room: c.room.toString(), inFlight: c.inFlight.toString(),
    }));
    assert.deepEqual(got, v.expected, v.name);
  }
});

// A fake node answering abci_query for the Caps path with value, or with
// the code and log a node without x/inflowcap answers.
function node(answer: { value?: string; code?: number; log?: string }, paths: string[] = []): typeof fetch {
  return (async (_input: unknown, init?: RequestInit) => {
    const req = JSON.parse(String(init?.body));
    paths.push(req.params.path);
    return new Response(JSON.stringify({ jsonrpc: "2.0", id: req.id, result: { response: { code: answer.code ?? 0, log: answer.log ?? "", value: answer.value ?? null } } }));
  }) as typeof fetch;
}

test("inflow caps: client and toolkit, active and not", async () => {
  const testnet = file.vectors[1];
  const paths: string[] = [];
  const client = new AetherClient({ rpc: "http://node.test", fetch: node({ value: testnet.value }, paths), usdcPath: "transfer/channel-2", usdcBaseDenom: "erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d", usdcIssuer: "Injective" });
  const caps = await client.inflowCaps();
  assert.equal(paths[0], INFLOW_CAPS_PATH);
  assert.equal(caps.active, true);
  assert.deepEqual(inflowRoom(caps, testnet.expected[0].voucherDenom), { room: 89_990_000n, limited: true, listed: true });
  assert.deepEqual(inflowRoom(caps, "ibc/OTHER"), { room: 0n, limited: true, listed: false });

  const kit = new AetherToolkit(client, undefined, { faucetUrl: null });
  const r = (await kit.call("aether_get_inflow_caps", {})) as any;
  assert.equal(r.active, true);
  assert.equal(r.caps[0].symbol, "USDC");
  assert.equal(r.caps[0].roomDisplay, "89.99 USDC");
  assert.equal(r.caps[1].symbol, "OSMO");
  assert.equal(r.caps[1].roomDisplay, "0 OSMO");
  assert.match(r.message, /refused/);

  const mcp = mcpTools(kit).find((t) => t.name === "get_inflow_caps")!;
  assert.deepEqual(await mcp.run({}), r, "the MCP tool answers as the toolkit does");

  const old = new AetherClient({ rpc: "http://node.test", fetch: node(file.inactive) });
  assert.deepEqual(await old.inflowCaps(), { active: false, caps: [] }, "no x/inflowcap: no cap, not an error");
  const off = (await new AetherToolkit(old, undefined, { faucetUrl: null }).call("aether_get_inflow_caps", {})) as any;
  assert.equal(off.active, false);
  assert.match(off.message, /no inflow cap yet/);

  const broken = new AetherClient({ rpc: "http://node.test", fetch: node({ code: 1, log: "internal" }) });
  const e = (await new AetherToolkit(broken, undefined, { faucetUrl: null }).call("aether_get_inflow_caps", {})) as any;
  assert.equal(e.error.code, "UNAVAILABLE", "any other failure is an error");
});
