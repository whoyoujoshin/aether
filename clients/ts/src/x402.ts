import { base64 } from "@scure/base";
import { AetherClient } from "./client.js";
import { Key, isAddress } from "./keys.js";
import { buildSend, DEFAULT_GAS_LIMIT } from "./tx.js";

// The standard x402 (v2) "exact" scheme on Aether, for @x402/core's
// x402Client and @x402/fetch (or any client with the same interface):
//
//   import { x402Client } from "@x402/core/client";
//   import { wrapFetchWithPayment } from "@x402/fetch";
//   import { ExactAetherScheme } from "aether-chain-client/x402";
//
//   const pay = new x402Client().register("cosmos:aether-testnet-1", new ExactAetherScheme(client, key));
//   const fetchWithPay = wrapFetchWithPayment(fetch, pay);
//
// The payment is a signed bank send of exactly the amount to payTo, which
// the resource server's facilitator checks and broadcasts (see the Go
// package x402 and cmd/facilitator). It needs nothing from @x402/* at run
// time: the types below match theirs.

/** The CAIP-2 network of an Aether chain: "cosmos:aether-testnet-1" for the testnet. */
export function x402Network(chainId: string): string {
  return `cosmos:${chainId}`;
}

/** One way a resource accepts payment (x402 v2). On Aether, asset is a denom and amount is in its base units. */
export interface X402PaymentRequirements {
  scheme: string;
  network: string;
  asset: string;
  amount: string;
  payTo: string;
  maxTimeoutSeconds: number;
  extra?: Record<string, unknown>;
}

/** The exact scheme's payload on Aether: the signed transaction (protobuf TxRaw), base64. */
export interface ExactAetherPayload {
  transaction: string;
}

export interface ExactAetherOptions {
  /** Gas limit of the payment transaction (default 400,000). */
  gasLimit?: bigint | number;
  /** Largest amount, in the asset's base units, one payment may be ("asset" → amount). Unlisted assets are uncapped here; x402Client's spend controls apply too. */
  maxAmount?: Record<string, bigint>;
}

/**
 * The x402 "exact" scheme on Aether, as a client: it pays a 402 by signing
 * a send of exactly what the requirement asks, from key's account. The
 * account pays the transaction's fee (zero on the testnet) and must exist
 * on chain. Payments from one key go one at a time: each is signed for
 * the account's next sequence, so a second one built before the first is
 * in a block is refused by the facilitator.
 */
export class ExactAetherScheme {
  readonly scheme = "exact";

  constructor(
    readonly client: AetherClient,
    readonly key: Key,
    readonly opts: ExactAetherOptions = {},
  ) {}

  /** USDC, for x402Client's USD spend caps, when the client knows it. */
  findDefaultAsset = (asset: string, network: string): { asset: string; decimals: number; symbol: string } | undefined => {
    if (network !== x402Network(this.client.chainId)) return undefined;
    const a = this.client.assets.byDenom(asset);
    return a && a.symbol === "USDC" ? { asset: a.denom, decimals: a.decimals, symbol: a.symbol } : undefined;
  };

  async createPaymentPayload(
    x402Version: number,
    req: X402PaymentRequirements,
    context?: { maxAmountPerPayment?: string },
  ): Promise<{ x402Version: number; payload: Record<string, unknown> }> {
    if (x402Version !== 2) throw new Error(`x402 version ${x402Version}: the exact scheme on Aether is v2`);
    if (req.scheme !== this.scheme) throw new Error(`scheme "${req.scheme}" isn't exact`);
    const network = x402Network(this.client.chainId);
    if (req.network !== network) throw new Error(`network "${req.network}": this client pays on ${network}`);
    if (!/^[1-9][0-9]*$/.test(req.amount)) throw new Error(`amount "${req.amount}" isn't a positive integer in base units`);
    if (!/^[a-zA-Z][a-zA-Z0-9/:._-]{2,127}$/.test(req.asset)) throw new Error(`asset "${req.asset}" isn't a denom`);
    if (!isAddress(req.payTo)) throw new Error(`payTo "${req.payTo}" isn't an Aether address`);
    const amount = BigInt(req.amount);
    const cap = this.opts.maxAmount?.[req.asset];
    if (cap !== undefined && amount > cap) throw new Error(`${req.amount}${req.asset} is over this client's limit of ${cap}${req.asset}`);
    if (context?.maxAmountPerPayment !== undefined && amount > BigInt(context.maxAmountPerPayment)) {
      throw new Error(`${req.amount}${req.asset} is over the spend limit of ${context.maxAmountPerPayment}`);
    }

    const info = await this.client.accountInfo(this.key.address);
    if (!info) throw new Error(`account ${this.key.address} doesn't exist on chain yet: fund it first`);
    const signed = buildSend(this.key, {
      chainId: this.client.chainId,
      accountNumber: info.accountNumber,
      sequence: info.sequence,
      to: req.payTo,
      amountUaeth: amount,
      denom: req.asset,
      gasLimit: this.opts.gasLimit ?? DEFAULT_GAS_LIMIT,
    });
    const payload: ExactAetherPayload = { transaction: base64.encode(signed.txBytes) };
    return { x402Version, payload: { ...payload } };
  }
}
