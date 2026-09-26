"""aether-pull: the Python buyer against the Python seller, over real HTTP, on a
fake chain that applies the x/authz messages broadcast to it."""

import base64
import hashlib
import json
import threading
import time
import unittest
import urllib.error
import urllib.request
from wsgiref.simple_server import WSGIRequestHandler, make_server

from aether_client import (MSG_EXEC_TYPE_URL, MSG_GRANT_TYPE_URL, AetherClient, Key, Paywall, PaymentError, fetch_paid,
                           pull_payment_header, signing_message)
from aether_client.proto import Writer, first, read_fields
from aether_client.rpc import RpcError

CHAIN = "aether-testnet-1"
SEND_AUTH = "/cosmos.bank.v1beta1.SendAuthorization"


class AuthzChain:
    """MsgGrant stores (replaces) an allowance; MsgExec spends under one or fails.
    Every accepted transaction goes straight into a block."""

    def __init__(self):
        self.txs, self.grants, self.balances, self.seq, self.broadcasts = {}, {}, {}, {}, []

    def call(self, method, params=None):
        params = params or {}
        if method == "tx":
            h = base64.b64decode(params["hash"]).hex().upper()
            if h not in self.txs:
                raise RpcError(f"tx: tx ({h}) not found", -32603)
            return self.txs[h]
        if method == "abci_query":
            data = bytes.fromhex(params["data"])
            if params["path"] == "/cosmos.auth.v1beta1.Query/AccountInfo":
                info = Writer().uint64(3, 9).uint64(4, self.seq.get(first(data, 1).decode(), 0)).finish()
                return {"response": {"code": 0, "value": base64.b64encode(Writer().message(1, info).finish()).decode()}}
            if params["path"] == "/cosmos.authz.v1beta1.Query/Grants":
                g = self.grants.get((first(data, 1).decode(), first(data, 2).decode()))
                if not g:
                    return {"response": {"code": 2, "log": "authorization not found: key not found", "value": None}}
                auth = Writer().message(1, Writer().string(1, "uaeth").string(2, str(g["limit"])).finish())
                for a in g["allow"]:
                    auth.string(2, a)
                any_ = Writer().string(1, SEND_AUTH).bytes(2, auth.finish()).finish()
                grant = Writer().message(1, any_).message(2, Writer().uint64(1, g["exp"]).finish()).finish()
                return {"response": {"code": 0, "value": base64.b64encode(Writer().message(1, grant).finish()).decode()}}
            raise AssertionError("unexpected query " + params["path"])
        if method == "broadcast_tx_sync":
            return self.apply(base64.b64decode(params["tx"]))
        raise AssertionError("unexpected " + method)

    def apply(self, tx):
        h = hashlib.sha256(tx).hexdigest().upper()
        if h in self.txs:
            return {"code": 19, "codespace": "sdk", "log": "tx already in mempool", "hash": h}
        body = first(tx, 1)
        code, events = 0, []
        for f, _, m in read_fields(body):
            if f != 1:
                continue
            type_url, v = first(m, 1).decode(), first(m, 2, b"")
            self.broadcasts.append(type_url)
            if type_url == MSG_GRANT_TYPE_URL:
                granter, grantee, g = first(v, 1).decode(), first(v, 2).decode(), first(v, 3)
                auth = first(first(g, 1), 2)
                coin = first(auth, 1)
                self.grants[(granter, grantee)] = {"limit": int(first(coin, 2).decode()), "exp": first(first(g, 2), 1, 0),
                                                   "allow": [x.decode() for ff, _, x in read_fields(auth) if ff == 2]}
                self.seq[granter] = self.seq.get(granter, 0) + 1
            elif type_url == MSG_EXEC_TYPE_URL:
                grantee = first(v, 1).decode()
                send = first(first(v, 2), 2)
                frm, to, amount = first(send, 1).decode(), first(send, 2).decode(), int(first(first(send, 3), 2).decode())
                g = self.grants.get((frm, grantee))
                if not g or g["limit"] < amount or (g["allow"] and to not in g["allow"]):
                    code = 4
                else:
                    g["limit"] -= amount
                    if not g["limit"]:
                        del self.grants[(frm, grantee)]  # like the chain: a used-up allowance is removed
                    self.balances[to] = self.balances.get(to, 0) + amount
                    events.append({"type": "transfer", "attributes": [{"key": "sender", "value": frm}, {"key": "recipient", "value": to},
                                                                      {"key": "amount", "value": f"{amount}uaeth"}]})
                self.seq[grantee] = self.seq.get(grantee, 0) + 1
        self.txs[h] = {"hash": h, "height": "9", "tx": base64.b64encode(tx).decode(),
                       "tx_result": {"code": code, "log": "failed to get grant: authorization not found" if code else "", "events": events}}
        return {"code": 0, "codespace": "", "log": "", "hash": h}

    def client(self):
        c = AetherClient("http://node", CHAIN)
        c.rpc.call = self.call
        return c


class _Quiet(WSGIRequestHandler):
    def log_message(self, *args):
        pass


