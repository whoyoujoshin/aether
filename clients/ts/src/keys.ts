import { ml_dsa44 } from "@noble/post-quantum/ml-dsa.js";
import { sha256 } from "@noble/hashes/sha2.js";
import { randomBytes } from "@noble/hashes/utils.js";
import { generateMnemonic, mnemonicToSeedSync, validateMnemonic } from "@scure/bip39";
import { wordlist } from "@scure/bip39/wordlists/english.js";
import { bech32 } from "@scure/base";

// Aether accounts are ML-DSA-44 (FIPS 204) keys -- post-quantum, and
// mandatory from genesis. One mnemonic is one key (no HD paths), derived
// exactly as the chain's own wallet does:
//   seed = SHA-256(BIP-39 seed(mnemonic, passphrase)), key = ML-DSA-44 KeyGen(seed)
// so a phrase from `aetherd keys add` or `agentmcp init` imports here.

export const PREFIX = "aether";
export const PUBKEY_TYPE_URL = "/aether.crypto.v1.PubKey";
const PUBKEY_PROTO_NAME = "aether.crypto.v1.PubKey";

const concat = (...parts: Uint8Array[]): Uint8Array => {
  const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
  let o = 0;
  for (const p of parts) {
    out.set(p, o);
    o += p.length;
  }
  return out;
};

/** The account address of an ML-DSA-44 public key (ADR-028: 32 bytes). */
export function addressOf(publicKey: Uint8Array): string {
  const typeHash = sha256(new TextEncoder().encode(PUBKEY_PROTO_NAME));
  return bech32.encode(PREFIX, bech32.toWords(sha256(concat(typeHash, publicKey))), 200);
}

/** Decodes an aether1... address to its bytes, checking the checksum and prefix. */
export function addressBytes(address: string): Uint8Array {
  const { prefix, words } = bech32.decode(address as `${string}1${string}`, 200);
  if (prefix !== PREFIX) throw new Error(`address "${address}" is not an ${PREFIX}1... address`);
  return bech32.fromWords(words);
}

export function isAddress(address: string): boolean {
  try {
    addressBytes(address);
    return true;
  } catch {
    return false;
  }
}

export class Key {
  readonly publicKey: Uint8Array;
  readonly address: string;
  private readonly secretKey: Uint8Array;

  private constructor(readonly seed: Uint8Array) {
    if (seed.length !== 32) throw new Error("ML-DSA-44 seed must be 32 bytes");
    const kp = ml_dsa44.keygen(seed);
    this.publicKey = kp.publicKey;
    this.secretKey = kp.secretKey;
    this.address = addressOf(this.publicKey);
  }

  /** A new key with its 24-word recovery phrase. Keep the phrase safe. */
  static generate(): { key: Key; mnemonic: string } {
    const mnemonic = generateMnemonic(wordlist, 256);
    return { key: Key.fromMnemonic(mnemonic), mnemonic };
  }

  static fromMnemonic(mnemonic: string, passphrase = ""): Key {
    const normalized = mnemonic.trim().split(/\s+/).join(" ");
    if (!validateMnemonic(normalized, wordlist)) throw new Error("invalid recovery phrase (checksum or word list)");
    return new Key(sha256(mnemonicToSeedSync(normalized, passphrase)));
  }

  static fromSeed(seed: Uint8Array): Key {
    return new Key(Uint8Array.from(seed));
  }

  static random(): Key {
    return new Key(randomBytes(32));
  }

  /** Signs msg (FIPS 204, empty context). Randomized unless deterministic. */
  sign(msg: Uint8Array, opts: { deterministic?: boolean } = {}): Uint8Array {
    return ml_dsa44.sign(msg, this.secretKey, opts.deterministic ? { extraEntropy: false } : undefined);
  }

  static verify(publicKey: Uint8Array, msg: Uint8Array, signature: Uint8Array): boolean {
    try {
      return ml_dsa44.verify(signature, msg, publicKey);
    } catch {
      return false;
    }
  }
}
