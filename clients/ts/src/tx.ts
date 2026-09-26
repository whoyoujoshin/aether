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
  return buildTx(key, [msgSend(key.address, p.to, p.amountUaeth)], p, opts);
}

export interface TxParams {
  chainId: string;
  accountNumber: bigint | number;
  sequence: bigint | number;
  memo?: string;
  gasLimit?: bigint | number;
}

/** An encoded message and its type URL, as a transaction body carries it (a protobuf Any). */
export interface AnyMsg {
  typeUrl: string;
  value: Uint8Array;
}

function msgSend(from: string, to: string, amountUaeth: bigint): AnyMsg {
  return { typeUrl: MSG_SEND_TYPE_URL, value: new Writer().string(1, from).string(2, to).message(3, coin(DENOM, amountUaeth)).finish() };
}

/** Signs a transaction carrying msgs, which the chain executes all-or-nothing. */
export function buildTx(key: Key, msgs: AnyMsg[], p: TxParams, opts: { deterministic?: boolean } = {}): SignedTx {
  const body = new Writer();
  for (const m of msgs) body.message(1, any(m.typeUrl, m.value));
  const bodyBytes = body.string(2, p.memo ?? "").finish();
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

export const MSG_GRANT_TYPE_URL = "/cosmos.authz.v1beta1.MsgGrant";
export const MSG_EXEC_TYPE_URL = "/cosmos.authz.v1beta1.MsgExec";
export const SEND_AUTHORIZATION_TYPE_URL = "/cosmos.bank.v1beta1.SendAuthorization";

/**
 * An x/authz grant letting grantee send up to limitUaeth from granter,
 * only to allowList if it's non-empty, until expiration (Unix seconds).
 * Granting again to the same grantee replaces the grant and its limit.
 */
export function grantSendMsg(granter: string, grantee: string, limitUaeth: bigint, allowList: string[], expiration: number): AnyMsg {
  addressBytes(granter);
  addressBytes(grantee);
  if (granter === grantee) throw new Error("granter and grantee must be different accounts");
  if (limitUaeth <= 0n) throw new Error("a send grant needs a positive spend limit");
  const auth = new Writer().message(1, coin(DENOM, limitUaeth));
  for (const a of allowList) {
    addressBytes(a);
    auth.string(2, a);
  }
  const timestamp = new Writer().uint64(1, BigInt(Math.floor(expiration))).finish();
  const grant = new Writer().message(1, any(SEND_AUTHORIZATION_TYPE_URL, auth.finish())).message(2, timestamp).finish();
  return { typeUrl: MSG_GRANT_TYPE_URL, value: new Writer().string(1, granter).string(2, grantee).message(3, grant).finish() };
}

/** grantee sends amountUaeth from granter to `to`, under a send grant granter gave it. */
export function execSendMsg(grantee: string, granter: string, to: string, amountUaeth: bigint): AnyMsg {
  addressBytes(to);
  if (grantee === granter) throw new Error("granter and grantee must be different accounts");
  const send = msgSend(granter, to, amountUaeth);
  return { typeUrl: MSG_EXEC_TYPE_URL, value: new Writer().string(1, grantee).message(2, any(send.typeUrl, send.value)).finish() };
}

/** The memo of an encoded transaction (TxRaw), "" if none. */
export function memoOf(txBytes: Uint8Array): string {
  const body = first(readFields(txBytes), 1)?.bytes;
  return body ? text(first(readFields(body), 2)?.bytes) : "";
}
