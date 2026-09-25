import json
import threading
import unittest
from http.server import BaseHTTPRequestHandler, HTTPServer

from aether_client import DIRECTORY_ADDRESS, IncomingPayment, Key, RatingSummary, find_services, rating_memo


class Reputation(unittest.TestCase):
    def test_only_paying_raters_count_and_trusted_split_out(self):
        payee = Key.random().address
        me, friend, stranger, sock = (Key.random().address for _ in range(4))

        class H(BaseHTTPRequestHandler):
            def do_GET(self):
                body = json.dumps({"x402Version": 1, "name": "Weather", "network": "aether-testnet-1", "payTo": payee,
                                   "price": "20000", "schemes": ["aether-memo"]}).encode()
                self.send_response(200)
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, *a):
                pass
        srv = HTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=srv.serve_forever, daemon=True).start()
        url = f"http://127.0.0.1:{srv.server_port}"

        def p(h, sender, memo, amount=1, code=0):
            return IncomingPayment(f"H{h}", h, code, sender, amount, memo)
        by = {
            DIRECTORY_ADDRESS: [p(10, payee, "x402-service:" + url), p(60, me, rating_memo(url, 5)),
                                p(61, friend, rating_memo(url + "/", 3)), p(62, stranger, rating_memo(url, 1)),
                                p(63, sock, rating_memo(url, 1)), p(64, stranger, rating_memo(url, 2)),
                                p(65, friend, "x402-rate:9:" + url)],
            payee: [p(50, me, "inv", 20_000), p(51, friend, "inv", 20_000), p(52, stranger, "inv", 20_000),
                    p(53, payee, "self", 99), p(54, sock, "inv", 20_000, 5)],
        }

        class Client:
            chain_id = "aether-testnet-1"

            def latest_height(self):
                return 100

            def incoming_payments(self, address, since_height=1, max_results=5000):
                return [x for x in by.get(address, []) if x.height >= since_height]
        try:
            s, = find_services(Client(), allow_private=True, trusted=[me, friend])
            r = s.reputation
            self.assertEqual((r.payers, r.payments, r.volume_uaeth), (3, 3, 60_000))
            self.assertEqual(r.ratings.count, 3)
            self.assertAlmostEqual(r.ratings.average, 10 / 3)
            self.assertEqual(r.trusted_ratings, RatingSummary(2, 4.0))
            self.assertEqual({x.rater: x.score for x in r.raters}[stranger], 2)
            self.assertIsNone(find_services(Client(), allow_private=True, reputation=False)[0].reputation)
        finally:
            srv.shutdown()
        with self.assertRaises(ValueError):
            rating_memo(url, 6)
        self.assertEqual(rating_memo("https://W.example/", 4), "x402-rate:4:https://w.example")
