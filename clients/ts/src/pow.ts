import { scrypt } from "@noble/hashes/scrypt.js";
import { sha256 } from "@noble/hashes/sha2.js";
import { bytesToHex, hexToBytes } from "@noble/hashes/utils.js";
import { Writer } from "./proto.js";
import type { AnyMsg } from "./tx.js";

// Native Aether mining, as the chain checks it (x/pow; package miner in
// Go): the header a miner hashes, its scrypt hash, and the MsgSubmitPoW
// that claims it. Tested against clients/testdata/pow.json.
//
// A header is mined against a recent block (its height, its hash and the
// difficulty recorded there), and the chain takes it while that block is
// at most recencyWindow blocks old, one native submission a block.

export const MSG_SUBMIT_POW_TYPE_URL = "/aether.pow.v1.MsgSubmitPoW";

/** The 32 bytes every Aether miner puts in a header's merkle root (the chain doesn't read it). */
export const MERKLE_ROOT = new TextEncoder().encode("merkleplaceholder000000000000000");

export interface MiningHeader {
  height: bigint;
  timestamp: bigint;
  prevHash: Uint8Array;
  merkleRoot: Uint8Array;
  nonce: bigint;
  difficulty: bigint;
  miner: Uint8Array; // the submitting account's address bytes
}

/** What a header is mined against: an explorer's /api/mine/work. */
export interface Work {
  chainId: string;
  height: number;
  hash: string; // hex
  difficulty: string; // decimal
  recencyWindow: number;
  target: string; // hex
}

const u64le = (out: Uint8Array, at: number, v: bigint): void => {
  let n = BigInt.asUintN(64, v);
  for (let i = 0; i < 8; i++) {
    out[at + i] = Number(n & 0xffn);
    n >>= 8n;
  }
};

/** The header as the chain hashes it: integers 8-byte little-endian, then the miner's address. */
export function headerBytes(h: MiningHeader): Uint8Array {
  const out = new Uint8Array(32 + h.prevHash.length + h.merkleRoot.length + h.miner.length);
  u64le(out, 0, h.height);
  u64le(out, 8, h.timestamp);
  out.set(h.prevHash, 16);
  out.set(h.merkleRoot, 16 + h.prevHash.length);
  const n = 16 + h.prevHash.length + h.merkleRoot.length;
  u64le(out, n, h.nonce);
  u64le(out, n + 8, h.difficulty);
  out.set(h.miner, n + 16);
  return out;
}

/** Where the nonce sits in headerBytes, for searchers that rewrite it in place. */
export const nonceOffset = (h: MiningHeader): number => 16 + h.prevHash.length + h.merkleRoot.length;

/** Writes nonce into header bytes at offset (see nonceOffset). */
export function setNonce(bytes: Uint8Array, offset: number, nonce: bigint): void {
  u64le(bytes, offset, nonce);
}

/** scrypt(header, header, N=1024, r=1, p=1, 32): Litecoin's parameters, as x/pow uses them. */
export function powHash(bytes: Uint8Array): Uint8Array {
  return scrypt(bytes, bytes, { N: 1024, r: 1, p: 1, dkLen: 32 });
}

/** sha256 of the header: what the chain keeps of an accepted one, to refuse it again. */
export const headerId = (bytes: Uint8Array): string => bytesToHex(sha256(bytes));

const MAX_TARGET = (1n << 256n) - 1n;

/** (2^256-1)/difficulty: a hash, read big-endian, must stay below it. */
export function target(difficulty: bigint): bigint {
  return difficulty <= 0n ? 0n : MAX_TARGET / difficulty;
}

export function targetHex(difficulty: bigint): string {
  return target(difficulty).toString(16).padStart(64, "0");
}

/** Whether hash meets difficulty. */
export function meets(hash: Uint8Array, difficulty: bigint): boolean {
  return difficulty > 0n && BigInt("0x" + (bytesToHex(hash) || "0")) < target(difficulty);
}

/** Fast check against a precomputed target (32 bytes, big-endian): no bigint per hash. */
export function belowTarget(hash: Uint8Array, targetBytes: Uint8Array): boolean {
  for (let i = 0; i < 32; i++) {
    if (hash[i] !== targetBytes[i]) return hash[i] < targetBytes[i];
  }
  return false;
}

/** A fresh header on work for miner, nonce 0. */
export function headerFor(work: Work, miner: Uint8Array, timestampSeconds: number): MiningHeader {
  return {
    height: BigInt(work.height),
    timestamp: BigInt(timestampSeconds),
    prevHash: hexToBytes(work.hash),
    merkleRoot: MERKLE_ROOT,
    nonce: 0n,
    difficulty: BigInt(work.difficulty),
    miner,
  };
}

/** The MsgSubmitPoW claiming header, sent by the account whose address it names. */
export function msgSubmitPoW(minerAddress: string, h: MiningHeader): AnyMsg {
  const native = new Writer()
    .uint64(1, h.height)
    .uint64(2, h.timestamp)
    .bytes(3, h.prevHash)
    .bytes(4, h.merkleRoot)
    .uint64(5, h.nonce)
    .uint64(6, h.difficulty)
    .finish();
  return { typeUrl: MSG_SUBMIT_POW_TYPE_URL, value: new Writer().string(1, minerAddress).message(2, native).finish() };
}
