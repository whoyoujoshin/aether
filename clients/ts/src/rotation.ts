import { sha256 } from "@noble/hashes/sha2.js";
import { bytesToHex } from "@noble/hashes/utils.js";
import { Writer, readFields, first, text, type Field } from "./proto.js";
import { PUBKEY_BYTES, PUBKEY_TYPE_URL, addressOf } from "./keys.js";
import { type AnyMsg } from "./tx.js";

// Key rotation (docs/PQ-KEY-ROTATION.md). An account keeps its address
// while the ML-DSA-44 key that signs for it changes. These builders match
// the chain's protobuf and the sha256 signing bytes in that doc.

const PKG = "/aether.accountauth.v1.";
export const MSG_ROTATE_KEY_TYPE_URL = PKG + "MsgRotateKey";
export const MSG_CANCEL_PENDING_ROTATION_TYPE_URL = PKG + "MsgCancelPendingRotation";
export const MSG_SET_RECOVERY_TYPE_URL = PKG + "MsgSetRecovery";
export const MSG_RECOVER_KEY_TYPE_URL = PKG + "MsgRecoverKey";
export const MSG_GUARDIAN_CANCEL_TYPE_URL = PKG + "MsgGuardianCancel";
export const MSG_GUARDIAN_SET_RECOVERY_TYPE_URL = PKG + "MsgGuardianSetRecovery";
export const MSG_WATCHTOWER_CANCEL_TYPE_URL = PKG + "MsgWatchtowerCancel";
export const ROTATION_QUERY_PATH = "/aether.accountauth.v1.Query/Rotation";

export interface GuardianSig {
  pubkey: Uint8Array;
  signature: Uint8Array;
}

const enc = new TextEncoder();
const digest = (s: string) => sha256(enc.encode(s));
const hexList = (keys: Uint8Array[]) => keys.map((k) => bytesToHex(k)).join(",");

function sameBytes(a: Uint8Array, b: Uint8Array): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false;
  return true;
}

/** What the new key signs to prove it is held. MsgRotateKey uses the account's nonce; MsgRecoverKey uses its recovery nonce. */
export function rotateKeySigningBytes(chainId: string, account: string, nonce: number | bigint, newPubkey: Uint8Array): Uint8Array {
  return digest(`aether-accountauth-rotate/v1\n${chainId}\n${account}\n${nonce}\n${bytesToHex(newPubkey)}\n`);
}

/** What each recovery guardian signs, at recovery_nonce, to rotate the account at once. */
export function recoverKeySigningBytes(chainId: string, account: string, nonce: number | bigint, newPubkey: Uint8Array): Uint8Array {
  return digest(`aether-accountauth-recover/v1\n${chainId}\n${account}\n${nonce}\n${bytesToHex(newPubkey)}\n`);
}

/** What each recovery guardian signs, at recovery_nonce, to cancel pending changes. */
export function guardianCancelSigningBytes(chainId: string, account: string, nonce: number | bigint): Uint8Array {
  return digest(`aether-accountauth-guardian-cancel/v1\n${chainId}\n${account}\n${nonce}\n`);
}

/**
 * What each recovery guardian signs, at recovery_nonce, to replace the
 * recovery set at once. Keys are hex in the message's order; an empty
 * list (threshold 0, no keys) removes the set.
 */
export function guardianSetRecoverySigningBytes(
  chainId: string, account: string, nonce: number | bigint, threshold: number,
  guardianPubkeys: Uint8Array[], watchtowerPubkeys: Uint8Array[] = [],
): Uint8Array {
  return digest(`aether-accountauth-guardian-set-recovery/v1\n${chainId}\n${account}\n${nonce}\n${threshold}\nguardians:${hexList(guardianPubkeys)}\nwatchtowers:${hexList(watchtowerPubkeys)}\n`);
}

/** What a watchtower signs, at the account's nonce, to cancel a change the current key scheduled. */
export function watchtowerCancelSigningBytes(chainId: string, account: string, nonce: number | bigint): Uint8Array {
  return digest(`aether-accountauth-watchtower-cancel/v1\n${chainId}\n${account}\n${nonce}\n`);
}

