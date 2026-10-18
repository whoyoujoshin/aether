import json
import pathlib
import urllib.parse

from aether_client import AetherClient
from aether_client.agent_tools import AetherToolkit
from aether_client.swap_quote import SwapQuoter

# clients/testdata/swapquote.json: Osmosis's recorded answers for pool 1370
# and what agentmcp's quote_swap makes of them; this client must agree.
FILE = json.loads((pathlib.Path(__file__).parents[2] / "testdata" / "swapquote.json").read_text())
POOL_PATH = "/osmosis/poolmanager/v1beta1/pools/1370"
ESTIMATE_PATH = "/osmosis/poolmanager/v1beta1/1370/estimate/single_pool_swap_exact_amount_in"


def osmosis(v, calls=None, status=200):
    def fetch(url):
        u = urllib.parse.urlsplit(url)
        if calls is not None:
            calls.append(u.path + ("?" + u.query if u.query else ""))
        if status != 200:
            return status, json.dumps({"code": 3, "message": "token amount calculated (0) must be positive: invalid request"})
        if u.path == POOL_PATH:
            return 200, json.dumps(v["pool"])
        if u.path == ESTIMATE_PATH:
            return 200, json.dumps(v["estimate"])
        return 404, "not found"
    return fetch


def test_shared_vectors():
    for v in FILE["vectors"]:
        q = SwapQuoter(["http://osmosis.test"], fetch=osmosis(v))
        r = q.quote(v["input"]["amount"], v["input"].get("slippagePercent", 0))
        assert "error" not in r, (v["name"], r)
        got = {
            "out_uosmo": r["out"]["uosmo"], "out_osmo": r["out"]["osmo"], "denom_on_aether": r["out"]["denomOnAether"],
            "spot_price": r["spotPrice"], "price": r["price"], "cost_percent": r["costPercent"],
            "swap_fee_percent": r["swapFeePercent"], "reserves_aeth": r["reserves"]["aeth"],
            "reserves_osmo": r["reserves"]["osmo"], "slippage_percent": r["slippagePercent"],
            "min_output_uosmo": r["minOutput"]["uosmo"],
        }
        assert got == v["want"], v["name"]


def test_request_cache_pacing_fallback():
    v = FILE["vectors"][0]
    calls = []
    now = [1_000.0]
    good = osmosis(v, calls)

    def fetch(url):
        if urllib.parse.urlsplit(url).hostname == "down.test":
            return 403, ""
        return good(url)

    q = SwapQuoter(["http://down.test", "http://osmosis.test/"], fetch=fetch, clock=lambda: now[0])
    q.quote("1 AETH")
    q.quote("1 AETH")
    assert calls == [
        POOL_PATH,
        ESTIMATE_PATH + "?pool_id=1370&token_in=1000000ibc%2F8277E1557EB379889BA54DFD0F19EDCFF907E4B3414E6B99D098F241CAED3BE1"
                        "&token_out_denom=uosmo",
    ], "the second call is cached; the refusing endpoint is skipped"
    for i in range(40):
        q.quote(f"{i + 2} AETH")
    paced = q.quote("99 AETH")
    assert paced["error"]["code"] == "QUOTE_UNAVAILABLE"
    assert 1 <= paced["error"]["retryAfterSeconds"] <= 61
    now[0] += 61
    assert "error" not in q.quote("99 AETH"), "a minute later it asks again"


def test_refusals():
    v = FILE["vectors"][0]
    calls = []
    q = SwapQuoter(["http://osmosis.test"], fetch=osmosis(v, calls))
    for amount, slip, code in [("5 USDC", 0, "INVALID_AMOUNT"), ("1", 0, "INVALID_AMOUNT"), ("0 AETH", 0, "INVALID_AMOUNT"),
                               ("1 AETH", 51, "INVALID_ARGUMENT"), ("1 AETH", -1, "INVALID_ARGUMENT"),
                               ("1 AETH", "2", "INVALID_ARGUMENT")]:
        assert q.quote(amount, slip)["error"]["code"] == code, (amount, slip)
    assert calls == [], "bad input never reaches Osmosis"

    refusing = SwapQuoter(["http://a.test", "http://b.test"], fetch=osmosis(v, calls, 400))
    r = refusing.quote("1 AETH")
    assert r["error"]["code"] == "INVALID_AMOUNT" and "must be positive" in r["error"]["message"]
    assert len(calls) == 1, "a refused amount isn't retried on the next endpoint"

    kit = AetherToolkit(AetherClient("http://127.0.0.1:1", chain_id="aether-devnet"), faucet_url=None)
    assert kit.quote_swap("1 AETH")["error"]["code"] == "SWAP_NOT_CONFIGURED", "off away from the testnet"
    assert AetherToolkit(AetherClient("http://127.0.0.1:1"), faucet_url=None)._quoter is not None, "on by default on the testnet"


def test_through_the_toolkit():
    v = FILE["vectors"][0]
    kit = AetherToolkit(AetherClient("http://127.0.0.1:1"), faucet_url=None, osmosis_rest=["http://osmosis.test"],
                        osmosis_fetch=osmosis(v))
    r = kit.call("aether_quote_swap", json.dumps({"amount": "1 AETH", "slippage_percent": 2}))
    assert r["out"]["uosmo"] == "394472"
    assert r["minOutput"]["uosmo"] == "386582"  # floor(394472 * 0.98)
    assert kit.call("aether_quote_swap", {"amount": "1 AETH", "slippage_percent": 0.5})["minOutput"]["uosmo"] == "392499"
    spec = next(t for t in kit.tool_specs() if t["function"]["name"] == "aether_quote_swap")
    assert spec["function"]["parameters"]["properties"]["slippage_percent"]["type"] == "number"
