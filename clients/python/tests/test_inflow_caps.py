"""x/inflowcap Caps answers: clients/testdata/inflowcaps.json holds them as the node encodes them
(x/inflowcap's TestCapsVectors checks the same file against the Go types)."""

import base64
import json
import pathlib

from aether_client import AetherClient, INFLOW_CAPS_PATH, RpcError, decode_inflow_caps
from aether_client.agent_tools import AetherToolkit

FILE = json.loads((pathlib.Path(__file__).parents[2] / "testdata" / "inflowcaps.json").read_text())


def test_shared_vectors():
    for v in FILE["vectors"]:
        got = [{"channel": c.channel, "denom": c.denom, "voucherDenom": c.voucher_denom,
                "cap": str(c.cap), "supply": str(c.supply), "room": str(c.room), "inFlight": str(c.in_flight)}
               for c in decode_inflow_caps(base64.b64decode(v["value"]))]
        assert got == v["expected"], v["name"]


class FakeRpc:
    """Answers abci_query for the Caps path as a node does."""

    def __init__(self, value=None, code=0, log=""):
        self.value, self.code, self.log, self.paths = value, code, log, []

    def abci_query(self, path, data):
        self.paths.append(path)
        if self.code:
            raise RpcError(f"{path}: {self.log}", self.code)
        return base64.b64decode(self.value) if self.value else b""


def client(rpc):
    c = AetherClient("http://node.test", usdc_path="transfer/channel-2",
                     usdc_base_denom="erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d", usdc_issuer="Injective")
    c.rpc = rpc
    return c


def test_client_and_toolkit():
    testnet = FILE["vectors"][1]
    rpc = FakeRpc(testnet["value"])
    c = client(rpc)
    caps = c.inflow_caps()
    assert rpc.paths == [INFLOW_CAPS_PATH]
    assert caps.active
    assert caps.room(testnet["expected"][0]["voucherDenom"]) == (89_990_000, True, True)
    assert caps.room("ibc/OTHER") == (0, True, False)

    r = AetherToolkit(c, faucet_url=None).call("aether_get_inflow_caps", {})
    assert r["active"] is True
    assert (r["caps"][0]["symbol"], r["caps"][0]["roomDisplay"]) == ("USDC", "89.99 USDC")
    assert (r["caps"][1]["symbol"], r["caps"][1]["roomDisplay"]) == ("OSMO", "0 OSMO")
    assert "refused" in r["message"]


def test_no_inflowcap_is_no_cap():
    old = client(FakeRpc(code=FILE["inactive"]["code"], log=FILE["inactive"]["log"]))
    assert old.inflow_caps().active is False
    r = AetherToolkit(old, faucet_url=None).get_inflow_caps()
    assert r["active"] is False and "no inflow cap yet" in r["message"]

    broken = client(FakeRpc(code=1, log="internal"))
    assert AetherToolkit(broken, faucet_url=None).get_inflow_caps()["error"]["code"] == "UNAVAILABLE"
