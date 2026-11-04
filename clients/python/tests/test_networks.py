"""clients/testdata/networks.json: the public networks every client names (the Go wallet's
wallet/network.go, the TypeScript client's src/networks.ts)."""
import json
import pathlib
import unittest

from aether_client import NETWORKS, AetherClient, network_by_name, network_for_chain, usdc_with

FILE = json.loads((pathlib.Path(__file__).parents[2] / "testdata" / "networks.json").read_text())


def kwargs(u):
    keys = {"channel": "usdc_channel", "path": "usdc_path", "baseDenom": "usdc_base_denom", "issuer": "usdc_issuer"}
    return {keys[k]: v for k, v in u.items() if v}


class TestNetworks(unittest.TestCase):
    def test_networks_match_the_shared_vectors(self):
        self.assertEqual(len(NETWORKS), len(FILE["networks"]))
        for n, v in zip(NETWORKS, FILE["networks"]):
            self.assertEqual(n.name, v["name"])
            self.assertEqual(n.chain_id, v["chainId"])
            self.assertEqual(n.rpc, v["rpc"])
            self.assertEqual(n.grpc, v["grpc"])
            self.assertEqual(n.faucet, v["faucet"])
            self.assertEqual(n.job_board, v["jobBoard"])
            self.assertEqual((n.usdc.path, n.usdc.base_denom, n.usdc.issuer),
                             (v["usdc"]["path"], v["usdc"]["baseDenom"], v["usdc"]["issuer"]))
            self.assertEqual(n.real, v["real"])
            self.assertIs(network_by_name(n.name), n)
            self.assertIs(network_by_name(n.chain_id), n)
            self.assertIs(network_for_chain(n.chain_id), n)
        with self.assertRaises(ValueError):
            network_by_name("devnet")
        self.assertIsNone(network_for_chain("aether-rc-1"))

    def test_usdc_with_matches_the_shared_vectors(self):
        for c in FILE["usdcWith"]:
            g = c["given"]
            got = usdc_with(network_by_name(c["network"]), g.get("channel"), g.get("path"), g.get("baseDenom"),
                            g.get("issuer"))
            self.assertEqual(got, kwargs(c["usdc"]), c["name"])

    def test_a_client_on_a_network(self):
        t = AetherClient(network="testnet")
        self.assertEqual(t.chain_id, "aether-testnet-1")
        self.assertIsNotNone(t.assets.by_symbol("USDC"))

        with self.assertRaisesRegex(ValueError, "no public RPC for mainnet yet"):
            AetherClient(network="mainnet")
        m = AetherClient("http://localhost:26657", network="mainnet", usdc_path="transfer/channel-7")
        self.assertEqual(m.chain_id, "aether-1")
        self.assertEqual(m.assets.by_symbol("USDC").origin, "Injective over transfer/channel-7")
        self.assertIsNone(AetherClient("http://localhost:26657", network="mainnet").assets.by_symbol("USDC"))
        with self.assertRaisesRegex(ValueError, "is chain aether-1"):
            AetherClient("http://x", chain_id="aether-testnet-1", network="mainnet")
        self.assertEqual(AetherClient("http://x").chain_id, "aether-testnet-1", "no network: as before")


if __name__ == "__main__":
    unittest.main()
