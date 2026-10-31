// The miner key, kept in this browser's storage. It is a real Aether
// account: rewards are paid to it, and anyone who can read this browser's
// storage can spend them. For the testnet; keep the recovery phrase.
import { Key } from "./chain";

const STORE = "aether-miner-key-v1";

export interface Saved {
  mnemonic: string;
  address: string;
  createdAt: number;
}

export function loadKey(): { key: Key; saved: Saved } | null {
  try {
    const raw = localStorage.getItem(STORE);
    if (!raw) return null;
    const saved = JSON.parse(raw) as Saved;
    const key = Key.fromMnemonic(saved.mnemonic);
    if (key.address !== saved.address) return null;
    return { key, saved };
  } catch {
    return null;
  }
}

export function saveKey(mnemonic: string): { key: Key; saved: Saved } {
  const key = Key.fromMnemonic(mnemonic);
  const saved: Saved = { mnemonic: mnemonic.trim().split(/\s+/).join(" "), address: key.address, createdAt: Date.now() };
  try {
    localStorage.setItem(STORE, JSON.stringify(saved));
  } catch {
    // private window: the key lasts as long as the page
  }
  return { key, saved };
}

export function forgetKey(): void {
  try {
    localStorage.removeItem(STORE);
  } catch {
    // nothing stored
  }
}

export function newKey(): { key: Key; mnemonic: string } {
  return Key.generate();
}
