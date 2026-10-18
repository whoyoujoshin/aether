import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { AetherClient, AetherToolkit, SwapQuoter, handleMcpMessage, mcpTools } from "../src/index.js";

// clients/testdata/swapquote.json: Osmosis's recorded answers for pool 1370
// and what agentmcp's quote_swap makes of them; this client must agree.
const file = JSON.parse(readFileSync(new URL("../../../testdata/swapquote.json", import.meta.url), "utf8"));

type Vector = { name: string; pool: unknown; estimate: unknown; input: { amount: string; slippagePercent?: number }; want: Record<string, string> };

function osmosis(v: Pick<Vector, "pool" | "estimate">, calls: string[] = [], status = 200): typeof fetch {
  return (async (input: string | URL) => {
    const u = new URL(String(input));
    calls.push(u.pathname + u.search);
    if (status !== 200) return new Response(JSON.stringify({ code: 3, message: "token amount calculated (0) must be positive: invalid request" }), { status });
    if (u.pathname === "/osmosis/poolmanager/v1beta1/pools/1370") return new Response(JSON.stringify(v.pool));
    if (u.pathname === "/osmosis/poolmanager/v1beta1/1370/estimate/single_pool_swap_exact_amount_in") return new Response(JSON.stringify(v.estimate));
    return new Response("not found", { status: 404 });
  }) as typeof fetch;
}

test("quote_swap: the shared vectors, as agentmcp quotes them", async () => {
  for (const v of file.vectors as Vector[]) {
    const q = new SwapQuoter({ rest: ["http://osmosis.test"], fetch: osmosis(v) });
    const r = (await q.quote(v.input)) as any;
    assert.equal(r.error, undefined, `${v.name}: ${JSON.stringify(r.error)}`);
    const got: Record<string, string> = {
      out_uosmo: r.out.uosmo, out_osmo: r.out.osmo, denom_on_aether: r.out.denomOnAether,
      spot_price: r.spotPrice, price: r.price, cost_percent: r.costPercent, swap_fee_percent: r.swapFeePercent,
      reserves_aeth: r.reserves.aeth, reserves_osmo: r.reserves.osmo,
      slippage_percent: r.slippagePercent, min_output_uosmo: r.minOutput.uosmo,
    };
    assert.deepEqual(got, v.want, v.name);
  }
});

test("quote_swap: the request, the cache, pacing and fallback", async () => {
  const v = file.vectors[0] as Vector;
  const calls: string[] = [];
  let now = 1_000_000;
  const q = new SwapQuoter({ rest: ["http://down.test", "http://osmosis.test/"], now: () => now, fetch: (async (input: string | URL, init?: RequestInit) => {
    if (String(input).startsWith("http://down.test")) return new Response("", { status: 403 });
    return osmosis(v, calls)(input, init);
  }) as typeof fetch });
  await q.quote({ amount: "1 AETH" });
  await q.quote({ amount: "1 AETH" });
  assert.deepEqual(calls, [
    "/osmosis/poolmanager/v1beta1/pools/1370",
    "/osmosis/poolmanager/v1beta1/1370/estimate/single_pool_swap_exact_amount_in?pool_id=1370&token_in=1000000ibc%2F8277E1557EB379889BA54DFD0F19EDCFF907E4B3414E6B99D098F241CAED3BE1&token_out_denom=uosmo",
  ], "the second call is cached; the refusing endpoint is skipped");
  for (let i = 0; i < 40; i++) await q.quote({ amount: `${i + 2} AETH` });
  const paced = (await q.quote({ amount: "99 AETH" })) as any;
  assert.equal(paced.error.code, "QUOTE_UNAVAILABLE");
  assert.ok(paced.error.retryAfterSeconds >= 1 && paced.error.retryAfterSeconds <= 61);
  now += 61_000;
  assert.equal(((await q.quote({ amount: "99 AETH" })) as any).error, undefined, "a minute later it asks again");
});

test("quote_swap: refusals", async () => {
  const v = file.vectors[0] as Vector;
  const calls: string[] = [];
  const q = new SwapQuoter({ rest: ["http://osmosis.test"], fetch: osmosis(v, calls) });
  for (const [input, code] of [
    [{ amount: "5 USDC" }, "INVALID_AMOUNT"],
    [{ amount: "1" }, "INVALID_AMOUNT"],
    [{ amount: "0 AETH" }, "INVALID_AMOUNT"],
    [{ amount: "1 AETH", slippagePercent: 51 }, "INVALID_ARGUMENT"],
    [{ amount: "1 AETH", slippagePercent: -1 }, "INVALID_ARGUMENT"],
  ] as const) assert.equal(((await q.quote(input)) as any).error.code, code, JSON.stringify(input));
  assert.equal(calls.length, 0, "bad input never reaches Osmosis");

  const refusing = new SwapQuoter({ rest: ["http://a.test", "http://b.test"], fetch: osmosis(v, calls, 400) });
  const r = (await refusing.quote({ amount: "1 AETH" })) as any;
  assert.equal(r.error.code, "INVALID_AMOUNT");
  assert.match(r.error.message, /must be positive/);
  assert.equal(calls.length, 1, "a refused amount isn't retried on the next endpoint");

  const kit = new AetherToolkit(new AetherClient({ rpc: "http://127.0.0.1:1", chainId: "aether-devnet" }), undefined, { faucetUrl: null });
  assert.equal(((await kit.quoteSwap({ amount: "1 AETH" })) as any).error.code, "SWAP_NOT_CONFIGURED", "off away from the testnet");
});

test("quote_swap: over aether-mcp and the toolkit", async () => {
  const v = file.vectors[0] as Vector;
  const kit = new AetherToolkit(new AetherClient({ rpc: "http://127.0.0.1:1" }), undefined, { faucetUrl: null, osmosisRest: ["http://osmosis.test"], fetch: osmosis(v) });
  const res = (await handleMcpMessage(mcpTools(kit), { jsonrpc: "2.0", id: 1, method: "tools/call", params: { name: "quote_swap", arguments: { amount: "1 AETH", slippagePercent: 2 } } }, "1"))!.result as any;
  assert.equal(res.isError, false);
  assert.equal(res.structuredContent.out.uosmo, "394472");
  assert.equal(res.structuredContent.minOutput.uosmo, "386582"); // floor(394472 * 0.98)
  const viaKit = (await kit.call("aether_quote_swap", { amount: "1 AETH", slippage_percent: 0.5 })) as any;
  assert.equal(viaKit.minOutput.uosmo, "392499");
});
