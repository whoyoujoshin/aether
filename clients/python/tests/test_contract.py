"""The client calls docs/START.md copy-pastes (docs/API-STABILITY.md): a
rename here breaks every bot that followed the start page."""

import dataclasses
import unittest

from aether_client import AetherClient, Key
from aether_client.client import SendResult, TransactionInfo


class StartPageContract(unittest.TestCase):
    def test_calls_exist_under_their_documented_names(self):
        key, phrase = Key.generate()
        self.assertTrue(key.address.startswith("aether1"))
        self.assertEqual(len(phrase.split()), 24)
        self.assertEqual(Key.from_mnemonic(phrase).address, key.address)

        client = AetherClient("http://127.0.0.1:1", "aether-testnet-1")
        for m in ("balance", "send", "rebroadcast", "wait_for_transaction"):
            self.assertTrue(callable(getattr(client, m)), m)

    def test_result_fields(self):
        self.assertLessEqual({"hash", "status", "signed"}, {f.name for f in dataclasses.fields(SendResult)})
        self.assertLessEqual({"hash", "status", "height"}, {f.name for f in dataclasses.fields(TransactionInfo)})


if __name__ == "__main__":
    unittest.main()
