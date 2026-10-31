/* eslint-disable @typescript-eslint/ban-ts-comment -- typed for callers by scrypt.d.ts */
// @ts-nocheck
// Litecoin / Aether Scrypt: scrypt(header, header, N=1024, r=1, p=1, 32).
// Pure JS so a browser worker can hash without native addons.

const K = new Uint32Array([
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
  0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
  0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
  0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
  0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
  0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
]);

function rotr(x, n) {
  return (x >>> n) | (x << (32 - n));
}

function sha256(bytes) {
  const bitLen = bytes.length * 8;
  const padLen = ((bytes.length + 9 + 63) & ~63) >>> 0;
  const buf = new Uint8Array(padLen);
  buf.set(bytes);
  buf[bytes.length] = 0x80;
  const view = new DataView(buf.buffer);
  view.setUint32(padLen - 4, bitLen >>> 0, false);
  view.setUint32(padLen - 8, Math.floor(bitLen / 0x100000000), false);

  let h0 = 0x6a09e667, h1 = 0xbb67ae85, h2 = 0x3c6ef372, h3 = 0xa54ff53a;
  let h4 = 0x510e527f, h5 = 0x9b05688c, h6 = 0x1f83d9ab, h7 = 0x5be0cd19;
  const w = new Uint32Array(64);

  for (let off = 0; off < padLen; off += 64) {
    for (let i = 0; i < 16; i++) w[i] = view.getUint32(off + i * 4, false);
    for (let i = 16; i < 64; i++) {
      const s0 = rotr(w[i - 15], 7) ^ rotr(w[i - 15], 18) ^ (w[i - 15] >>> 3);
      const s1 = rotr(w[i - 2], 17) ^ rotr(w[i - 2], 19) ^ (w[i - 2] >>> 10);
      w[i] = (w[i - 16] + s0 + w[i - 7] + s1) >>> 0;
    }
    let a = h0, b = h1, c = h2, d = h3, e = h4, f = h5, g = h6, h = h7;
    for (let i = 0; i < 64; i++) {
      const S1 = rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25);
      const ch = (e & f) ^ (~e & g);
      const t1 = (h + S1 + ch + K[i] + w[i]) >>> 0;
      const S0 = rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22);
      const maj = (a & b) ^ (a & c) ^ (b & c);
      const t2 = (S0 + maj) >>> 0;
      h = g; g = f; f = e; e = (d + t1) >>> 0; d = c; c = b; b = a; a = (t1 + t2) >>> 0;
    }
    h0 = (h0 + a) >>> 0; h1 = (h1 + b) >>> 0; h2 = (h2 + c) >>> 0; h3 = (h3 + d) >>> 0;
    h4 = (h4 + e) >>> 0; h5 = (h5 + f) >>> 0; h6 = (h6 + g) >>> 0; h7 = (h7 + h) >>> 0;
  }
  const out = new Uint8Array(32);
  const ov = new DataView(out.buffer);
  [h0, h1, h2, h3, h4, h5, h6, h7].forEach((v, i) => ov.setUint32(i * 4, v, false));
  return out;
}

function hmacSha256(key, msg) {
  const block = 64;
  let k = key.length > block ? sha256(key) : key;
  const ipad = new Uint8Array(block);
  const opad = new Uint8Array(block);
  ipad.set(k);
  opad.set(k);
  for (let i = 0; i < block; i++) {
    ipad[i] ^= 0x36;
    opad[i] ^= 0x5c;
  }
  const inner = new Uint8Array(block + msg.length);
  inner.set(ipad);
  inner.set(msg, block);
  const outer = new Uint8Array(block + 32);
  outer.set(opad);
  outer.set(sha256(inner), block);
  return sha256(outer);
}

function pbkdf2(password, salt, c, dkLen) {
  const blocks = Math.ceil(dkLen / 32);
  const out = new Uint8Array(blocks * 32);
  const saltBlock = new Uint8Array(salt.length + 4);
  saltBlock.set(salt);
  const sv = new DataView(saltBlock.buffer);
  for (let i = 1; i <= blocks; i++) {
    sv.setUint32(salt.length, i, false);
    let u = hmacSha256(password, saltBlock);
    const t = new Uint8Array(u);
    for (let j = 1; j < c; j++) {
      u = hmacSha256(password, u);
      for (let k = 0; k < 32; k++) t[k] ^= u[k];
    }
    out.set(t, (i - 1) * 32);
  }
  return out.subarray(0, dkLen);
}

function rotl(x, n) {
  return ((x << n) | (x >>> (32 - n))) >>> 0;
}

