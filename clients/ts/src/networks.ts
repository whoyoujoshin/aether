import type { UsdcSetting } from "./amount.js";

/**
 * Aether's public networks, as every client names them (the Go wallet's
 * wallet.Network; shared vectors: clients/testdata/networks.json). Pick one
 * with `new AetherClient({ network: "mainnet", rpc })`.
 */
export interface Network {
  name: "testnet" | "mainnet";
  chainId: string;
  /** Public endpoints, "" where none is published yet: give your own then. */
  rpc: string;
  grpc: string;
  /** "" on a network with none: mainnet has no faucet. */
  faucet: string;
  jobBoard: string;
  /**
   * The USDC its tools accept. On mainnet the base denom and issuer are known
   * (Circle's USDC on Injective) but the path is "" until the Injective
   * channel is opened and listed after launch.
   */
  usdc: { path: string; baseDenom: string; issuer: string };
  /** Funds have real value: keep keys encrypted; nothing hands out free funds. */
  real: boolean;
}

export const TESTNET: Network = {
  name: "testnet",
  chainId: "aether-testnet-1",
  rpc: "https://rpc.157-245-252-221.sslip.io",
  grpc: "grpc.157-245-252-221.sslip.io:443",
  faucet: "https://faucet.157-245-252-221.sslip.io/request",
  jobBoard: "https://explorer.157-245-252-221.sslip.io/jobs",
  usdc: { path: "transfer/channel-2", baseDenom: "erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d", issuer: "Injective" },
  real: false,
};

export const MAINNET: Network = {
  name: "mainnet",
  chainId: "aether-1",
  rpc: "",
  grpc: "",
  faucet: "",
  jobBoard: "",
  usdc: { path: "", baseDenom: "erc20:0xa00C59fF5a080D2b954d0c75e46E22a0c371235a", issuer: "Injective" },
  real: true,
};

export const NETWORKS: readonly Network[] = [TESTNET, MAINNET];

/** The network called name ("testnet", "mainnet") or with that chain ID. */
export function networkByName(name: string): Network {
  const n = NETWORKS.find((n) => n.name === name.toLowerCase() || n.chainId === name);
  if (!n) throw new Error(`unknown network "${name}": want testnet or mainnet`);
  return n;
}

/** The public network with chainId, or undefined for any other chain (a devnet, a rehearsal). */
export function networkForChain(chainId: string): Network | undefined {
  return NETWORKS.find((n) => n.chainId === chainId);
}

/**
 * What given leaves out of the USDC setting, from the network's: given only
 * a path on mainnet, it is Circle's USDC on Injective over it, not Noble's
 * uusdc, which a bare path otherwise means. Nothing given: the network's own
 * USDC, if its path is open.
 */
export function usdcWith(n: Network, given: UsdcSetting): UsdcSetting {
  const empty = !given.usdcChannel && !given.usdcPath && !given.usdcBaseDenom && !given.usdcIssuer;
  if (empty) {
    if (!n.usdc.path) return {};
    return { usdcPath: n.usdc.path, usdcBaseDenom: n.usdc.baseDenom, usdcIssuer: n.usdc.issuer };
  }
  if (given.usdcPath && !given.usdcBaseDenom && !given.usdcChannel && n.usdc.baseDenom) {
    return { ...given, usdcBaseDenom: n.usdc.baseDenom, usdcIssuer: given.usdcIssuer || n.usdc.issuer };
  }
  return given;
}
