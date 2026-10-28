"""Every case in clients/testdata/rotation.json, as the Go implementation produces it."""

import base64
import hashlib
import json
import pathlib
import unittest

from aether_client import (PUBKEY_TYPE_URL, AetherClient, Key, build_tx, create_receipt_delegation, decode_account_pubkey,
                           decode_rotation, delegation_signing_message, guardian_cancel_signing_bytes,
                           guardian_set_recovery_signing_bytes, msg_cancel_pending_rotation, msg_guardian_cancel,
                           msg_guardian_set_recovery, msg_recover_key, msg_rotate_key, msg_set_recovery,
                           msg_watchtower_cancel, recover_key_signing_bytes, rotate_key_signing_bytes, rotation_request,
                           sign_receipt, signs_for, verify_receipt, watchtower_cancel_signing_bytes)
from aether_client.proto import Writer
from aether_client.rpc import RpcError

FILE = json.loads((pathlib.Path(__file__).resolve().parents[2] / "testdata" / "rotation.json").read_text())
KEYS = {k["name"]: Key.from_mnemonic(k["mnemonic"]) for k in FILE["keys"]}
OWNER, NEXT, G1, G2, WT = KEYS["owner"], KEYS["next"], KEYS["guardian1"], KEYS["guardian2"], KEYS["watchtower"]
CHAIN, ACCOUNT = FILE["chainId"], OWNER.address


def _sign(key, msg):
    return key.sign(msg, deterministic=True)