function salsa20_8(bin) {
  const x = new Uint32Array(16);
  const v = new DataView(bin.buffer, bin.byteOffset, 64);
  for (let i = 0; i < 16; i++) x[i] = v.getUint32(i * 4, true);
  const z = x.slice();
  for (let i = 0; i < 4; i++) {
    z[4] ^= rotl((z[0] + z[12]) >>> 0, 7);
    z[8] ^= rotl((z[4] + z[0]) >>> 0, 9);
    z[12] ^= rotl((z[8] + z[4]) >>> 0, 13);
    z[0] ^= rotl((z[12] + z[8]) >>> 0, 18);
    z[9] ^= rotl((z[5] + z[1]) >>> 0, 7);
    z[13] ^= rotl((z[9] + z[5]) >>> 0, 9);
    z[1] ^= rotl((z[13] + z[9]) >>> 0, 13);
    z[5] ^= rotl((z[1] + z[13]) >>> 0, 18);
    z[14] ^= rotl((z[10] + z[6]) >>> 0, 7);
    z[2] ^= rotl((z[14] + z[10]) >>> 0, 9);
    z[6] ^= rotl((z[2] + z[14]) >>> 0, 13);
    z[10] ^= rotl((z[6] + z[2]) >>> 0, 18);
    z[3] ^= rotl((z[15] + z[11]) >>> 0, 7);
    z[7] ^= rotl((z[3] + z[15]) >>> 0, 9);
    z[11] ^= rotl((z[7] + z[3]) >>> 0, 13);
    z[15] ^= rotl((z[11] + z[7]) >>> 0, 18);
    z[1] ^= rotl((z[0] + z[3]) >>> 0, 7);
    z[2] ^= rotl((z[1] + z[0]) >>> 0, 9);
    z[3] ^= rotl((z[2] + z[1]) >>> 0, 13);
    z[0] ^= rotl((z[3] + z[2]) >>> 0, 18);
    z[6] ^= rotl((z[5] + z[4]) >>> 0, 7);
    z[7] ^= rotl((z[6] + z[5]) >>> 0, 9);
    z[4] ^= rotl((z[7] + z[6]) >>> 0, 13);
    z[5] ^= rotl((z[4] + z[7]) >>> 0, 18);
    z[11] ^= rotl((z[10] + z[9]) >>> 0, 7);
    z[8] ^= rotl((z[11] + z[10]) >>> 0, 9);
    z[9] ^= rotl((z[8] + z[11]) >>> 0, 13);
    z[10] ^= rotl((z[9] + z[8]) >>> 0, 18);
    z[12] ^= rotl((z[15] + z[14]) >>> 0, 7);
    z[13] ^= rotl((z[12] + z[15]) >>> 0, 9);
    z[14] ^= rotl((z[13] + z[12]) >>> 0, 13);
    z[15] ^= rotl((z[14] + z[13]) >>> 0, 18);
  }
  const out = new Uint8Array(64);
  const ov = new DataView(out.buffer);
  for (let i = 0; i < 16; i++) ov.setUint32(i * 4, (z[i] + x[i]) >>> 0, true);
  return out;
}

function blockMix(b, r) {
  const x = new Uint8Array(64);
  x.set(b.subarray((2 * r - 1) * 64, 2 * r * 64));
  const y = new Uint8Array(128 * r);
  for (let i = 0; i < 2 * r; i++) {
    const bi = b.subarray(i * 64, (i + 1) * 64);
    for (let j = 0; j < 64; j++) x[j] ^= bi[j];
    const mixed = salsa20_8(x);
    x.set(mixed);
    y.set(mixed, i * 64);
  }
  const out = new Uint8Array(128 * r);
  for (let i = 0; i < r; i++) out.set(y.subarray(i * 2 * 64, (i * 2 + 1) * 64), i * 64);
  for (let i = 0; i < r; i++) out.set(y.subarray((i * 2 + 1) * 64, (i * 2 + 2) * 64), (r + i) * 64);
  return out;
}

function integerify(b, r) {
  const off = (2 * r - 1) * 64;
  const v = new DataView(b.buffer, b.byteOffset + off, 8);
  return v.getUint32(0, true);
}

function smix(b, r, N) {
  const v = new Array(N);
  let x = new Uint8Array(b);
  for (let i = 0; i < N; i++) {
    v[i] = x;
    x = blockMix(x, r);
  }
  for (let i = 0; i < N; i++) {
    const j = integerify(x, r) % N;
    const t = new Uint8Array(x.length);
    const vj = v[j];
    for (let k = 0; k < t.length; k++) t[k] = x[k] ^ vj[k];
    x = blockMix(t, r);
  }
  return x;
}

export function scrypt(password, salt, N, r, p, dkLen) {
  const blen = p * 128 * r;
  let b = pbkdf2(password, salt, 1, blen);
  const out = new Uint8Array(blen);
  for (let i = 0; i < p; i++) {
    const block = b.subarray(i * 128 * r, (i + 1) * 128 * r);
    out.set(smix(block, r, N), i * 128 * r);
  }
  return pbkdf2(password, out, 1, dkLen);
}

export const SCRYPT_N = 1024;
export const SCRYPT_R = 1;
export const SCRYPT_P = 1;

export function aetherScrypt(header) {
  return scrypt(header, header, SCRYPT_N, SCRYPT_R, SCRYPT_P, 32);
}
