// The client calls docs/START.md copy-pastes (docs/API-STABILITY.md): a
// rename here breaks every bot that followed the start page.
import { test } from "node:test";
import assert from "node:assert/strict";
import { AetherClient, Key } from "../src/index.js";

test("the start page's calls exist under their documented names", () => {
  const { key, mnemonic } = Key.generate();
  assert.match(key.address, /^aether1/);
  assert.equal(mnemonic.split(" ").length, 24);
  assert.equal(Key.fromMnemonic(mnemonic).address, key.address);

  const client = new AetherClient({ rpc: "http://127.0.0.1:1", chainId: "aether-testnet-1" });
  for (const m of ["balance", "send", "rebroadcast", "waitForTransaction"] as const) {
    assert.equal(typeof client[m], "function", `AetherClient.${m}`);
  }
});