const msg = (typeUrl: string, value: Uint8Array): AnyMsg => ({ typeUrl, value });
const gsig = (g: GuardianSig) => new Writer().bytes(1, g.pubkey).bytes(2, g.signature).finish();

export function msgRotateKey(account: string, newPubkey: Uint8Array, newKeySignature: Uint8Array): AnyMsg {
  return msg(MSG_ROTATE_KEY_TYPE_URL, new Writer().string(1, account).bytes(2, newPubkey).bytes(3, newKeySignature).finish());
}

export function msgCancelPendingRotation(account: string): AnyMsg {
  return msg(MSG_CANCEL_PENDING_ROTATION_TYPE_URL, new Writer().string(1, account).finish());
}

export function msgSetRecovery(account: string, guardianPubkeys: Uint8Array[], threshold: number, watchtowerPubkeys: Uint8Array[] = []): AnyMsg {
  const w = new Writer().string(1, account);
  for (const g of guardianPubkeys) w.bytes(2, g);
  w.uint64(3, threshold);
  for (const t of watchtowerPubkeys) w.bytes(4, t);
  return msg(MSG_SET_RECOVERY_TYPE_URL, w.finish());
}

export function msgRecoverKey(submitter: string, account: string, newPubkey: Uint8Array, newKeySignature: Uint8Array, guardianSignatures: GuardianSig[]): AnyMsg {
  const w = new Writer().string(1, submitter).string(2, account).bytes(3, newPubkey).bytes(4, newKeySignature);
  for (const g of guardianSignatures) w.message(5, gsig(g));
  return msg(MSG_RECOVER_KEY_TYPE_URL, w.finish());
}

export function msgGuardianCancel(submitter: string, account: string, guardianSignatures: GuardianSig[]): AnyMsg {
  const w = new Writer().string(1, submitter).string(2, account);
  for (const g of guardianSignatures) w.message(3, gsig(g));
  return msg(MSG_GUARDIAN_CANCEL_TYPE_URL, w.finish());
}

export function msgGuardianSetRecovery(
  submitter: string, account: string, guardianPubkeys: Uint8Array[], threshold: number,
  watchtowerPubkeys: Uint8Array[], guardianSignatures: GuardianSig[],
): AnyMsg {
  const w = new Writer().string(1, submitter).string(2, account);
  for (const g of guardianPubkeys) w.bytes(3, g);
  w.uint64(4, threshold);
  for (const t of watchtowerPubkeys) w.bytes(5, t);
  for (const g of guardianSignatures) w.message(6, gsig(g));
  return msg(MSG_GUARDIAN_SET_RECOVERY_TYPE_URL, w.finish());
}

export function msgWatchtowerCancel(submitter: string, account: string, watchtowerPubkey: Uint8Array, signature: Uint8Array): AnyMsg {
  return msg(MSG_WATCHTOWER_CANCEL_TYPE_URL, new Writer().string(1, submitter).string(2, account).bytes(3, watchtowerPubkey).bytes(4, signature).finish());
}

/**
 * The chain's rule (wallet.SignsFor): the key stored on the account signs
 * for it; while none is stored, the key its address hashes from does.
 * After a rotation that is the new key, not the retired one. `stored`
 * empty or omitted means none is stored. A key that isn't ML-DSA-44 is
 * never a signer. This does not look the key up; AetherClient.signsFor does,
 * and refuses when the lookup fails.
 */
export function signsFor(account: string, pubKey: Uint8Array, stored?: Uint8Array): boolean {
  if (pubKey.length !== PUBKEY_BYTES) return false;
  if (stored && stored.length > 0) return sameBytes(stored, pubKey);
  return addressOf(pubKey) === account;
}

/** Reads the ML-DSA-44 key stored on an account. undefined: none stored. Throw if the lookup fails. */
export interface AccountKeys {
  accountPubKey(account: string): Uint8Array | undefined;
}