class RotationVectors(unittest.TestCase):
    def test_mnemonics_derive_the_go_keys(self):
        for k in FILE["keys"]:
            key = KEYS[k["name"]]
            self.assertEqual(key.seed.hex(), k["seed"], k["name"])
            self.assertEqual(key.public_key.hex(), k["pubKey"], k["name"])
            self.assertEqual(key.address, k["address"], k["name"])

    def test_signing_bytes_and_signatures_match_go(self):
        produced = {
            "rotate (new key's proof, nonce 3)": rotate_key_signing_bytes(CHAIN, ACCOUNT, 3, NEXT.public_key),
            "recover (new key's proof, recovery nonce 1)": rotate_key_signing_bytes(CHAIN, ACCOUNT, 1, NEXT.public_key),
            "recover (guardian 1, recovery nonce 1)": recover_key_signing_bytes(CHAIN, ACCOUNT, 1, NEXT.public_key),
            "recover (guardian 2, recovery nonce 1)": recover_key_signing_bytes(CHAIN, ACCOUNT, 1, NEXT.public_key),
            "guardian cancel (guardian 1, recovery nonce 1)": guardian_cancel_signing_bytes(CHAIN, ACCOUNT, 1),
            "watchtower cancel (nonce 3)": watchtower_cancel_signing_bytes(CHAIN, ACCOUNT, 3),
            "guardian set recovery (guardian 1, recovery nonce 1)": guardian_set_recovery_signing_bytes(
                CHAIN, ACCOUNT, 1, 1, [G1.public_key, G2.public_key], [WT.public_key]),
            "guardian set recovery, removing the set (guardian 1, recovery nonce 1)": guardian_set_recovery_signing_bytes(
                CHAIN, ACCOUNT, 1, 0, [], []),
        }
        self.assertEqual(len(FILE["signingBytes"]), len(produced), "every signing-bytes case is covered")
        for s in FILE["signingBytes"]:
            bz = produced[s["name"]]
            self.assertEqual(hashlib.sha256(s["text"].encode()).hexdigest(), s["sha256"], s["name"] + " text")
            self.assertEqual(bz.hex(), s["sha256"], s["name"])
            key = KEYS[s["signedBy"]]
            self.assertEqual(_sign(key, bz).hex(), s["signature"], s["name"] + " signature")
            self.assertTrue(Key.verify(key.public_key, bz, bytes.fromhex(s["signature"])))

    def test_every_message_encodes_like_go(self):
        rotate_sig = _sign(NEXT, rotate_key_signing_bytes(CHAIN, ACCOUNT, 3, NEXT.public_key))
        recover_pop = _sign(NEXT, rotate_key_signing_bytes(CHAIN, ACCOUNT, 1, NEXT.public_key))
        recover_bz = recover_key_signing_bytes(CHAIN, ACCOUNT, 1, NEXT.public_key)
        g1_recover, g2_recover = _sign(G1, recover_bz), _sign(G2, recover_bz)
        g1_cancel = _sign(G1, guardian_cancel_signing_bytes(CHAIN, ACCOUNT, 1))
        g1_set = _sign(G1, guardian_set_recovery_signing_bytes(CHAIN, ACCOUNT, 1, 1, [G1.public_key, G2.public_key], [WT.public_key]))
        watch_sig = _sign(WT, watchtower_cancel_signing_bytes(CHAIN, ACCOUNT, 3))
        built = {
            "MsgRotateKey": msg_rotate_key(ACCOUNT, NEXT.public_key, rotate_sig),
            "MsgCancelPendingRotation": msg_cancel_pending_rotation(ACCOUNT),
            "MsgSetRecovery": msg_set_recovery(ACCOUNT, [G1.public_key, G2.public_key], 2),
            "MsgSetRecovery (with a watchtower)": msg_set_recovery(ACCOUNT, [G1.public_key, G2.public_key], 2, [WT.public_key]),
            "MsgSetRecovery (remove)": msg_set_recovery(ACCOUNT, [], 0),
            "MsgRecoverKey": msg_recover_key(G1.address, ACCOUNT, NEXT.public_key, recover_pop,
                                             [(G1.public_key, g1_recover), (G2.public_key, g2_recover)]),
            "MsgGuardianCancel": msg_guardian_cancel(G1.address, ACCOUNT, [(G1.public_key, g1_cancel)]),
            "MsgGuardianSetRecovery": msg_guardian_set_recovery(
                G1.address, ACCOUNT, [G1.public_key, G2.public_key], 1, [WT.public_key], [(G1.public_key, g1_set)]),
            "MsgWatchtowerCancel": msg_watchtower_cancel(G2.address, ACCOUNT, WT.public_key, watch_sig),
        }
        self.assertEqual(len(FILE["messages"]), len(built), "every message is covered")
        for m in FILE["messages"]:
            type_url, value = built[m["name"]]
            self.assertEqual(type_url, m["typeUrl"], m["name"])
            self.assertEqual(value.hex(), m["bytes"], m["name"])

        tx = FILE["rotateKeyTx"]
        signed = build_tx(OWNER, [built["MsgRotateKey"]], chain_id=tx["chainId"], account_number=tx["accountNumber"],
                          sequence=tx["sequence"], gas_limit=tx["gasLimit"], deterministic=True)
        self.assertEqual(signed.body_bytes.hex(), tx["bodyBytes"], "body")
        self.assertEqual(signed.auth_info_bytes.hex(), tx["authInfoBytes"], "auth info")
        self.assertEqual(signed.sign_doc.hex(), tx["signDoc"], "sign doc")
        self.assertEqual(signed.signature.hex(), tx["signature"], "signature")
        self.assertEqual(signed.tx_bytes.hex(), tx["txBytes"], "tx bytes")
        self.assertEqual(signed.hash, tx["txHash"], "tx hash")

    def test_rotation_query_decodes_the_chains_answer(self):
        q = FILE["rotationQuery"]
        self.assertEqual(base64.b64encode(rotation_request(ACCOUNT)).decode(), q["request"])
        got = decode_rotation(base64.b64decode(q["response"]))

        def hexes(xs):
            return [x.hex() for x in xs]
        pending = None
        if got.pending_recovery:
            pending = {"guardians": hexes(got.pending_recovery.guardians), "threshold": got.pending_recovery.threshold,
                       "watchtowers": hexes(got.pending_recovery.watchtowers), "applyHeight": got.pending_recovery.apply_height}
        self.assertEqual({
            "nonce": got.nonce, "recoveryNonce": got.recovery_nonce, "recoveryThreshold": got.threshold,
            "recoveryGuardians": hexes(got.guardians), "recoveryWatchtowers": hexes(got.watchtowers),
            "pendingRotation": {"newPubkey": got.pending_rotation.new_pubkey.hex(), "applyHeight": got.pending_rotation.apply_height},
            "pendingRecovery": pending,
            "currentPubkey": got.current_pubkey.hex() if got.current_pubkey else "",
            "active": got.active, "delayBlocks": got.delay_blocks,
        }, q["expected"])

    def test_signs_for_follows_the_stored_key_and_fails_closed(self):
        for c in FILE["signsFor"]:
            stored = bytes.fromhex(c["stored"]) if c["stored"] else None
            self.assertEqual(signs_for(c["account"], bytes.fromhex(c["pubKey"]), stored), c["signsFor"], c["name"])
        retired = next(c for c in FILE["signsFor"] if c["name"].startswith("rotated: the retired"))
        accepted = next(c for c in FILE["signsFor"] if c["name"] == "rotated: the new key")
        self.assertFalse(retired["signsFor"])
        self.assertTrue(accepted["signsFor"])

        def info(pub=None):
            body = Writer()
            if pub is not None:
                body.message(2, Writer().string(1, PUBKEY_TYPE_URL).bytes(2, Writer().bytes(1, pub).finish()).finish())
            return Writer().message(1, body.uint64(3, 7).uint64(4, 1).finish()).finish()

        class Node:
            def __init__(self, value=None, fail=False):
                self.value, self.fail = value, fail

            def call(self, method, params=None):
                if self.fail:
                    raise RpcError(f"{method}: node down", -32603)
                return {"response": {"code": 0, "value": base64.b64encode(self.value).decode() if self.value else None}}

        def client(value=None, fail=False):
            c = AetherClient("http://node", CHAIN)
            c.rpc.call = Node(value, fail).call
            return c

        rotated = client(info(NEXT.public_key))
        self.assertEqual(rotated.account_pub_key(ACCOUNT), NEXT.public_key)
        self.assertTrue(rotated.signs_for(ACCOUNT, NEXT.public_key), "the new key")
        self.assertFalse(rotated.signs_for(ACCOUNT, OWNER.public_key), "the retired key")
        self.assertIsNone(decode_account_pubkey(info()))
        self.assertTrue(client(info()).signs_for(ACCOUNT, OWNER.public_key), "nothing stored: its own key")
        with self.assertRaises(RpcError):
            client(fail=True).signs_for(ACCOUNT, OWNER.public_key)
        self.assertFalse(client(fail=True).signs_for(ACCOUNT, OWNER.public_key[:32]), "not a key, so it isn't looked up")

        view = client(base64.b64decode(FILE["rotationQuery"]["response"])).rotation(ACCOUNT)
        self.assertTrue(view.active)
        self.assertEqual(view.delay_blocks, 15300)
        self.assertEqual(view.current_pubkey.hex(), FILE["rotationQuery"]["expected"]["currentPubkey"])

    def test_receipts_accept_the_new_key_and_refuse_the_retired_one(self):
        digest = hashlib.sha256(b"").hexdigest()
        fields = {"network": CHAIN, "payTo": ACCOUNT, "payer": G1.address, "scheme": "aether-memo", "payment": "AB",
                  "amount": "1", "method": "GET", "host": "h", "path": "/p", "requestHash": digest, "status": 200, "at": 10}
        by_old, by_new = sign_receipt(OWNER, fields), sign_receipt(NEXT, fields)
        self.assertIsNone(verify_receipt(by_old), "hash-only still accepts the address's key")
        self.assertIsNotNone(verify_receipt(by_new), "hash-only refuses the new key")

        class Stored:
            def account_pub_key(self, a):
                if a == ACCOUNT:
                    return NEXT.public_key
                return None

        rotated = Stored()
        self.assertIsNotNone(verify_receipt(by_old, rotated), "the retired key")
        self.assertIsNone(verify_receipt(by_new, rotated), "the new key")

        class Down:
            def account_pub_key(self, a):
                raise RpcError("node down")

        self.assertIn("reading the payee's key", verify_receipt(by_new, Down()))

        expires = 100

        def delegation(signer):
            return {"payTo": ACCOUNT, "signer": G2.address, "expires": expires,
                    "payToPubKey": base64.b64encode(signer.public_key).decode(),
                    "signature": base64.b64encode(signer.sign(delegation_signing_message(ACCOUNT, G2.address, expires))).decode()}

        self.assertIsNone(verify_receipt(sign_receipt(G2, fields, delegation(NEXT)), rotated), "a delegation the new key signed")
        self.assertIn("payee", verify_receipt(sign_receipt(G2, fields, delegation(OWNER)), rotated) or "", "a delegation the retired key signed")
        self.assertIsNone(verify_receipt(sign_receipt(G2, fields, create_receipt_delegation(OWNER, G2.address, expires))),
                          "hash-only, the address's own key")
