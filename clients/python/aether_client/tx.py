"""Building and signing a bank send (SIGN_MODE_DIRECT), byte-identical to the
chain's Go wallet (see clients/testdata/vectors.json)."""

import hashlib
from dataclasses import dataclass
from typing import List, Tuple

from .amount import DENOM
from .keys import PUBKEY_TYPE_URL, Key, address_bytes
from .proto import Writer, first

MSG_SEND_TYPE_URL = "/cosmos.bank.v1beta1.MsgSend"
DEFAULT_GAS_LIMIT = 400_000  # ML-DSA signatures need more than the SDK's 200k default
_SIGN_MODE_DIRECT = 1


@dataclass
class SignedTx:
    body_bytes: bytes
    auth_info_bytes: bytes
    sign_doc: bytes
    signature: bytes
    tx_bytes: bytes
    hash: str  # uppercase hex SHA-256 of tx_bytes: what the chain indexes it by


def _coin(amount: int) -> bytes:
    return Writer().string(1, DENOM).string(2, str(amount)).finish()


def _any(type_url: str, value: bytes) -> bytes:
    return Writer().string(1, type_url).bytes(2, value).finish()


def build_send(key: Key, *, chain_id: str, account_number: int, sequence: int, to: str, amount_uaeth: int,
               memo: str = "", gas_limit: int = DEFAULT_GAS_LIMIT, deterministic: bool = False) -> SignedTx:
    address_bytes(to)  # validates the recipient
    if amount_uaeth <= 0:
        raise ValueError("amount must be positive")
    return build_tx(key, [_msg_send(key.address, to, amount_uaeth)], chain_id=chain_id, account_number=account_number,
                    sequence=sequence, memo=memo, gas_limit=gas_limit, deterministic=deterministic)


def _msg_send(frm: str, to: str, amount_uaeth: int) -> Tuple[str, bytes]:
    return MSG_SEND_TYPE_URL, Writer().string(1, frm).string(2, to).message(3, _coin(amount_uaeth)).finish()


def build_tx(key: Key, msgs: List[Tuple[str, bytes]], *, chain_id: str, account_number: int, sequence: int,
             memo: str = "", gas_limit: int = DEFAULT_GAS_LIMIT, deterministic: bool = False) -> SignedTx:
    """Signs a transaction carrying msgs, (type URL, encoded message) pairs, which the chain executes all-or-nothing."""
    w = Writer()
    for type_url, value in msgs:
        w.message(1, _any(type_url, value))
    body = w.string(2, memo).finish()
    pub = _any(PUBKEY_TYPE_URL, Writer().bytes(1, key.public_key).finish())
    mode = Writer().message(1, Writer().uint64(1, _SIGN_MODE_DIRECT).finish()).finish()
    signer = Writer().message(1, pub).message(2, mode).uint64(3, sequence).finish()
    fee = Writer().uint64(2, gas_limit).finish()  # zero fee: no amount
    auth = Writer().message(1, signer).message(2, fee).finish()
    doc = Writer().bytes(1, body).bytes(2, auth).string(3, chain_id).uint64(4, account_number).finish()
    sig = key.sign(doc, deterministic=deterministic)
    tx = Writer().bytes(1, body).bytes(2, auth).bytes(3, sig).finish()
    return SignedTx(body, auth, doc, sig, tx, hashlib.sha256(tx).hexdigest().upper())


MSG_GRANT_TYPE_URL = "/cosmos.authz.v1beta1.MsgGrant"
MSG_EXEC_TYPE_URL = "/cosmos.authz.v1beta1.MsgExec"
SEND_AUTHORIZATION_TYPE_URL = "/cosmos.bank.v1beta1.SendAuthorization"


def grant_send_msg(granter: str, grantee: str, limit_uaeth: int, allow_list: List[str], expiration: int) -> Tuple[str, bytes]:
    """An x/authz grant letting grantee send up to limit_uaeth from granter, only to allow_list if
    it's non-empty, until expiration (Unix seconds). Granting again replaces the grant and its limit."""
    address_bytes(granter)
    address_bytes(grantee)
    if granter == grantee:
        raise ValueError("granter and grantee must be different accounts")
    if limit_uaeth <= 0:
        raise ValueError("a send grant needs a positive spend limit")
    auth = Writer().message(1, _coin(limit_uaeth))
    for a in allow_list:
        address_bytes(a)
        auth.string(2, a)
    timestamp = Writer().uint64(1, int(expiration)).finish()
    grant = Writer().message(1, _any(SEND_AUTHORIZATION_TYPE_URL, auth.finish())).message(2, timestamp).finish()
    return MSG_GRANT_TYPE_URL, Writer().string(1, granter).string(2, grantee).message(3, grant).finish()


def exec_send_msg(grantee: str, granter: str, to: str, amount_uaeth: int) -> Tuple[str, bytes]:
    """grantee sends amount_uaeth from granter to `to`, under a send grant granter gave it."""
    address_bytes(to)
    if grantee == granter:
        raise ValueError("granter and grantee must be different accounts")
    type_url, send = _msg_send(granter, to, amount_uaeth)
    return MSG_EXEC_TYPE_URL, Writer().string(1, grantee).message(2, _any(type_url, send)).finish()


def memo_of(tx_bytes: bytes) -> str:
    body = first(tx_bytes, 1, b"")
    return first(body, 2, b"").decode() if body else ""
