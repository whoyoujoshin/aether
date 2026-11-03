import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { feeFor, parseGasPrices } from "../src/index.js";

// clients/testdata/fees.json: the fee rule every client applies (the Go
// wallet's TestFeeFor_SharedVectors checks the same file).
const file = JSON.parse(readFileSync(new URL("../../../testdata/fees.json", import.meta.url), "utf8"));

test("fees: the shared vectors", () => {
  for (const v of file.vectors as { name: string; prices: string; denom: string; gas: number; tip: number; fee: string }[]) {
    const f = feeFor(parseGasPrices(v.prices), v.denom, v.gas, v.tip);
    assert.equal(f ? `${f.amount}${f.denom}` : "", v.fee, v.name);
  }
});

test("fees: a tip under 1 is refused", () => {
  assert.throws(() => feeFor(parseGasPrices("0.0001uaeth"), "uaeth", 1, 0.5), /at least 1/);
});
