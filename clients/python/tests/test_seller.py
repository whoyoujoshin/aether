"""The Python seller kit, against a fake chain and real HTTP (WSGI) plus a driven ASGI app."""

import asyncio
import base64
import hashlib
import json
import os
import tempfile
import threading
import time
import unittest
import urllib.error
import urllib.request
from wsgiref.simple_server import WSGIRequestHandler, make_server

from aether_client import (WITHDRAW_PATH, AetherClient, FileLedger, Key, Paywall, PaymentError, build_send, fetch_paid,
                           memo_payment_header, prepaid_payment_header, withdraw_prepaid)
from aether_client.rpc import RpcError

CHAIN = "aether-testnet-1"


class FakeChain:
    """A node that knows the transactions tests put in blocks, over the client's JSON-RPC."""

    def __init__(self):
        self.txs, self.broadcasts, self.seq = {}, [], 4
        self.on_broadcast = lambda tx: {"code": 0}

    def call(self, method, params=None):
        params = params or {}
        if method == "tx":
            h = base64.b64decode(params["hash"]).hex().upper()
            if h not in self.txs:
                raise RpcError(f"tx: tx ({h}) not found", -32603)
            return self.txs[h]
        if method == "abci_query":
            from aether_client.proto import Writer
            info = Writer().uint64(3, 9).uint64(4, self.seq).finish()
            return {"response": {"code": 0, "value": base64.b64encode(Writer().message(1, info).finish()).decode()}}
        if method == "broadcast_tx_sync":
            tx = base64.b64decode(params["tx"])
            self.broadcasts.append(tx)
            r = self.on_broadcast(tx)
            return {"code": r.get("code", 0), "codespace": r.get("codespace", ""), "log": r.get("log", ""), "hash": ""}
        raise AssertionError("unexpected " + method)

    def client(self):
        c = AetherClient("http://node", CHAIN)
        c.rpc.call = self.call
        return c

    def pay(self, to, uaeth, memo, code=0, sender=None):
        sender = sender or Key.random()
        s = build_send(sender, chain_id=CHAIN, account_number=1, sequence=0, to=to, amount_uaeth=uaeth, memo=memo)
        self.include(s.tx_bytes, s.hash, code, [(sender.address, to, uaeth)])
        return s.hash

    def include(self, tx_bytes, h, code=0, transfers=()):
        events = [{"type": "transfer", "attributes": [{"key": "sender", "value": a}, {"key": "recipient", "value": b},
                                                      {"key": "amount", "value": f"{n}uaeth"}]} for a, b, n in transfers]
        self.txs[h] = {"hash": h, "height": "7", "tx": base64.b64encode(tx_bytes).decode(),
                       "tx_result": {"code": code, "log": "failed" if code else "", "events": events}}


class _Quiet(WSGIRequestHandler):
    def log_message(self, *args):
        pass


class Seller:
    def __init__(self, payout=False, ledger="", fail_with=None):
        self.chain = FakeChain()
        self.client = self.chain.client()
        self.seller = Key.random()
        self.t = time.time()
        self.pw = Paywall(self.client, self.seller.address, "0.01 AETH", name="Weather", description="forecasts",
                          prepaid_ledger=ledger, min_deposit="0.03 AETH", payout_key=Key.random() if payout else None,
                          now=lambda: self.t)
        self.served = []

        def app(environ, start_response):
            p = environ.get("aether.payment")
            if p:
                self.served.append(p)
            body = environ["wsgi.input"].read(int(environ.get("CONTENT_LENGTH") or 0))
            start_response(f"{fail_with or 200} X", [("Content-Type", "application/json")])
            return [json.dumps({"path": environ["PATH_INFO"], "body": body.decode()}).encode()]

        self.httpd = make_server("127.0.0.1", 0, self.pw.wsgi(app, free=["/health"]), handler_class=_Quiet)
        self.url = f"http://127.0.0.1:{self.httpd.server_port}"
        threading.Thread(target=self.httpd.serve_forever, daemon=True).start()

    def close(self):
        self.httpd.shutdown()
        self.httpd.server_close()

    def request(self, path, headers=None, method="GET", body=None):
        req = urllib.request.Request(self.url + path, data=body, method=method, headers=headers or {})
        try:
            with urllib.request.urlopen(req) as r:
                return r.status, dict(r.headers), r.read()
        except urllib.error.HTTPError as e:
            return e.code, dict(e.headers), e.read()

    def error(self, *a, **k):
        return json.loads(self.request(*a, **k)[2])["error"]

    def signed(self, key, request_id, path="/forecast", body=b"", deposit="", max_price=10_000, host=None):
        return prepaid_payment_header(key, network=CHAIN, pay_to=self.seller.address, host=host or f"127.0.0.1:{self.httpd.server_port}",
                                      method="POST", path=path, body=body, max_price=max_price, timestamp=int(self.t),
                                      request_id=request_id, deposit_tx=deposit)


