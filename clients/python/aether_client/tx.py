"""Building and signing a bank send (SIGN_MODE_DIRECT), byte-identical to the
chain's Go wallet (see clients/testdata/vectors.json)."""

import hashlib
from dataclasses import dataclass

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
    msg = Writer().string(1, key.address).string(2, to).message(3, _coin(amount_uaeth)).finish()
    body = Writer().message(1, _any(MSG_SEND_TYPE_URL, msg)).string(2, memo).finish()
    pub = _any(PUBKEY_TYPE_URL, Writer().bytes(1, key.public_key).finish())
    mode = Writer().message(1, Writer().uint64(1, _SIGN_MODE_DIRECT).finish()).finish()
    signer = Writer().message(1, pub).message(2, mode).uint64(3, sequence).finish()
    fee = Writer().uint64(2, gas_limit).finish()  # zero fee: no amount
    auth = Writer().message(1, signer).message(2, fee).finish()
    doc = Writer().bytes(1, body).bytes(2, auth).string(3, chain_id).uint64(4, account_number).finish()
    sig = key.sign(doc, deterministic=deterministic)
    tx = Writer().bytes(1, body).bytes(2, auth).bytes(3, sig).finish()
    return SignedTx(body, auth, doc, sig, tx, hashlib.sha256(tx).hexdigest().upper())


def memo_of(tx_bytes: bytes) -> str:
    body = first(tx_bytes, 1, b"")
    return first(body, 2, b"").decode() if body else ""
