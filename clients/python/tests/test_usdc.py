"""USDC: the Python buyer pays a USDC-priced service in USDC, and never mixes assets."""

import base64
import hashlib
import json
import os
import threading
import unittest
from http.server import BaseHTTPRequestHandler, HTTPServer

from aether_client import (AETH, MSG_GRANT_TYPE_URL, AetherClient, Assets, Key, PaymentError, fetch_paid, format_amount,
                           receipt_amount, transfers, usdc)
from aether_client.proto import Writer, first, read_fields
from aether_client.rpc import RpcError
from aether_client.tx import MSG_SEND_TYPE_URL

CHAIN = "aether-testnet-1"
USDC = usdc("channel-3")

with open(os.path.join(os.path.dirname(__file__), "..", "..", "testdata", "vectors.json")) as f:
    V = json.load(f)


class Chain:
    """Every transaction goes straight into a block; records the coins each message moves."""

    def __init__(self):
        self.txs, self.msgs, self.seq = {}, [], 0

    def call(self, method, params=None):
        params = params or {}
        if method == "tx":
            h = base64.b64decode(params["hash"]).hex().upper()
            if h not in self.txs:
                raise RpcError(f"tx: tx ({h}) not found", -32603)
            return self.txs[h]
        if method == "abci_query" and params["path"] == "/cosmos.auth.v1beta1.Query/AccountInfo":
            info = Writer().uint64(3, 9).uint64(4, self.seq).finish()
            return {"response": {"code": 0, "value": base64.b64encode(Writer().message(1, info).finish()).decode()}}
        if method == "broadcast_tx_sync":
            tx = base64.b64decode(params["tx"])
            h = hashlib.sha256(tx).hexdigest().upper()
            for fnum, _, m in read_fields(first(tx, 1)):
                if fnum != 1:
                    continue
                type_url, v = first(m, 1).decode(), first(m, 2, b"")
                # MsgSend: coin at field 3. MsgGrant: grant(3) > authorization(1) > value(2) > spend limit coin(1).
                coin = first(v, 3) if type_url == MSG_SEND_TYPE_URL else first(first(first(first(v, 3), 1), 2), 1)
                self.msgs.append((type_url, first(coin, 1).decode(), int(first(coin, 2).decode())))
            self.seq += 1
            self.txs[h] = {"hash": h, "height": "9", "tx": params["tx"], "tx_result": {"code": 0, "log": "", "events": []}}
            return {"code": 0, "codespace": "", "log": "", "hash": h}
        raise AssertionError(f"unexpected {method} {params.get('path', '')}")

    def client(self, usdc_channel="channel-3"):
        c = AetherClient("http://node", CHAIN, usdc_channel=usdc_channel)
        c.rpc.call = self.call
        return c


def usdc_service(chain, pay_to):
    """Charges 0.05 USDC per request, by memo or pull. Takes any memo proof, and a pull request once any
    allowance is on chain: the buyer is what's tested."""
    grantee = Key.random().address

    class H(BaseHTTPRequestHandler):
        def do_GET(self):
            payment = self.headers.get("X-PAYMENT")
            scheme = json.loads(base64.b64decode(payment))["scheme"] if payment else ""
            granted = any(m[0] == MSG_GRANT_TYPE_URL for m in chain.msgs)
            if scheme == "aether-memo" or (scheme == "aether-pull" and granted):
                self.send_response(200)
                self.end_headers()
                self.wfile.write(b"paid in dollars")
                return

            def offer(s, extra):
                return {"scheme": s, "network": CHAIN, "maxAmountRequired": "50000", "asset": USDC.denom, "payTo": pay_to,
                        "resource": "", "description": "", "maxTimeoutSeconds": 300, "extra": {"symbol": "USDC", "amount": "0.05", **extra}}
            body = json.dumps({"x402Version": 1, "error": "no_grant" if scheme == "aether-pull" else "payment_required",
                               "accepts": [offer("aether-memo", {"invoice": "inv-1"}), offer("aether-pull", {"grantee": grantee, "owed": "0"})]})
            self.send_response(402)
            self.end_headers()
            self.wfile.write(body.encode())

        def log_message(self, *args):
            pass

    srv = HTTPServer(("127.0.0.1", 0), H)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv, f"http://127.0.0.1:{srv.server_port}/q"


