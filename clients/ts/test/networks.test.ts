import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { AetherClient, NETWORKS, networkByName, networkForChain, usdcWith } from "../src/index.js";

// clients/testdata/networks.json: the public networks every client names
// (the Go wallet's wallet/network.go, the Python client's networks.py).
const file = JSON.parse(readFileSync(new URL("../../../testdata/networks.json", import.meta.url), "utf8"));

const setting = (u: { channel?: string; path?: string; baseDenom?: string; issuer?: string }) => {
  const out: Record<string, string> = {};
  if (u.channel) out.usdcChannel = u.channel;
  if (u.path) out.usdcPath = u.path;
  if (u.baseDenom) out.usdcBaseDenom = u.baseDenom;
  if (u.issuer) out.usdcIssuer = u.issuer;
  return out;
};

test("networks match the shared vectors", () => {
  assert.equal(NETWORKS.length, file.networks.length);
  file.networks.forEach((v: any, i: number) => {
    const n = NETWORKS[i];
    assert.deepEqual({ ...n }, v);
    assert.equal(networkByName(v.name), n);
    assert.equal(networkByName(v.chainId), n);
    assert.equal(networkForChain(v.chainId), n);
  });
  assert.throws(() => networkByName("devnet"), /unknown network/);
  assert.equal(networkForChain("aether-rc-1"), undefined);
});

test("usdcWith matches the shared vectors", () => {
  for (const c of file.usdcWith) {
    const got = usdcWith(networkByName(c.network), setting(c.given));
    assert.deepEqual(got, setting(c.usdc), c.name);
  }
});

test("a client on a network takes its chain ID, RPC and USDC", () => {
  const t = new AetherClient({ network: "testnet" });
  assert.equal(t.chainId, "aether-testnet-1");
  assert.ok(t.assets.bySymbol("USDC"), "the testnet's USDC");

  assert.throws(() => new AetherClient({ network: "mainnet" }), /no public RPC for mainnet yet/);
  const m = new AetherClient({ network: "mainnet", rpc: "http://localhost:26657", usdcPath: "transfer/channel-7" });
  assert.equal(m.chainId, "aether-1");
  const usdc = m.assets.bySymbol("USDC");
  assert.ok(usdc);
  assert.match(usdc!.origin, /^Injective over transfer\/channel-7$/);
  assert.equal(new AetherClient({ network: "mainnet", rpc: "http://localhost:26657" }).assets.bySymbol("USDC"), undefined, "no USDC until its channel is given");

  assert.throws(() => new AetherClient({ network: "mainnet", rpc: "http://x", chainId: "aether-testnet-1" }), /is chain aether-1/);
  assert.equal(new AetherClient({ rpc: "http://x" }).chainId, "aether-testnet-1", "no network: as before");
});
