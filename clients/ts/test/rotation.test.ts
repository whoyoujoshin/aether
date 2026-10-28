import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { sha256 } from "@noble/hashes/sha2.js";
import { bytesToHex, hexToBytes } from "@noble/hashes/utils.js";
import { base64 } from "@scure/base";
import { Writer } from "../src/proto.js";
import {
  AetherClient, Key, PUBKEY_TYPE_URL, buildTx, createReceiptDelegation, decodeAccountPubKey, decodeRotation,
  delegationSigningMessage, guardianCancelSigningBytes, guardianSetRecoverySigningBytes, msgCancelPendingRotation,
  msgGuardianCancel, msgGuardianSetRecovery, msgRecoverKey, msgRotateKey, msgSetRecovery, msgWatchtowerCancel,
  recoverKeySigningBytes, rotateKeySigningBytes, rotationRequest, signReceipt, signsFor, verifyReceipt,
  watchtowerCancelSigningBytes, type Receipt,
} from "../src/index.js";

// Every case in clients/testdata/rotation.json, as the Go implementation produces it.
const file = JSON.parse(readFileSync(new URL("../../../testdata/rotation.json", import.meta.url), "utf8"));
const text = new TextEncoder();

const keys = Object.fromEntries(file.keys.map((k: { name: string; mnemonic: string }) => [k.name, Key.fromMnemonic(k.mnemonic)])) as Record<string, Key>;
const owner = keys.owner, next = keys.next, g1 = keys.guardian1, g2 = keys.guardian2, wt = keys.watchtower;
const chain = file.chainId as string;
const account = owner.address;
const det = { deterministic: true };

test("rotation mnemonics derive the Go wallet's keys", () => {
  for (const k of file.keys) {
    const key = keys[k.name];
    assert.equal(bytesToHex(key.seed), k.seed, k.name);
    assert.equal(bytesToHex(key.publicKey), k.pubKey, k.name);
    assert.equal(key.address, k.address, k.name);
  }
});

test("signing bytes and deterministic signatures match Go", () => {
  const produced: Record<string, Uint8Array> = {
    "rotate (new key's proof, nonce 3)": rotateKeySigningBytes(chain, account, 3, next.publicKey),
    "recover (new key's proof, recovery nonce 1)": rotateKeySigningBytes(chain, account, 1, next.publicKey),
    "recover (guardian 1, recovery nonce 1)": recoverKeySigningBytes(chain, account, 1, next.publicKey),
    "recover (guardian 2, recovery nonce 1)": recoverKeySigningBytes(chain, account, 1, next.publicKey),
    "guardian cancel (guardian 1, recovery nonce 1)": guardianCancelSigningBytes(chain, account, 1),
    "watchtower cancel (nonce 3)": watchtowerCancelSigningBytes(chain, account, 3),
    "guardian set recovery (guardian 1, recovery nonce 1)": guardianSetRecoverySigningBytes(chain, account, 1, 1, [g1.publicKey, g2.publicKey], [wt.publicKey]),
    "guardian set recovery, removing the set (guardian 1, recovery nonce 1)": guardianSetRecoverySigningBytes(chain, account, 1, 0, [], []),
  };
  assert.equal(file.signingBytes.length, Object.keys(produced).length, "every signing-bytes case is covered");
  for (const s of file.signingBytes) {
    const bz = produced[s.name];
    assert.ok(bz, s.name);
    assert.equal(bytesToHex(sha256(text.encode(s.text))), s.sha256, s.name + " text");
    assert.equal(bytesToHex(bz), s.sha256, s.name);
    const key = keys[s.signedBy];
    assert.equal(bytesToHex(key.sign(bz, det)), s.signature, s.name + " signature");
    assert.ok(Key.verify(key.publicKey, bz, hexToBytes(s.signature)));
  }
});

