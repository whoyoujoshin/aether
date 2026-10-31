import { WORDS } from "./wordlist";

const STORAGE_KEY = "aether-miner-wallet-v1";

export type StoredWallet = {
  mnemonic: string;
  createdAt: number;
};

function bytesToBits(bytes: Uint8Array): number[] {
  const bits: number[] = [];
  for (const byte of bytes) {
    for (let i = 7; i >= 0; i--) bits.push((byte >> i) & 1);
  }
  return bits;
}

function bitsToInt(bits: number[], start: number, len: number): number {
  let n = 0;
  for (let i = 0; i < len; i++) n = (n << 1) | (bits[start + i] ?? 0);
  return n;
}

async function sha256(data: Uint8Array): Promise<Uint8Array> {
  const copy = new ArrayBuffer(data.byteLength);
  new Uint8Array(copy).set(data);
  const digest = await crypto.subtle.digest("SHA-256", copy);
  return new Uint8Array(digest);
}

export async function createMnemonic(): Promise<string> {
  const entropy = crypto.getRandomValues(new Uint8Array(16));
  const hash = await sha256(entropy);
  const bits = bytesToBits(entropy);
  const check = bytesToBits(hash.subarray(0, 1)).slice(0, 4);
  bits.push(...check);
  const words: string[] = [];
  for (let i = 0; i < 12; i++) words.push(WORDS[bitsToInt(bits, i * 11, 11)] ?? "");
  return words.join(" ");
}

export async function mnemonicValid(phrase: string): Promise<boolean> {
  const words = phrase.trim().toLowerCase().split(/\s+/);
  if (words.length !== 12) return false;
  const indexes = words.map((w) => WORDS.indexOf(w as (typeof WORDS)[number]));
  if (indexes.some((i) => i < 0)) return false;
  const bits: number[] = [];
  for (const idx of indexes) {
    for (let i = 10; i >= 0; i--) bits.push((idx >> i) & 1);
  }
  const entropyBits = bits.slice(0, 128);
  const checksum = bits.slice(128, 132);
  const entropy = new Uint8Array(16);
  for (let i = 0; i < 16; i++) entropy[i] = bitsToInt(entropyBits, i * 8, 8);
  const hash = await sha256(entropy);
  const expect = bytesToBits(hash.subarray(0, 1)).slice(0, 4);
  return checksum.every((b, i) => b === expect[i]);
}

export async function fingerprint(phrase: string): Promise<string> {
  const hash = await sha256(new TextEncoder().encode(phrase.trim().toLowerCase()));
  const hex = [...hash.subarray(0, 4)].map((b) => b.toString(16).padStart(2, "0")).join("");
  return `miner-${hex}`;
}

export function loadWallet(): StoredWallet | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as StoredWallet;
    if (!parsed?.mnemonic || typeof parsed.createdAt !== "number") return null;
    return parsed;
  } catch {
    return null;
  }
}

export function saveWallet(mnemonic: string): StoredWallet {
  const wallet: StoredWallet = { mnemonic, createdAt: Date.now() };
  localStorage.setItem(STORAGE_KEY, JSON.stringify(wallet));
  return wallet;
}

export function clearWallet(): void {
  localStorage.removeItem(STORAGE_KEY);
}

export function downloadPhrase(phrase: string): void {
  const blob = new Blob(
    [
      "Aether Miner recovery phrase\n",
      "Keep this private. Anyone with it can restore this miner on another browser.\n",
      "This is not a spendable Aether account key.\n\n",
      phrase.trim(),
      "\n",
    ],
    { type: "text/plain" },
  );
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = "aether-miner-recovery.txt";
  a.click();
  URL.revokeObjectURL(url);
}
