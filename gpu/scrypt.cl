// Aether's native proof-of-work on a GPU: scrypt(header, header, N=1024,
// r=1, p=1, 32) over the 128-byte mining header (package miner), as
// x/pow checks it. One work item hashes one nonce.
//
// The header comes in as 32 big-endian words. The nonce is bytes 80..87,
// little-endian, so it is words 20 and 21 byte-swapped. Each work item
// tries nonce = base + its global id.
//
// scrypt's scratchpad (N * 128 bytes = 128 KiB per work item) lives in
// global memory, laid out so neighbouring work items read neighbouring
// words: V[(i*32 + w) * size + gid].

#define N 1024u

__constant uint K256[64] = {
  0x428a2f98u, 0x71374491u, 0xb5c0fbcfu, 0xe9b5dba5u, 0x3956c25bu, 0x59f111f1u, 0x923f82a4u, 0xab1c5ed5u,
  0xd807aa98u, 0x12835b01u, 0x243185beu, 0x550c7dc3u, 0x72be5d74u, 0x80deb1feu, 0x9bdc06a7u, 0xc19bf174u,
  0xe49b69c1u, 0xefbe4786u, 0x0fc19dc6u, 0x240ca1ccu, 0x2de92c6fu, 0x4a7484aau, 0x5cb0a9dcu, 0x76f988dau,
  0x983e5152u, 0xa831c66du, 0xb00327c8u, 0xbf597fc7u, 0xc6e00bf3u, 0xd5a79147u, 0x06ca6351u, 0x14292967u,
  0x27b70a85u, 0x2e1b2138u, 0x4d2c6dfcu, 0x53380d13u, 0x650a7354u, 0x766a0abbu, 0x81c2c92eu, 0x92722c85u,
  0xa2bfe8a1u, 0xa81a664bu, 0xc24b8b70u, 0xc76c51a3u, 0xd192e819u, 0xd6990624u, 0xf40e3585u, 0x106aa070u,
  0x19a4c116u, 0x1e376c08u, 0x2748774cu, 0x34b0bcb5u, 0x391c0cb3u, 0x4ed8aa4au, 0x5b9cca4fu, 0x682e6ff3u,
  0x748f82eeu, 0x78a5636fu, 0x84c87814u, 0x8cc70208u, 0x90befffau, 0xa4506cebu, 0xbef9a3f7u, 0xc67178f2u,
};

inline uint bswap(uint x) {
  return (x >> 24) | ((x >> 8) & 0xff00u) | ((x << 8) & 0xff0000u) | (x << 24);
}

#define ROTR(x, n) rotate((x), (uint)(32 - (n)))

// One SHA-256 compression of the 16 big-endian words w into s.
void sha256_block(uint s[8], const uint in[16]) {
  uint w[64];
  for (int i = 0; i < 16; i++) w[i] = in[i];
  for (int i = 16; i < 64; i++) {
    uint s0 = ROTR(w[i - 15], 7) ^ ROTR(w[i - 15], 18) ^ (w[i - 15] >> 3);
    uint s1 = ROTR(w[i - 2], 17) ^ ROTR(w[i - 2], 19) ^ (w[i - 2] >> 10);
    w[i] = w[i - 16] + s0 + w[i - 7] + s1;
  }
  uint a = s[0], b = s[1], c = s[2], d = s[3], e = s[4], f = s[5], g = s[6], h = s[7];
  for (int i = 0; i < 64; i++) {
    uint S1 = ROTR(e, 6) ^ ROTR(e, 11) ^ ROTR(e, 25);
    uint ch = (e & f) ^ (~e & g);
    uint t1 = h + S1 + ch + K256[i] + w[i];
    uint S0 = ROTR(a, 2) ^ ROTR(a, 13) ^ ROTR(a, 22);
    uint mj = (a & b) ^ (a & c) ^ (b & c);
    uint t2 = S0 + mj;
    h = g; g = f; f = e; e = d + t1; d = c; c = b; b = a; a = t1 + t2;
  }
  s[0] += a; s[1] += b; s[2] += c; s[3] += d; s[4] += e; s[5] += f; s[6] += g; s[7] += h;
}