export interface Rotation {
  nonce: bigint;
  recoveryNonce: bigint;
  guardians: Uint8Array[];
  threshold: number;
  watchtowers: Uint8Array[];
  pendingRotation?: { newPubkey: Uint8Array; applyHeight: bigint };
  pendingRecovery?: { guardians: Uint8Array[]; threshold: number; watchtowers: Uint8Array[]; applyHeight: bigint };
  currentPubkey?: Uint8Array;
  active: boolean;
  delayBlocks: bigint;
}

export function rotationRequest(account: string): Uint8Array {
  return new Writer().string(1, account).finish();
}

const bytesOf = (fields: Field[], n: number): Uint8Array[] => fields.filter((f) => f.field === n && f.bytes).map((f) => f.bytes!);

function recoveryOf(buf: Uint8Array | undefined): { guardians: Uint8Array[]; threshold: number; watchtowers: Uint8Array[] } {
  if (!buf) return { guardians: [], threshold: 0, watchtowers: [] };
  const f = readFields(buf);
  return { guardians: bytesOf(f, 1), threshold: Number(first(f, 2)?.varint ?? 0n), watchtowers: bytesOf(f, 3) };
}

/** Decodes a Query/Rotation response. */
export function decodeRotation(resp: Uint8Array): Rotation {
  const top = readFields(resp);
  const state = readFields(first(top, 1)?.bytes ?? new Uint8Array());
  const recovery = recoveryOf(first(state, 2)?.bytes);
  const pendKey = first(state, 3)?.bytes;
  const pendSet = first(state, 4)?.bytes;
  let pendingRotation: Rotation["pendingRotation"];
  if (pendKey) {
    const f = readFields(pendKey);
    const pk = first(f, 1)?.bytes;
    if (pk) pendingRotation = { newPubkey: pk, applyHeight: first(f, 2)?.varint ?? 0n };
  }
  let pendingRecovery: Rotation["pendingRecovery"];
  if (pendSet) {
    const f = readFields(pendSet);
    const set = recoveryOf(first(f, 1)?.bytes);
    pendingRecovery = { ...set, applyHeight: first(f, 2)?.varint ?? 0n };
  }
  const current = first(top, 2)?.bytes;
  return {
    nonce: first(state, 1)?.varint ?? 0n,
    recoveryNonce: first(state, 5)?.varint ?? 0n,
    guardians: recovery.guardians,
    threshold: recovery.threshold,
    watchtowers: recovery.watchtowers,
    pendingRotation,
    pendingRecovery,
    currentPubkey: current && current.length > 0 ? current : undefined,
    active: (first(top, 3)?.varint ?? 0n) !== 0n,
    delayBlocks: first(top, 4)?.varint ?? 0n,
  };
}

/**
 * The ML-DSA-44 key inside a Query/AccountInfo response, or undefined if
 * the account has no key stored. A key that is present but isn't ML-DSA-44
 * is an error: treating it as "none" would accept the retired key.
 */
export function decodeAccountPubKey(resp: Uint8Array): Uint8Array | undefined {
  const info = first(readFields(resp), 1)?.bytes;
  if (!info) return undefined;
  const any = first(readFields(info), 2)?.bytes;
  if (!any) return undefined;
  const af = readFields(any);
  const typeUrl = text(first(af, 1)?.bytes);
  const value = first(af, 2)?.bytes ?? new Uint8Array();
  if (!typeUrl && value.length === 0) return undefined;
  if (typeUrl !== PUBKEY_TYPE_URL) throw new Error(`account key type ${typeUrl || "unknown"} is not an ML-DSA-44 key`);
  const raw = first(readFields(value), 1)?.bytes ?? new Uint8Array();
  if (raw.length === 0) return undefined;
  if (raw.length !== PUBKEY_BYTES) throw new Error(`stored key is ${raw.length} bytes, not an ML-DSA-44 public key`);
  return raw;
}
