"""Aether's public networks, as every client names them (the Go wallet's wallet.Network; shared
vectors: clients/testdata/networks.json). Pick one with AetherClient(network="mainnet", rpc=...)."""
from dataclasses import dataclass, field
from typing import Dict, Optional, Tuple


@dataclass(frozen=True)
class NetworkUSDC:
    """The USDC a network's tools accept. On mainnet the base denom and issuer are known (Circle's USDC
    on Injective) but path is "" until the Injective channel is opened and listed after launch."""
    path: str
    base_denom: str
    issuer: str


@dataclass(frozen=True)
class Network:
    name: str
    chain_id: str
    rpc: str  # "" where none is published yet: give your own node then
    grpc: str
    faucet: str  # "" on a network with none: mainnet has no faucet
    job_board: str
    usdc: NetworkUSDC
    real: bool  # funds have real value: keep keys encrypted; nothing hands out free funds


TESTNET = Network(
    name="testnet",
    chain_id="aether-testnet-1",
    rpc="https://rpc.157-245-252-221.sslip.io",
    grpc="grpc.157-245-252-221.sslip.io:443",
    faucet="https://faucet.157-245-252-221.sslip.io/request",
    job_board="https://explorer.157-245-252-221.sslip.io/jobs",
    usdc=NetworkUSDC("transfer/channel-2", "erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d", "Injective"),
    real=False,
)

MAINNET = Network(
    name="mainnet",
    chain_id="aether-1",
    rpc="",
    grpc="",
    faucet="",
    job_board="",
    usdc=NetworkUSDC("", "erc20:0xa00C59fF5a080D2b954d0c75e46E22a0c371235a", "Injective"),
    real=True,
)

NETWORKS: Tuple[Network, ...] = (TESTNET, MAINNET)


def network_by_name(name: str) -> Network:
    """The network called name ("testnet", "mainnet") or with that chain ID."""
    for n in NETWORKS:
        if name.lower() == n.name or name == n.chain_id:
            return n
    raise ValueError(f'unknown network "{name}": want testnet or mainnet')


def network_for_chain(chain_id: str) -> Optional[Network]:
    """The public network with chain_id, or None for any other chain (a devnet, a rehearsal)."""
    for n in NETWORKS:
        if n.chain_id == chain_id:
            return n
    return None


def usdc_with(n: Network, channel: Optional[str] = None, path: Optional[str] = None,
              base_denom: Optional[str] = None, issuer: Optional[str] = None) -> Dict[str, str]:
    """What the given USDC setting leaves out, from the network's, as usdc_* keyword arguments: given only
    a path on mainnet, it is Circle's USDC on Injective over it, not Noble's uusdc, which a bare path
    otherwise means. Nothing given: the network's own USDC, if its path is open."""
    if not (channel or path or base_denom or issuer):
        if not n.usdc.path:
            return {}
        return {"usdc_path": n.usdc.path, "usdc_base_denom": n.usdc.base_denom, "usdc_issuer": n.usdc.issuer}
    out = {k: v for k, v in (("usdc_channel", channel), ("usdc_path", path), ("usdc_base_denom", base_denom),
                             ("usdc_issuer", issuer)) if v}
    if path and not base_denom and not channel and n.usdc.base_denom:
        out["usdc_base_denom"] = n.usdc.base_denom
        out.setdefault("usdc_issuer", n.usdc.issuer)
    return out
