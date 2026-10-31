import { createServerFn } from "@tanstack/react-start";

const RPC = "https://rpc.157-245-252-221.sslip.io/status";
const EXPLORER = "https://explorer.157-245-252-221.sslip.io";
const ADDRESS = /^aether1[qpzry9x8gf2tvdw0s3jn54khce6mua7l]{20,128}$/;

export type ChainStatus =
  | {
      ok: true;
      network: string;
      height: string;
      blockTime: string;
      catchingUp: boolean;
    }
  | { ok: false; error: string };

export const getChainStatus = createServerFn({ method: "GET" }).handler(async (): Promise<ChainStatus> => {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), 8000);
  try {
    const res = await fetch(RPC, { signal: ctrl.signal });
    if (!res.ok) return { ok: false, error: `The testnet did not answer (${res.status}).` };
    const json = (await res.json()) as {
      result?: {
        node_info?: { network?: string };
        sync_info?: { latest_block_height?: string; latest_block_time?: string; catching_up?: boolean };
      };
    };
    const sync = json.result?.sync_info;
    const network = json.result?.node_info?.network ?? "";
    if (!sync?.latest_block_height || !network) return { ok: false, error: "The testnet sent an unexpected reply." };
    return {
      ok: true,
      network,
      height: sync.latest_block_height,
      blockTime: sync.latest_block_time ?? "",
      catchingUp: Boolean(sync.catching_up),
    };
  } catch {
    return { ok: false, error: "Could not reach the Aether testnet. Check your connection and try again." };
  } finally {
    clearTimeout(timer);
  }
});

export type AddressTx = {
  hash: string;
  direction: string;
  amount: string;
  timestamp: string;
  failed: boolean;
};

export type AddressBalance =
  | { ok: true; address: string; uaeth: string; txs: AddressTx[] }
  | { ok: false; error: string };

export const getAddressBalance = createServerFn({ method: "POST" })
  .validator((address: string) => address.trim().toLowerCase())
  .handler(async ({ data: address }): Promise<AddressBalance> => {
    if (!ADDRESS.test(address)) {
      return { ok: false, error: "Paste a full aether1 address. The 12 recovery words are not one." };
    }
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), 8000);
    try {
      const res = await fetch(`${EXPLORER}/api/address?addr=${encodeURIComponent(address)}`, {
        signal: ctrl.signal,
      });
      if (!res.ok) {
        return { ok: false, error: "The testnet did not recognize that address." };
      }
      const json = (await res.json()) as {
        address?: string;
        balance?: string;
        transactions?: { hash?: string; direction?: string; amount?: string; timestamp?: string; code?: number }[];
      };
      if (!json.address || json.balance == null) {
        return { ok: false, error: "The explorer sent an unexpected reply." };
      }
      const txs = (json.transactions ?? []).slice(0, 5).map((tx) => ({
        hash: tx.hash ?? "",
        direction: tx.direction ?? "",
        amount: tx.amount ?? "",
        timestamp: tx.timestamp ?? "",
        failed: (tx.code ?? 0) !== 0,
      }));
      return { ok: true, address: json.address, uaeth: json.balance, txs };
    } catch {
      return { ok: false, error: "Could not reach the Aether explorer. Try again in a moment." };
    } finally {
      clearTimeout(timer);
    }
  });