void sha256_init(uint s[8]) {
  s[0] = 0x6a09e667u; s[1] = 0xbb67ae85u; s[2] = 0x3c6ef372u; s[3] = 0xa54ff53au;
  s[4] = 0x510e527fu; s[5] = 0x9b05688cu; s[6] = 0x1f83d9abu; s[7] = 0x5be0cd19u;
}

// HMAC-SHA256 keyed with the 128-byte header, as PBKDF2 uses it: the key
// is longer than a block, so it is hashed first. istate and ostate are
// the states after the key's ipad and opad blocks.
void hmac_states(const uint hdr[32], uint istate[8], uint ostate[8]) {
  uint s[8], blk[16];
  sha256_init(s);
  sha256_block(s, hdr);
  sha256_block(s, hdr + 16);
  blk[0] = 0x80000000u;
  for (int i = 1; i < 15; i++) blk[i] = 0;
  blk[15] = 128u * 8u;
  sha256_block(s, blk);
  for (int i = 0; i < 8; i++) blk[i] = s[i] ^ 0x36363636u;
  for (int i = 8; i < 16; i++) blk[i] = 0x36363636u;
  sha256_init(istate);
  sha256_block(istate, blk);
  for (int i = 0; i < 8; i++) blk[i] = s[i] ^ 0x5c5c5c5cu;
  for (int i = 8; i < 16; i++) blk[i] = 0x5c5c5c5cu;
  sha256_init(ostate);
  sha256_block(ostate, blk);
}

// HMAC over a 128-byte message (32 big-endian words) followed by the
// 4-byte block index idx: PBKDF2's U_1 for one output block.
void hmac_128(const uint istate[8], const uint ostate[8], const uint msg[32], uint idx, uint out[8]) {
  uint s[8], blk[16];
  for (int i = 0; i < 8; i++) s[i] = istate[i];
  sha256_block(s, msg);
  sha256_block(s, msg + 16);
  blk[0] = idx;
  blk[1] = 0x80000000u;
  for (int i = 2; i < 15; i++) blk[i] = 0;
  blk[15] = (64u + 128u + 4u) * 8u;
  sha256_block(s, blk);
  for (int i = 0; i < 8; i++) blk[i] = s[i];
  blk[8] = 0x80000000u;
  for (int i = 9; i < 15; i++) blk[i] = 0;
  blk[15] = (64u + 32u) * 8u;
  for (int i = 0; i < 8; i++) out[i] = ostate[i];
  sha256_block(out, blk);
}

#define R(a, b) rotate((a), (uint)(b))

void salsa8(uint B[16]) {
  uint x[16];
  for (int i = 0; i < 16; i++) x[i] = B[i];
  for (int i = 0; i < 8; i += 2) {
    x[4] ^= R(x[0] + x[12], 7);   x[8] ^= R(x[4] + x[0], 9);
    x[12] ^= R(x[8] + x[4], 13);  x[0] ^= R(x[12] + x[8], 18);
    x[9] ^= R(x[5] + x[1], 7);    x[13] ^= R(x[9] + x[5], 9);
    x[1] ^= R(x[13] + x[9], 13);  x[5] ^= R(x[1] + x[13], 18);
    x[14] ^= R(x[10] + x[6], 7);  x[2] ^= R(x[14] + x[10], 9);
    x[6] ^= R(x[2] + x[14], 13);  x[10] ^= R(x[6] + x[2], 18);
    x[3] ^= R(x[15] + x[11], 7);  x[7] ^= R(x[3] + x[15], 9);
    x[11] ^= R(x[7] + x[3], 13);  x[15] ^= R(x[11] + x[7], 18);
    x[1] ^= R(x[0] + x[3], 7);    x[2] ^= R(x[1] + x[0], 9);
    x[3] ^= R(x[2] + x[1], 13);   x[0] ^= R(x[3] + x[2], 18);
    x[6] ^= R(x[5] + x[4], 7);    x[7] ^= R(x[6] + x[5], 9);
    x[4] ^= R(x[7] + x[6], 13);   x[5] ^= R(x[4] + x[7], 18);
    x[11] ^= R(x[10] + x[9], 7);  x[8] ^= R(x[11] + x[10], 9);
    x[9] ^= R(x[8] + x[11], 13);  x[10] ^= R(x[9] + x[8], 18);
    x[12] ^= R(x[15] + x[14], 7); x[13] ^= R(x[12] + x[15], 9);
    x[14] ^= R(x[13] + x[12], 13); x[15] ^= R(x[14] + x[13], 18);
  }
  for (int i = 0; i < 16; i++) B[i] += x[i];
}