class SellerTest(unittest.TestCase):
    def setUp(self):
        self.s = None

    def tearDown(self):
        if self.s:
            self.s.close()

    def test_quote_manifest_and_free_paths(self):
        s = self.s = Seller(payout=True)
        status, _, body = s.request("/forecast")
        self.assertEqual(status, 402)
        q = json.loads(body)
        self.assertEqual([a["scheme"] for a in q["accepts"]], ["aether-memo", "aether-prepaid"])
        self.assertTrue(q["accepts"][0]["extra"]["invoice"].startswith("x402-"))
        self.assertEqual(q["accepts"][1]["extra"]["withdrawPath"], WITHDRAW_PATH)
        self.assertEqual(s.request("/health")[0], 200)
        m = json.loads(s.request("/.well-known/x402")[2])
        self.assertEqual(m, {"x402Version": 1, "name": "Weather", "description": "forecasts", "network": CHAIN,
                             "payTo": s.seller.address, "price": "10000", "priceAeth": "0.01",
                             "schemes": ["aether-memo", "aether-prepaid"], "minDeposit": "30000", "withdrawPath": WITHDRAW_PATH})

    def test_memo_payment_served_once(self):
        s = self.s = Seller()
        inv = json.loads(s.request("/forecast")[2])["accepts"][0]["extra"]["invoice"]

        def present(h, invoice=inv, path="/forecast"):
            return s.request(path, {"X-PAYMENT": memo_payment_header(CHAIN, invoice, h)})
        unseen = s.chain.pay(s.seller.address, 10_000, inv)
        del s.chain.txs[unseen]
        status, headers, body = present(unseen)
        self.assertEqual(json.loads(body)["error"], "payment_not_confirmed")
        self.assertEqual(json.loads(body)["accepts"][0]["extra"]["invoice"], inv)
        self.assertEqual(headers.get("Retry-After"), "10")

        h = s.chain.pay(s.seller.address, 10_000, inv)
        status, headers, _ = present(h)
        self.assertEqual(status, 200)
        self.assertEqual(json.loads(base64.b64decode(headers["X-PAYMENT-RESPONSE"]))["transaction"], h)
        self.assertEqual(s.served[0].scheme, "aether-memo")
        self.assertEqual(json.loads(present(h)[2])["error"], "invoice_already_redeemed")
        self.assertEqual(json.loads(present(h, path="/other")[2])["error"], "invoice_for_other_resource")

        inv2 = json.loads(s.request("/forecast")[2])["accepts"][0]["extra"]["invoice"]
        for h2, want in [(s.chain.pay(s.seller.address, 9_999, inv2), "insufficient_payment"),
                         (s.chain.pay(s.seller.address, 10_000, "other"), "memo_mismatch"),
                         (s.chain.pay(s.seller.address, 10_000, inv2, code=5), "payment_failed")]:
            self.assertEqual(json.loads(present(h2, inv2)[2])["error"], want)
        forged = inv2[:-2] + ("AB" if inv2.endswith("AA") else "AA")
        self.assertEqual(json.loads(present(h, forged)[2])["error"], "invalid_invoice")
        s.t += 25 * 3600
        self.assertEqual(json.loads(present(s.chain.pay(s.seller.address, 10_000, inv2), inv2)[2])["error"], "invoice_expired")

    def test_failed_response_frees_the_payment(self):
        s = self.s = Seller(fail_with=503)
        inv = json.loads(s.request("/forecast")[2])["accepts"][0]["extra"]["invoice"]
        h = {"X-PAYMENT": memo_payment_header(CHAIN, inv, s.chain.pay(s.seller.address, 10_000, inv))}
        self.assertEqual(s.request("/forecast", h)[0], 503)
        self.assertEqual(s.request("/forecast", h)[0], 503)

    def test_prepaid(self):
        s = self.s = Seller()
        agent = Key.random()
        body = b'{"q":1}'

        def post(header, b=body):
            return s.request("/forecast", {"X-PAYMENT": header, "Content-Type": "application/json"}, "POST", b)
        self.assertEqual(json.loads(post(s.signed(agent, "r0", body=body))[2])["error"], "insufficient_balance")
        dep = s.chain.pay(s.seller.address, 50_000, "prepaid:" + agent.address)
        status, _, out = post(s.signed(agent, "r1", body=body, deposit=dep))
        self.assertEqual(status, 200)
        self.assertEqual(json.loads(out)["body"], '{"q":1}', "the app still reads the body")
        self.assertEqual(s.served[-1].balance_uaeth, 40_000)
        self.assertEqual(post(s.signed(agent, "r2", body=body, deposit=dep))[0], 200)
        self.assertEqual(s.served[-1].balance_uaeth, 30_000)
        self.assertEqual(json.loads(post(s.signed(agent, "r2", body=body))[2])["error"], "invoice_already_redeemed")
        for header, want in [(s.signed(agent, "r3", body=b'{"q":2}'), "invalid_signature"),
                             (s.signed(agent, "r3", body=body, path="/other"), "invalid_signature"),
                             (s.signed(agent, "r3", body=body, host="evil.example"), "invalid_signature"),
                             (s.signed(agent, "r3", body=body, max_price=9_999), "price_above_signed_max")]:
            self.assertEqual(json.loads(post(header)[2])["error"], want)
        other = Key.random()
        theirs = s.chain.pay(s.seller.address, 50_000, "prepaid:" + other.address)
        self.assertEqual(post(s.signed(agent, "r4", body=body, deposit=theirs))[0], 200)
        self.assertEqual(s.served[-1].balance_uaeth, 20_000, "charged from its own balance, not credited")
        self.assertEqual(post(s.signed(other, "o1", body=body))[0], 200)
        self.assertEqual(s.served[-1].balance_uaeth, 40_000)
        s.t += 600
        stale = s.signed(agent, "r5", body=body)
        s.t -= 600
        self.assertEqual(json.loads(post(stale)[2])["error"], "stale_request")

    def test_withdrawals(self):
        path = os.path.join(tempfile.mkdtemp(), "ledger.json")
        s = self.s = Seller(payout=True, ledger=path)
        agent = Key.random()
        dep = s.chain.pay(s.seller.address, 50_000, "prepaid:" + agent.address)
        self.assertEqual(s.request("/q", {"X-PAYMENT": s.signed(agent, "r1", path="/q", deposit=dep)}, "POST")[0], 200)

        s.chain.on_broadcast = lambda tx: {"code": 5, "codespace": "sdk", "log": "insufficient funds"}
        with self.assertRaises(PaymentError) as e:
            withdraw_prepaid(s.client, agent, s.url, withdrawal_id="w1")
        self.assertEqual(e.exception.code, "WITHDRAWAL_FAILED")
        self.assertEqual(FileLedger(path).balance(agent.address), 40_000)

        def reset(tx):
            raise OSError("connection reset")
        s.chain.on_broadcast = reset
        first = withdraw_prepaid(s.client, agent, s.url + "/any", withdrawal_id="w1")
        self.assertEqual((first.status, first.amount_uaeth, first.balance_uaeth), ("pending", 40_000, 0))
        s.chain.on_broadcast = lambda tx: {"code": 0}
        second = withdraw_prepaid(s.client, agent, s.url, withdrawal_id="w1")
        self.assertEqual(second.tx_hash, first.tx_hash)
        self.assertEqual(s.chain.broadcasts[-1], s.chain.broadcasts[-2], "the same bytes, never a second payout")
        s.chain.include(s.chain.broadcasts[-1], first.tx_hash)
        self.assertEqual(withdraw_prepaid(s.client, agent, s.url, withdrawal_id="w1").status, "confirmed")
        n = len(s.chain.broadcasts)
        self.assertEqual(withdraw_prepaid(s.client, agent, s.url, withdrawal_id="w1").status, "confirmed")
        self.assertEqual(len(s.chain.broadcasts), n)
        with self.assertRaises(PaymentError) as e:
            withdraw_prepaid(s.client, agent, s.url, withdrawal_id="w2")
        self.assertEqual(e.exception.code, "INSUFFICIENT_PREPAID_BALANCE")
        with self.assertRaises(PaymentError) as e:
            withdraw_prepaid(s.client, agent, s.url, amount="0.03 AETH", withdrawal_id="w1")
        self.assertEqual(e.exception.code, "IDEMPOTENCY_CONFLICT")

    def test_python_buyer_pays_python_seller(self):
        s = self.s = Seller()
        buyer = Key.random()
        amount = {"n": 10_000}

        def include(tx):
            s.chain.include(tx, hashlib.sha256(tx).hexdigest().upper(), 0, [(buyer.address, s.seller.address, amount["n"])])
            return {"code": 0}
        s.chain.on_broadcast = include
        r = fetch_paid(s.client, buyer, s.url + "/forecast", max_amount="0.01 AETH", confirm_timeout=5)
        self.assertEqual((r.status, r.response.status), ("paid", 200))
        amount["n"] = 50_000
        r = fetch_paid(s.client, buyer, s.url + "/forecast", max_amount="0.01 AETH", prepay="0.05 AETH", request_id="p1", confirm_timeout=5)
        self.assertEqual(r.balance_uaeth, 40_000)
        r = fetch_paid(s.client, buyer, s.url + "/forecast", max_amount="0.01 AETH", prepay="0.05 AETH", request_id="p2")
        self.assertEqual(r.balance_uaeth, 30_000)
        self.assertEqual([p.scheme for p in s.served], ["aether-memo", "aether-prepaid", "aether-prepaid"])

    def test_manifest_cant_redirect_the_withdrawal(self):
        s = self.s = Seller(payout=True)
        s.pw.manifest = lambda: {"x402Version": 1, "network": CHAIN, "payTo": s.seller.address, "withdrawPath": "@evil.example/w"}
        with self.assertRaises(PaymentError) as e:
            withdraw_prepaid(s.client, Key.random(), s.url)
        self.assertEqual(e.exception.code, "PAYMENT_UNSUPPORTED")


