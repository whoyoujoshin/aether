"""ML-DSA-44 (FIPS 204) account keys, derived exactly as the chain's wallet does:

    seed = SHA-256(BIP-39 seed(mnemonic, passphrase)); key = ML-DSA-44 KeyGen(seed)

so a phrase from `aetherd keys add` or `agentmcp init` imports here.
"""

import hashlib
import os

from dilithium_py.ml_dsa import ML_DSA_44
from mnemonic import Mnemonic

from . import bech32

PREFIX = "aether"
PUBKEY_TYPE_URL = "/aether.crypto.v1.PubKey"
PUBKEY_BYTES = 1312  # raw ML-DSA-44 public key (FIPS 204)
_PUBKEY_PROTO_NAME = b"aether.crypto.v1.PubKey"
_WORDS = Mnemonic("english")


def address_of(public_key: bytes) -> str:
    """The account address of an ML-DSA-44 public key (ADR-028: 32 bytes)."""
    type_hash = hashlib.sha256(_PUBKEY_PROTO_NAME).digest()
    return bech32.encode(PREFIX, hashlib.sha256(type_hash + public_key).digest())


def address_bytes(address: str) -> bytes:
    hrp, data = bech32.decode(address)
    if hrp != PREFIX:
        raise ValueError(f'address "{address}" is not an {PREFIX}1... address')
    return data


def is_address(address: str) -> bool:
    try:
        address_bytes(address)
        return True
    except ValueError:
        return False


class Key:
    """An ML-DSA-44 key. `address` is the account it signs for: its own address, or the account it
    was rotated onto (for_account). Sends, payments and sign-ins go from it. `key_address` is the
    address the key derives on its own."""

    def __init__(self, seed: bytes, account: str = None):
        if len(seed) != 32:
            raise ValueError("ML-DSA-44 seed must be 32 bytes")
        self.seed = bytes(seed)
        self.public_key, self._secret_key = ML_DSA_44.key_derive(self.seed)
        self.key_address = address_of(self.public_key)
        if account is not None:
            address_bytes(account)
        self.address = account if account is not None else self.key_address

    def for_account(self, account: str) -> "Key":
        """This key signing for `account`, the account it was rotated onto (docs/PQ-KEY-ROTATION.md):
        an account keeps its address when its key changes, so the new key sends from the old address.
        The chain accepts it only while it is the key stored on that account; AetherClient.key_for
        checks that first."""
        return Key(self.seed, account)

    @property
    def rotated(self) -> bool:
        """True when this key signs for an account other than its own address."""
        return self.address != self.key_address

    @classmethod
    def generate(cls):
        """A new key and its 24-word recovery phrase. Keep the phrase safe."""
        phrase = _WORDS.generate(strength=256)
        return cls.from_mnemonic(phrase), phrase

    @classmethod
    def from_mnemonic(cls, mnemonic: str, passphrase: str = "") -> "Key":
        normalized = " ".join(mnemonic.split())
        if not _WORDS.check(normalized):
            raise ValueError("invalid recovery phrase (checksum or word list)")
        return cls(hashlib.sha256(Mnemonic.to_seed(normalized, passphrase)).digest())

    @classmethod
    def random(cls) -> "Key":
        return cls(os.urandom(32))

    def sign(self, msg: bytes, deterministic: bool = False) -> bytes:
        """FIPS 204 signature, empty context. Randomized unless deterministic."""
        return ML_DSA_44.sign(self._secret_key, msg, deterministic=deterministic)

    @staticmethod
    def verify(public_key: bytes, msg: bytes, signature: bytes) -> bool:
        try:
            return ML_DSA_44.verify(public_key, msg, signature)
        except Exception:
            return False
