// The node's CometBFT RPC (default port 26657), which every Aether node
// serves: queries go through abci_query with gRPC method paths, so no
// REST/gRPC gateway is needed.

import { base64 } from "@scure/base";
import { bytesToHex } from "@noble/hashes/utils.js";

export class RpcError extends Error {
  constructor(message: string, readonly code?: number) {
    super(message);
  }
}

export interface TxEvent {
  type: string;
  attributes: { key: string; value: string }[];
}

export interface TxResult {
  hash: string;
  height: number;
  code: number;
  codespace: string;
  log: string;
  events: TxEvent[];
  tx: Uint8Array;
}

export class Rpc {
  constructor(readonly url: string, private readonly fetchImpl: typeof fetch = fetch) {
    this.url = url.replace(/\/+$/, "");
  }

  async call<T>(method: string, params: Record<string, unknown> = {}): Promise<T> {
    const resp = await this.fetchImpl(this.url, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ jsonrpc: "2.0", id: 1, method, params }),
    });
    if (!resp.ok && resp.status !== 500) throw new RpcError(`${method}: HTTP ${resp.status}`);
    const body = (await resp.json()) as { result?: T; error?: { code: number; message: string; data?: string } };
    if (body.error) throw new RpcError(`${method}: ${body.error.data || body.error.message}`, body.error.code);
    return body.result as T;
  }

  async latestHeight(): Promise<number> {
    const r = await this.call<{ sync_info: { latest_block_height: string } }>("status");
    return Number(r.sync_info.latest_block_height);
  }

  /** Runs a gRPC query method (e.g. "/cosmos.bank.v1beta1.Query/Balance") against the latest state. */
  async abciQuery(path: string, data: Uint8Array): Promise<Uint8Array> {
    const r = await this.call<{ response: { code: number; log: string; value: string | null } }>("abci_query", {
      path,
      data: bytesToHex(data),
    });
    if (r.response.code !== 0) throw new RpcError(`${path}: ${r.response.log}`, r.response.code);
    return r.response.value ? base64.decode(r.response.value) : new Uint8Array();
  }

  async broadcastSync(tx: Uint8Array): Promise<{ code: number; codespace: string; log: string; hash: string }> {
    return this.call("broadcast_tx_sync", { tx: base64.encode(tx) });
  }

  /** A transaction in a block, or undefined if the node has none by that hash. */
  async tx(hash: string): Promise<TxResult | undefined> {
    try {
      return toTxResult(await this.call<RawTx>("tx", { hash: base64.encode(hexToBytes(hash)) }));
    } catch (e) {
      if (e instanceof RpcError && /not found/i.test(e.message)) return undefined;
      throw e;
    }
  }

  async txSearch(query: string, page: number, perPage: number): Promise<{ txs: TxResult[]; total: number }> {
    const r = await this.call<{ txs: RawTx[]; total_count: string }>("tx_search", {
      query,
      page: String(page),
      per_page: String(perPage),
      order_by: "asc",
    });
    return { txs: r.txs.map(toTxResult), total: Number(r.total_count) };
  }
}

interface RawTx {
  hash: string;
  height: string;
  tx: string;
  tx_result: { code: number; codespace?: string; log: string; events?: TxEvent[] };
}

function toTxResult(r: RawTx): TxResult {
  return {
    hash: r.hash.toUpperCase(),
    height: Number(r.height),
    code: r.tx_result.code,
    codespace: r.tx_result.codespace ?? "",
    log: r.tx_result.log,
    events: r.tx_result.events ?? [],
    tx: base64.decode(r.tx),
  };
}

function hexToBytes(hex: string): Uint8Array {
  if (!/^[0-9a-fA-F]*$/.test(hex) || hex.length % 2) throw new Error(`invalid hash "${hex}"`);
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i++) out[i] = parseInt(hex.slice(2 * i, 2 * i + 2), 16);
  return out;
}