class AsgiTest(unittest.TestCase):
    def test_asgi_serves_prepaid_and_replays_the_body(self):
        chain = FakeChain()
        client = chain.client()
        seller, agent = Key.random(), Key.random()
        pw = Paywall(client, seller.address, "0.01 AETH", prepaid_ledger="")
        seen = {}

        async def app(scope, receive, send):
            msg = await receive()
            seen.update(body=msg["body"], payment=scope.get("aether"))
            await send({"type": "http.response.start", "status": 200, "headers": [(b"content-type", b"text/plain")]})
            await send({"type": "http.response.body", "body": b"ok"})
        mw = pw.asgi(app)
        dep = chain.pay(seller.address, 50_000, "prepaid:" + agent.address)
        body = b'{"q":1}'
        header = prepaid_payment_header(agent, network=CHAIN, pay_to=seller.address, host="svc.example", method="POST",
                                        path="/forecast", body=body, max_price=10_000, timestamp=int(time.time()),
                                        request_id="r1", deposit_tx=dep)

        async def run(h):
            sent, chunks = [], [{"type": "http.request", "body": body[:3], "more_body": True},
                                {"type": "http.request", "body": body[3:], "more_body": False}]

            async def receive():
                return chunks.pop(0) if chunks else {"type": "http.disconnect"}

            async def send(m):
                sent.append(m)
            scope = {"type": "http", "method": "POST", "path": "/forecast", "scheme": "http",
                     "headers": [(b"host", b"svc.example"), (b"x-payment", h.encode())]}
            await mw(scope, receive, send)
            return sent
        sent = asyncio.run(run(header))
        self.assertEqual(sent[0]["status"], 200)
        self.assertIn(b"x-payment-response", [k for k, _ in sent[0]["headers"]])
        self.assertEqual(seen["body"], body)
        self.assertEqual(seen["payment"].payer, agent.address)
        self.assertEqual(seen["payment"].balance_uaeth, 40_000)
        again = asyncio.run(run(header))
        self.assertEqual(again[0]["status"], 402)
        self.assertEqual(json.loads(again[1]["body"])["error"], "invoice_already_redeemed")


if __name__ == "__main__":
    unittest.main()