class USDCTest(unittest.TestCase):
    def test_denom_and_receipt_amount_match_go(self):
        self.assertEqual(USDC.denom, V["usdc"]["denom"])
        self.assertEqual(receipt_amount(50_000, USDC.denom), V["usdc"]["receiptAmount"])
        self.assertEqual(receipt_amount(50_000), "50000", "AETH receipts read as they always did")
        with self.assertRaises(ValueError):
            usdc("3")

    def test_amounts_name_their_asset(self):
        with self.assertRaisesRegex(ValueError, 'unknown unit "USDC"'):
            Assets().parse("5 USDC")
        with self.assertRaisesRegex(ValueError, "no unit"):
            Assets().parse("5")
        on = Assets("channel-3")
        self.assertEqual(on.parse("2.25 usdc"), (USDC, 2_250_000))
        self.assertEqual(on.parse("5000000uusdc"), (USDC, 5_000_000))
        self.assertEqual(on.parse("1.5 AETH"), (AETH, 1_500_000))
        with self.assertRaisesRegex(ValueError, "decimal places"):
            on.parse("0.0000001 USDC")
        self.assertEqual(format_amount(USDC, 50_000), "0.05 USDC")

    def test_transfers_keep_assets_apart(self):
        events = [{"type": "transfer", "attributes": [{"key": "sender", "value": "A"}, {"key": "recipient", "value": "B"},
                                                      {"key": "amount", "value": "150uaeth,7ibc/ABC"}]}]
        (t,) = transfers(events)
        self.assertEqual((t.amount, t.denom, t.amount_uaeth), (150, "uaeth", 150))
        (u,) = transfers(events, "ibc/ABC")
        self.assertEqual((u.amount, u.denom, u.amount_uaeth), (7, "ibc/ABC", 0))

    def test_send_moves_usdc(self):
        chain = Chain()
        c = chain.client()
        c.send(Key.random(), Key.random().address, "2 USDC")
        c.send(Key.random(), Key.random().address, "0.5 AETH")
        self.assertEqual([(d, a) for _, d, a in chain.msgs], [(USDC.denom, 2_000_000), ("uaeth", 500_000)])

    def test_fetch_paid_pays_usdc_price_in_usdc(self):
        chain = Chain()
        c = chain.client()
        srv, url = usdc_service(chain, Key.random().address)
        try:
            key = Key.random()
            with self.assertRaises(PaymentError) as e:
                fetch_paid(c, key, url, max_amount="1 AETH")
            self.assertEqual(e.exception.code, "ASSET_MISMATCH")
            self.assertIn("0.05 USDC", str(e.exception))
            with self.assertRaises(PaymentError) as e:
                fetch_paid(c, key, url, max_amount="0.04 USDC")
            self.assertEqual(e.exception.code, "PRICE_EXCEEDS_MAX")
            with self.assertRaises(PaymentError) as e:
                fetch_paid(chain.client(None), key, url, max_amount="1 AETH")
            self.assertEqual(e.exception.code, "PAYMENT_UNSUPPORTED")
            self.assertIn(USDC.denom, str(e.exception))
            self.assertEqual(chain.msgs, [], "nothing was paid")

            r = fetch_paid(c, key, url, max_amount="0.10 USDC", confirm_timeout=1)
            self.assertEqual(r.status, "paid")
            self.assertEqual(r.response.body, b"paid in dollars")
            self.assertEqual((r.asset, r.amount, r.amount_uaeth), (USDC, 50_000, None), "a USDC amount never shows up as uaeth")
            self.assertEqual(chain.msgs, [(MSG_SEND_TYPE_URL, USDC.denom, 50_000)])
        finally:
            srv.shutdown()
            srv.server_close()

    def test_pull_allowance_is_a_limit_in_usdc_only(self):
        chain = Chain()
        c = chain.client()
        srv, url = usdc_service(chain, Key.random().address)
        try:
            key = Key.random()
            with self.assertRaises(PaymentError) as e:
                fetch_paid(c, key, url, max_amount="0.10 USDC", pull_allowance="1 AETH")
            self.assertEqual(e.exception.code, "ASSET_MISMATCH")
            self.assertEqual(chain.msgs, [])

            r = fetch_paid(c, key, url, max_amount="0.10 USDC", pull_allowance="1 USDC", confirm_timeout=1)
            self.assertEqual((r.status, r.scheme, r.asset), ("paid", "aether-pull", USDC))
            self.assertTrue(r.grant_tx_hash)
            self.assertEqual(chain.msgs, [(MSG_GRANT_TYPE_URL, USDC.denom, 1_000_000)], "one allowance, in USDC")
            fetch_paid(c, key, url, max_amount="0.10 USDC", pull_allowance="1 USDC")
            self.assertEqual(len(chain.msgs), 1, "no transaction per request")
        finally:
            srv.shutdown()
            srv.server_close()


if __name__ == "__main__":
    unittest.main()
