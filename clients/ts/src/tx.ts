import { sha256 } from "@noble/hashes/sha2.js";
import { bytesToHex } from "@noble/hashes/utils.js";
import { Writer, readFields, first, text } from "./proto.js";
import { Key, PUBKEY_TYPE_URL, addressBytes } from "./keys.js";
import { DENOM } from "./amount.js";

// Building and signing a bank send (SIGN_MODE_DIRECT). The bytes match the
// chain's Go wallet exactly (see clients/testdata/vectors.json).

export const MSG_SEND_TYPE_URL = "/cosmos.bank.v1beta1.MsgSend";
export const DEFAULT_GAS_LIMIT = 400_000n; // ML-DSA signatures need more than the SDK's 200k default
const SIGN_MODE_DIRECT = 1n;

const coin = (denom: string, amount: bigint) => new Writer().string(1, denom).string(2, amount.toString()).finish();
const any = (typeUrl: string, value: Uint8Array) => new Writer().string(1, typeUrl).bytes(2, value).finish();

export interface SendParams {
  chainId: string;
  accountNumber: bigint | number;
  sequence: bigint | number;
  to: string;
  amountUaeth: bigint;
  memo?: string;
  gasLimit?: bigint | number;
}

export interface SignedTx {
  bodyBytes: Uint8Array;
  authInfoBytes: Uint8Array;
  signDoc: Uint8Array;
  signature: Uint8Array;
  txBytes: Uint8Array;
  /** Uppercase hex SHA-256 of txBytes: what the chain indexes it by. */
  hash: string;
}

export function buildSend(key: Key, p: SendParams, opts: { deterministic?: boolean } = {}): SignedTx {
  addressBytes(p.to); // validates the recipient
  if (p.amountUaeth <= 0n) throw new Error("amount must be positive");
  const msg = new Writer()
    .string(1, key.address)
    .string(2, p.to)
    .message(3, coin(DENOM, p.amountUaeth))
    .finish();
  const bodyBytes = new Writer()
    .message(1, any(MSG_SEND_TYPE_URL, msg))
    .string(2, p.memo ?? "")
    .finish();
  const pubKey = any(PUBKEY_TYPE_URL, new Writer().bytes(1, key.publicKey).finish());
  const modeInfo = new Writer().message(1, new Writer().uint64(1, SIGN_MODE_DIRECT).finish()).finish();
  const signerInfo = new Writer().message(1, pubKey).message(2, modeInfo).uint64(3, BigInt(p.sequence)).finish();
  const fee = new Writer().uint64(2, BigInt(p.gasLimit ?? DEFAULT_GAS_LIMIT)).finish(); // zero fee: no amount
  const authInfoBytes = new Writer().message(1, signerInfo).message(2, fee).finish();
  const signDoc = new Writer()
    .bytes(1, bodyBytes)
    .bytes(2, authInfoBytes)
    .string(3, p.chainId)
    .uint64(4, BigInt(p.accountNumber))
    .finish();
  const signature = key.sign(signDoc, opts);
  const txBytes = new Writer().bytes(1, bodyBytes).bytes(2, authInfoBytes).bytes(3, signature).finish();
  return { bodyBytes, authInfoBytes, signDoc, signature, txBytes, hash: bytesToHex(sha256(txBytes)).toUpperCase() };
}

/** The memo of an encoded transaction (TxRaw), "" if none. */
export function memoOf(txBytes: Uint8Array): string {
  const body = first(readFields(txBytes), 1)?.bytes;
  return body ? text(first(readFields(body), 2)?.bytes) : "";
}
