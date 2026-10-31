import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { bytesToHex, hexToBytes } from "@noble/hashes/utils.js";
import {
  Key, addressBytes, belowTarget, buildTx, headerBytes, headerFor, headerId, meets, msgSubmitPoW, nonceOffset, powHash,
  setNonce, targetHex, type MiningHeader, type Work,
} from "../src/index.js";

// Every case in clients/testdata/pow.json, as the Go implementation produces it.
const file = JSON.parse(readFileSync(new URL("../../../testdata/pow.json", import.meta.url), "utf8"));

const headerOf = (v: Record<string, string | number>): MiningHeader => ({
  height: BigInt(v.height), timestamp: BigInt(v.timestamp), prevHash: hexToBytes(v.prevHash as string),
  merkleRoot: hexToBytes(v.merkleRoot as string), nonce: BigInt(v.nonce), difficulty: BigInt(v.difficulty), miner: hexToBytes(v.miner as string),
});

test("the miner key and address match Go", () => {
  const key = Key.fromMnemonic(file.mnemonic);
  assert.equal(key.address, file.miner);
  assert.equal(bytesToHex(addressBytes(key.address)), file.minerBytes);
});

test("header bytes, scrypt, id and difficulty checks match Go", () => {
  for (const v of [...file.headers, file.found]) {
    const h = headerOf(v);
    const b = headerBytes(h);
    assert.equal(bytesToHex(b), v.bytes, v.name);
    const hash = powHash(b);
    assert.equal(bytesToHex(hash), v.scrypt, v.name);
    assert.equal(headerId(b), v.id, v.name);
    for (const [d, want] of Object.entries(v.meets)) {
      assert.equal(meets(hash, BigInt(d)), want, `${v.name} at ${d}`);
      assert.equal(belowTarget(hash, hexToBytes(file.targets[d])), want, `${v.name} at ${d}, fast`);
    }
  }
  for (const [d, t] of Object.entries(file.targets)) assert.equal(targetHex(BigInt(d)), t, d);
});

test("rewriting the nonce in place gives the same bytes", () => {
  const v = file.headers[5];
  const h = headerOf(v);
  const b = headerBytes({ ...h, nonce: 0n });
  setNonce(b, nonceOffset(h), h.nonce);
  assert.equal(bytesToHex(b), v.bytes);
});

test("a header from /api/mine/work, searched from 0, finds Go's nonce", () => {
  const work = file.work as Work;
  const h = headerFor(work, hexToBytes(file.minerBytes), Number(file.found.timestamp));
  const b = headerBytes(h);
  const off = nonceOffset(h);
  const t = hexToBytes(work.target);
  let n = 0n;
  for (; n < 100_000n; n++) {
    setNonce(b, off, n);
    if (belowTarget(powHash(b), t)) break;
  }
  assert.equal(n.toString(), file.found.nonce);
});

test("MsgSubmitPoW and its signed transaction match Go", () => {
  const h = headerOf(file.found);
  const msg = msgSubmitPoW(file.miner, h);
  assert.equal(msg.typeUrl, file.msg.typeUrl);
  assert.equal(bytesToHex(msg.value), file.msg.bytes);
  const key = Key.fromMnemonic(file.mnemonic);
  const signed = buildTx(key, [msg], { chainId: file.tx.chainId, accountNumber: file.tx.accountNumber, sequence: file.tx.sequence, gasLimit: file.tx.gasLimit }, { deterministic: true });
  assert.equal(bytesToHex(signed.bodyBytes), file.tx.bodyBytes);
  assert.equal(bytesToHex(signed.authInfoBytes), file.tx.authInfoBytes);
  assert.equal(bytesToHex(signed.signDoc), file.tx.signDoc);
  assert.equal(bytesToHex(signed.signature), file.tx.signature);
  assert.equal(bytesToHex(signed.txBytes), file.tx.txBytes);
  assert.equal(signed.hash, file.tx.txHash);
});
