// The browser's side of the faucet: the small proof of work it solves
// instead of a captcha (cmd/faucet/pow.go), and Aether address checks.

// --- SHA-256, synchronous: WebCrypto's digest is async per call and far
// too slow for a few hundred thousand tiny hashes. ---

const K = new Uint32Array([
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01,
  0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc,
  0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da, 0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147,
  0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
  0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070, 0x19a4c116, 0x1e376c08,
  0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
  0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
]);
const W = new Uint32Array(64);
const H = new Uint32Array(8);

/** sha256 of ASCII text, as its eight big-endian 32-bit words. */
export function sha256Words(text: string): Uint32Array {
  const n = text.length;
  const blocks = ((n + 8) >> 6) + 1;
  const m = new Uint32Array(blocks * 16);
  for (let i = 0; i < n; i++) m[i >> 2] |= (text.charCodeAt(i) & 0xff) << (24 - (i & 3) * 8);
  m[n >> 2] |= 0x80 << (24 - (n & 3) * 8);
  m[blocks * 16 - 1] = n * 8;
  H.set([0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19]);
  for (let b = 0; b < blocks; b++) {
    for (let t = 0; t < 16; t++) W[t] = m[b * 16 + t];
    for (let t = 16; t < 64; t++) {
      const x = W[t - 15];
      const y = W[t - 2];
      const s0 = ((x >>> 7) | (x << 25)) ^ ((x >>> 18) | (x << 14)) ^ (x >>> 3);
      const s1 = ((y >>> 17) | (y << 15)) ^ ((y >>> 19) | (y << 13)) ^ (y >>> 10);
      W[t] = (W[t - 16] + s0 + W[t - 7] + s1) | 0;
    }
    let a = H[0], bb = H[1], c = H[2], d = H[3], e = H[4], f = H[5], g = H[6], h = H[7];
    for (let t = 0; t < 64; t++) {
      const S1 = ((e >>> 6) | (e << 26)) ^ ((e >>> 11) | (e << 21)) ^ ((e >>> 25) | (e << 7));
      const t1 = (h + S1 + ((e & f) ^ (~e & g)) + K[t] + W[t]) | 0;
      const S0 = ((a >>> 2) | (a << 30)) ^ ((a >>> 13) | (a << 19)) ^ ((a >>> 22) | (a << 10));
      const t2 = (S0 + ((a & bb) ^ (a & c) ^ (bb & c))) | 0;
      h = g; g = f; f = e; e = (d + t1) | 0; d = c; c = bb; bb = a; a = (t1 + t2) | 0;
    }
    H[0] += a; H[1] += bb; H[2] += c; H[3] += d; H[4] += e; H[5] += f; H[6] += g; H[7] += h;
  }
  return H;
}

export function sha256Hex(text: string): string {
  return Array.from(sha256Words(text), (w) => (w >>> 0).toString(16).padStart(8, "0")).join("");
}

function leadingZeroBits(words: Uint32Array): number {
  let n = 0;
  for (const w of words) {
    if (w === 0) {
      n += 32;
      continue;
    }
    return n + Math.clz32(w);
  }
  return n;
}

/**
 * Finds a decimal nonce where sha256(challenge + ":" + nonce) starts with
 * `bits` zero bits, yielding to the page every few thousand hashes so it
 * can show progress. Rejects if `signal` aborts.
 */
export function solveChallenge(
  challenge: string,
  bits: number,
  onProgress: (hashes: number) => void,
  signal?: AbortSignal,
): Promise<string> {
  const prefix = challenge + ":";
  return new Promise((resolve, reject) => {
    let nonce = 0;
    function chunk() {
      if (signal?.aborted) return reject(new Error("cancelled"));
      const end = nonce + 20000;
      for (; nonce < end; nonce++) {
        if (leadingZeroBits(sha256Words(prefix + nonce)) >= bits) {
          onProgress(nonce + 1);
          return resolve(String(nonce));
        }
      }
      onProgress(nonce);
      setTimeout(chunk, 0);
    }
    chunk();
  });
}

// --- bech32 (BIP-173), for "is this an Aether address" and demo addresses ---

const CHARSET = "qpzry9x8gf2tvdw0s3jn54khce6mua7l";
const GEN = [0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3];

function polymod(values: number[]): number {
  let chk = 1;
  for (const v of values) {
    const top = chk >>> 25;
    chk = ((chk & 0x1ffffff) << 5) ^ v;
    for (let i = 0; i < 5; i++) if ((top >>> i) & 1) chk ^= GEN[i];
  }
  return chk;
}

function hrpExpand(hrp: string): number[] {
  const out: number[] = [];
  for (let i = 0; i < hrp.length; i++) out.push(hrp.charCodeAt(i) >> 5);
  out.push(0);
  for (let i = 0; i < hrp.length; i++) out.push(hrp.charCodeAt(i) & 31);
  return out;
}

/** A well-formed aether1... address with a valid checksum (20 or 32 bytes). */
export function isAetherAddress(addr: string): boolean {
  if (addr !== addr.toLowerCase() || !addr.startsWith("aether1")) return false;
  const data: number[] = [];
  for (const ch of addr.slice(7)) {
    const v = CHARSET.indexOf(ch);
    if (v < 0) return false;
    data.push(v);
  }
  if (polymod([...hrpExpand("aether"), ...data]) !== 1) return false;
  const bytes = Math.floor(((data.length - 6) * 5) / 8);
  return bytes === 20 || bytes === 32;
}

/** A random 32-byte aether1... address nobody holds the key to: the demo address. */
export function randomAetherAddress(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(32));
  const words: number[] = [];
  let acc = 0;
  let nbits = 0;
  for (const b of bytes) {
    acc = (acc << 8) | b;
    nbits += 8;
    while (nbits >= 5) {
      nbits -= 5;
      words.push((acc >> nbits) & 31);
    }
  }
  if (nbits > 0) words.push((acc << (5 - nbits)) & 31);
  const mod = polymod([...hrpExpand("aether"), ...words, 0, 0, 0, 0, 0, 0]) ^ 1;
  for (let i = 0; i < 6; i++) words.push((mod >> (5 * (5 - i))) & 31);
  return "aether1" + words.map((w) => CHARSET[w]).join("");
}