// scrypt's BlockMix with r = 1 on X (32 little-endian words).
void blockmix(uint X[32]) {
  uint y[16];
  for (int i = 0; i < 16; i++) y[i] = X[i] ^ X[16 + i];
  salsa8(y);
  for (int i = 0; i < 16; i++) X[i] = y[i];
  for (int i = 0; i < 16; i++) y[i] ^= X[16 + i];
  salsa8(y);
  for (int i = 0; i < 16; i++) X[16 + i] = y[i];
}

// The scrypt hash of hdr (32 big-endian words) into out (8 big-endian
// words: the hash's 32 bytes in order).
void scrypt_hash(const uint hdr[32], __global uint *V, uint gid, uint size, uint out[8]) {
  uint istate[8], ostate[8], t[8], X[32], msg[32];
  hmac_states(hdr, istate, ostate);
  for (uint b = 0; b < 4; b++) {
    hmac_128(istate, ostate, hdr, b + 1, t);
    for (int i = 0; i < 8; i++) X[b * 8 + i] = bswap(t[i]);
  }
  for (uint i = 0; i < N; i++) {
    for (int w = 0; w < 32; w++) V[(i * 32 + w) * size + gid] = X[w];
    blockmix(X);
  }
  for (uint i = 0; i < N; i++) {
    uint j = X[16] & (N - 1);
    for (int w = 0; w < 32; w++) X[w] ^= V[(j * 32 + w) * size + gid];
    blockmix(X);
  }
  for (int i = 0; i < 32; i++) msg[i] = bswap(X[i]);
  hmac_128(istate, ostate, msg, 1, out);
}

void load_header(__constant uint *header, ulong nonce, uint hdr[32]) {
  for (int i = 0; i < 32; i++) hdr[i] = header[i];
  hdr[20] = bswap((uint)nonce);
  hdr[21] = bswap((uint)(nonce >> 32));
}

// hashes: each work item's hash, for checking against Go's scrypt.
__kernel void scrypt_hashes(__constant uint *header, ulong base, __global uint *V, __global uint *hashes) {
  uint gid = get_global_id(0), size = get_global_size(0);
  uint hdr[32], out[8];
  load_header(header, base + gid, hdr);
  scrypt_hash(hdr, V, gid, size, out);
  for (int i = 0; i < 8; i++) hashes[gid * 8 + i] = out[i];
}

// search: found[0] gets 1 and found[1..2] the nonce of any hash below
// target (8 big-endian words), when one of this batch meets it.
__kernel void scrypt_search(__constant uint *header, ulong base, __global uint *V, __constant uint *target, __global uint *found) {
  uint gid = get_global_id(0), size = get_global_size(0);
  uint hdr[32], out[8];
  ulong nonce = base + gid;
  load_header(header, nonce, hdr);
  scrypt_hash(hdr, V, gid, size, out);
  for (int i = 0; i < 8; i++) {
    if (out[i] < target[i]) {
      if (atomic_cmpxchg(&found[0], 0u, 1u) == 0u) {
        found[1] = (uint)nonce;
        found[2] = (uint)(nonce >> 32);
      }
      return;
    }
    if (out[i] > target[i]) return;
  }
}
