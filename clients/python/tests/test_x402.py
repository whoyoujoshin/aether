"""The x402 exact scheme signs what the Go facilitator accepts: the same bytes as the Go wallet's send
(vectors.json), which x402/exact_test.go runs through the facilitator. Mirrors clients/ts/test/x402.test.ts."""

import base64
import json
import os
import unittest
from types import SimpleNamespace

from aether_client import AetherClient, Key, build_send
from aether_client.proto import first
from aether_client.x402 import ExactAetherScheme, x402_network

with open(os.path.join(os.path.dirname(__file__), "..", "..", "testdata", "vectors.json")) as f:
    V = json.load(f)

KEY = Key.from_mnemonic(V["key"]["mnemonic"])
PAY_TO = V["txs"][0]["to"]
REQ = {"scheme": "exact", "network": "cosmos:aether-testnet-1", "asset": "uaeth", "amount": "1500000",
       "payTo": PAY_TO, "maxTimeoutSeconds": 60, "extra": {}}


def client(account=(7, 3), usdc_path=None):
    c = AetherClient("http://127.0.0.1:1", "aether-testnet-1", usdc_path=usdc_path,
                     usdc_base_denom="uusdc" if usdc_path else None)
    c.account_info = lambda address: account
    return c


class ExactAetherSchemeTest(unittest.TestCase):
    def test_pays_exactly_the_requirement(self):
        scheme = ExactAetherScheme(client(), KEY)
        self.assertEqual(scheme.scheme, "exact")
        self.assertEqual(x402_network("aether-testnet-1"), "cosmos:aether-testnet-1")
        tx = base64.b64decode(scheme.create_payment_payload(REQ)["transaction"])
        want = build_send(KEY, chain_id="aether-testnet-1", account_number=7, sequence=3, to=PAY_TO, amount_uaeth=1500000)
        self.assertEqual(first(tx, 1), want.body_bytes, "one send of the amount to payTo")
        self.assertEqual(first(tx, 2), want.auth_info_bytes, "the account's key, its next sequence, default gas")
        self.assertTrue(Key.verify(KEY.public_key, want.sign_doc, first(tx, 3)), "signed for account 7 on aether-testnet-1")

    def test_takes_the_sdks_requirements_model(self):
        # x402's PaymentRequirements: snake_case attributes, pay_to for payTo.
        model = SimpleNamespace(scheme="exact", network="cosmos:aether-testnet-1", asset="uaeth", amount="1500000",
                                pay_to=PAY_TO, max_timeout_seconds=60, extra={})
        a = base64.b64decode(ExactAetherScheme(client(), KEY).create_payment_payload(model)["transaction"])
        b = base64.b64decode(ExactAetherScheme(client(), KEY).create_payment_payload(REQ)["transaction"])
        self.assertEqual(first(a, 1), first(b, 1))
        self.assertEqual(first(a, 2), first(b, 2))

    def test_refuses_what_it_cant_or_shouldnt_pay(self):
        scheme = ExactAetherScheme(client(), KEY, max_amount={"uaeth": 1_000_000})
        bad = {
            "another scheme": {**REQ, "scheme": "upto"},
            "another network": {**REQ, "network": "eip155:8453"},
            "another chain": {**REQ, "network": "cosmos:osmo-test-5"},
            "zero": {**REQ, "amount": "0"},
            "decimal": {**REQ, "amount": "1.5"},
            "leading zero": {**REQ, "amount": "0500"},
            "bad asset": {**REQ, "asset": "$$"},
            "bad payTo": {**REQ, "payTo": "0x0000000000000000000000000000000000000000"},
            "over the client's cap": {**REQ, "amount": "1000001"},
        }
        for name, r in bad.items():
            with self.subTest(name), self.assertRaises(ValueError):
                scheme.create_payment_payload(r)
        with self.assertRaisesRegex(ValueError, "spend limit"):
            scheme.create_payment_payload({**REQ, "amount": "500"}, context=SimpleNamespace(max_amount_per_payment="499"))
        scheme.create_payment_payload({**REQ, "amount": "500"}, context=SimpleNamespace(max_amount_per_payment="500"))
        with self.assertRaisesRegex(ValueError, "fund it first"):
            ExactAetherScheme(client(account=None), KEY).create_payment_payload(REQ)

    def test_names_usdc_for_spend_caps(self):
        c = client(usdc_path="transfer/channel-2")
        usdc = c.assets.by_symbol("USDC")
        scheme = ExactAetherScheme(c, KEY)
        self.assertEqual(scheme.find_default_asset(usdc.denom, "cosmos:aether-testnet-1"),
                         {"asset": usdc.denom, "decimals": 6, "symbol": "USDC"})
        self.assertIsNone(scheme.find_default_asset("uaeth", "cosmos:aether-testnet-1"))
        self.assertIsNone(scheme.find_default_asset(usdc.denom, "cosmos:osmo-test-5"))


if __name__ == "__main__":
    unittest.main()