test("every rotation message encodes like Go", () => {
  const rotateSig = next.sign(rotateKeySigningBytes(chain, account, 3, next.publicKey), det);
  const recoverPop = next.sign(rotateKeySigningBytes(chain, account, 1, next.publicKey), det);
  const recoverBz = recoverKeySigningBytes(chain, account, 1, next.publicKey);
  const g1Recover = g1.sign(recoverBz, det);
  const g2Recover = g2.sign(recoverBz, det);
  const g1Cancel = g1.sign(guardianCancelSigningBytes(chain, account, 1), det);
  const setBz = guardianSetRecoverySigningBytes(chain, account, 1, 1, [g1.publicKey, g2.publicKey], [wt.publicKey]);
  const g1Set = g1.sign(setBz, det);
  const watchSig = wt.sign(watchtowerCancelSigningBytes(chain, account, 3), det);
  const sig = (key: Key, signature: Uint8Array) => ({ pubkey: key.publicKey, signature });
  const built: Record<string, { typeUrl: string; value: Uint8Array }> = {
    "MsgRotateKey": msgRotateKey(account, next.publicKey, rotateSig),
    "MsgCancelPendingRotation": msgCancelPendingRotation(account),
    "MsgSetRecovery": msgSetRecovery(account, [g1.publicKey, g2.publicKey], 2),
    "MsgSetRecovery (with a watchtower)": msgSetRecovery(account, [g1.publicKey, g2.publicKey], 2, [wt.publicKey]),
    "MsgSetRecovery (remove)": msgSetRecovery(account, [], 0),
    "MsgRecoverKey": msgRecoverKey(g1.address, account, next.publicKey, recoverPop, [sig(g1, g1Recover), sig(g2, g2Recover)]),
    "MsgGuardianCancel": msgGuardianCancel(g1.address, account, [sig(g1, g1Cancel)]),
    "MsgGuardianSetRecovery": msgGuardianSetRecovery(g1.address, account, [g1.publicKey, g2.publicKey], 1, [wt.publicKey], [sig(g1, g1Set)]),
    "MsgWatchtowerCancel": msgWatchtowerCancel(g2.address, account, wt.publicKey, watchSig),
  };
  assert.equal(file.messages.length, Object.keys(built).length, "every message is covered");
  for (const m of file.messages) {
    const got = built[m.name];
    assert.ok(got, m.name);
    assert.equal(got.typeUrl, m.typeUrl, m.name);
    assert.equal(bytesToHex(got.value), m.bytes, m.name);
  }

  const tx = file.rotateKeyTx;
  const signed = buildTx(owner, [built["MsgRotateKey"]], {
    chainId: tx.chainId, accountNumber: BigInt(tx.accountNumber), sequence: BigInt(tx.sequence), gasLimit: BigInt(tx.gasLimit),
  }, det);
  assert.equal(bytesToHex(signed.bodyBytes), tx.bodyBytes, "body");
  assert.equal(bytesToHex(signed.authInfoBytes), tx.authInfoBytes, "auth info");
  assert.equal(bytesToHex(signed.signDoc), tx.signDoc, "sign doc");
  assert.equal(bytesToHex(signed.signature), tx.signature, "signature");
  assert.equal(bytesToHex(signed.txBytes), tx.txBytes, "tx bytes");
  assert.equal(signed.hash, tx.txHash, "tx hash");
});

test("a Rotation query answer decodes to what the chain returns", () => {
  const q = file.rotationQuery;
  assert.equal(base64.encode(rotationRequest(account)), q.request);
  const got = decodeRotation(base64.decode(q.response));
  const hexes = (xs: Uint8Array[]) => xs.map(bytesToHex);
  assert.deepEqual({
    nonce: Number(got.nonce),
    recoveryNonce: Number(got.recoveryNonce),
    recoveryThreshold: got.threshold,
    recoveryGuardians: hexes(got.guardians),
    recoveryWatchtowers: hexes(got.watchtowers),
    pendingRotation: got.pendingRotation ? { newPubkey: bytesToHex(got.pendingRotation.newPubkey), applyHeight: Number(got.pendingRotation.applyHeight) } : null,
    pendingRecovery: got.pendingRecovery ? {
      guardians: hexes(got.pendingRecovery.guardians), threshold: got.pendingRecovery.threshold,
      watchtowers: hexes(got.pendingRecovery.watchtowers), applyHeight: Number(got.pendingRecovery.applyHeight),
    } : null,
    currentPubkey: got.currentPubkey ? bytesToHex(got.currentPubkey) : "",
    active: got.active,
    delayBlocks: Number(got.delayBlocks),
  }, q.expected);
});