class PullTest(unittest.TestCase):
    def setUp(self, credit="1 AETH"):
        self.chain = AuthzChain()
        self.client = self.chain.client()
        self.seller, self.collector = Key.random(), Key.random()
        self.pw = Paywall(self.client, self.seller.address, "0.01 AETH", pull_collector_key=self.collector, pull_ledger="", pull_credit=credit)
        self.served = []

        def app(environ, start_response):
            self.served.append(environ.get("aether.payment"))
            start_response("200 OK", [("Content-Type", "text/plain")])
            return [b"ok"]
        self.httpd = make_server("127.0.0.1", 0, self.pw.wsgi(app), handler_class=_Quiet)
        self.url = f"http://127.0.0.1:{self.httpd.server_port}/q"
        threading.Thread(target=self.httpd.serve_forever, daemon=True).start()

    def tearDown(self):
        self.httpd.shutdown()
        self.httpd.server_close()

    def buy(self, buyer, rid, allowance="0.05 AETH"):
        return fetch_paid(self.client, buyer, self.url, max_amount="0.02 AETH", pull_allowance=allowance, prepay="1 AETH", request_id=rid,
                          confirm_timeout=5)

    def collect(self):
        self.pw.collect_all()  # signs, saves, sends
        self.pw.collect_all()  # sees it in a block

    def test_grants_once_then_pays_instantly_and_is_collected_in_batches(self):
        buyer = Key.random()
        r = self.buy(buyer, "r1")
        self.assertEqual((r.status, r.scheme, r.owed_uaeth, r.allowance_uaeth), ("paid", "aether-pull", 10_000, 40_000))
        self.assertTrue(r.grant_tx_hash)
        g = self.chain.grants[(buyer.address, self.collector.address)]
        self.assertEqual((g["limit"], g["allow"]), (50_000, [self.seller.address]), "payable only to the seller")
        self.assertLess(abs(g["exp"] - (time.time() + 7 * 86400)), 60)
        for rid in ("r2", "r3"):
            self.assertEqual(self.buy(buyer, rid).status, "paid")
        self.assertEqual(self.chain.broadcasts, [MSG_GRANT_TYPE_URL], "one transaction: the allowance")
        with self.assertRaises(PaymentError) as e:
            self.buy(buyer, "r3")
        self.assertEqual(e.exception.code, "PAYMENT_ALREADY_REDEEMED")
        self.collect()
        self.assertEqual(self.chain.balances[self.seller.address], 30_000, "collected in one transfer")
        self.assertEqual(self.chain.broadcasts.count(MSG_EXEC_TYPE_URL), 1)
        self.assertEqual(self.pw.pull_ledger.pull_account(buyer.address).owed, 0)
        self.assertEqual(len(self.served), 3)

    def test_revoked_allowance_debt_blocks_until_covered(self):
        buyer = Key.random()
        self.buy(buyer, "a")
        self.buy(buyer, "b")
        del self.chain.grants[(buyer.address, self.collector.address)]  # the buyer revokes
        self.collect()
        self.assertEqual(self.pw.pull_ledger.pull_account(buyer.address).unpaid, 20_000)
        with self.assertRaises(PaymentError) as e:
            self.buy(buyer, "c", "0.02 AETH")
        self.assertEqual(e.exception.code, "INVALID_ARGUMENT")
        self.assertIn("at least 30000", str(e.exception))
        r = self.buy(buyer, "c")
        self.assertEqual((r.status, r.owed_uaeth), ("paid", 30_000), "the old debt is owed again, with this request")
        self.collect()
        self.assertEqual(self.chain.balances[self.seller.address], 30_000, "every served request was paid for")

    def test_prepaid_signatures_dont_pass_as_pull_and_credit_caps_whats_owed(self):
        self.tearDown()
        self.setUp(credit="0.02 AETH")
        buyer = Key.random()
        self.buy(buyer, "1", "0.1 AETH")
        f = dict(network=CHAIN, pay_to=self.seller.address, host=f"127.0.0.1:{self.httpd.server_port}", method="GET", path="/q", body=b"",
                 max_price=10_000, timestamp=int(time.time()), request_id="x")
        forged = json.loads(base64.b64decode(pull_payment_header(buyer, **f)))
        forged["payload"]["signature"] = base64.b64encode(buyer.sign(signing_message(**f))).decode()
        self.assertEqual(self.error(base64.b64encode(json.dumps(forged).encode()).decode()), "invalid_signature")
        self.buy(buyer, "2", "0.1 AETH")
        self.assertEqual(self.error(pull_payment_header(buyer, **{**f, "request_id": "3"})), "settlement_pending")

    def error(self, header):
        try:
            urllib.request.urlopen(urllib.request.Request(self.url, headers={"X-PAYMENT": header}))
        except urllib.error.HTTPError as e:
            self.assertEqual(e.code, 402)
            return json.loads(e.read())["error"]
        self.fail("served")


if __name__ == "__main__":
    unittest.main()