test("signsFor follows the stored key and fails closed when the lookup fails", async () => {
  for (const c of file.signsFor) {
    const stored = c.stored ? hexToBytes(c.stored) : undefined;
    assert.equal(signsFor(c.account, hexToBytes(c.pubKey), stored), c.signsFor, c.name);
  }
  const retired = file.signsFor.find((c: { name: string }) => c.name.startsWith("rotated: the retired"));
  const accepted = file.signsFor.find((c: { name: string }) => c.name === "rotated: the new key");
  assert.equal(retired.signsFor, false);
  assert.equal(accepted.signsFor, true);

  const info = (pub?: Uint8Array) => {
    const body = new Writer();
    if (pub) body.message(2, new Writer().string(1, PUBKEY_TYPE_URL).bytes(2, new Writer().bytes(1, pub).finish()).finish());
    return new Writer().message(1, body.uint64(3, 7n).uint64(4, 1n).finish()).finish();
  };
  const node = (value?: Uint8Array, fail = false) => new AetherClient({
    rpc: "http://node", chainId: chain,
    fetch: (async () => new Response(JSON.stringify(fail
      ? { jsonrpc: "2.0", id: 1, error: { code: -32603, message: "Internal error", data: "node down" } }
      : { jsonrpc: "2.0", id: 1, result: { response: { code: 0, log: "", value: value ? base64.encode(value) : null } } }))) as typeof fetch,
  });

  const rotated = node(info(next.publicKey));
  assert.equal(bytesToHex(await rotated.accountPubKey(account) ?? new Uint8Array()), bytesToHex(next.publicKey));
  assert.equal(await rotated.signsFor(account, next.publicKey), true, "the new key");
  assert.equal(await rotated.signsFor(account, owner.publicKey), false, "the retired key");
  assert.equal(decodeAccountPubKey(info()), undefined, "no key stored");
  assert.equal(await node(info()).signsFor(account, owner.publicKey), true, "nothing stored: its own key");
  await assert.rejects(node(undefined, true).signsFor(account, owner.publicKey), /node down/);
  assert.equal(await node(undefined, true).signsFor(account, owner.publicKey.subarray(0, 32)), false, "not a key, so it isn't looked up");

  const answered = node(base64.decode(file.rotationQuery.response));
  const view = await answered.rotation(account);
  assert.equal(view.active, true);
  assert.equal(view.delayBlocks, 15300n);
  assert.equal(bytesToHex(view.currentPubkey!), file.rotationQuery.expected.currentPubkey);
});

test("receipts accept the payee's new key and refuse the retired one", () => {
  const hash = bytesToHex(sha256(new Uint8Array()));
  const fields = {
    network: chain, payTo: account, payer: g1.address, scheme: "aether-memo", payment: "AB", amount: "1",
    method: "GET", host: "h", path: "/p", requestHash: hash, status: 200, at: 10,
  };
  const byOld = signReceipt(owner, fields);
  const byNew = signReceipt(next, fields);
  assert.equal(verifyReceipt(byOld), undefined, "hash-only still accepts the address's key");
  assert.ok(verifyReceipt(byNew), "hash-only refuses the new key");

  const rotated = { accountPubKey: (a: string) => (a === account ? next.publicKey : undefined) };
  assert.ok(verifyReceipt(byOld, rotated), "the retired key");
  assert.equal(verifyReceipt(byNew, rotated), undefined, "the new key");
  assert.match(verifyReceipt(byNew, { accountPubKey: () => { throw new Error("node down"); } }) ?? "", /reading the payee's key/);

  const receiptKey = g2;
  const expires = 100;
  const delegation = (signer: Key): NonNullable<Receipt["delegation"]> => ({
    payTo: account, signer: receiptKey.address, expires, payToPubKey: base64.encode(signer.publicKey),
    signature: base64.encode(signer.sign(delegationSigningMessage(account, receiptKey.address, expires))),
  });
  const delegated = (d: NonNullable<Receipt["delegation"]>) => signReceipt(receiptKey, fields, d);
  assert.equal(verifyReceipt(delegated(delegation(next)), rotated), undefined, "a delegation the new key signed");
  assert.match(verifyReceipt(delegated(delegation(owner)), rotated) ?? "", /payee's key/, "a delegation the retired key signed");
  assert.equal(verifyReceipt(delegated(createReceiptDelegation(owner, receiptKey.address, expires))), undefined, "hash-only, the address's own key");
});
