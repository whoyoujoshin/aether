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
    def __init__(self, seed: bytes):
        if len(seed) != 32:
            raise ValueError("ML-DSA-44 seed must be 32 bytes")
        self.seed = bytes(seed)
        self.public_key, self._secret_key = ML_DSA_44.key_derive(self.seed)
        self.address = address_of(self.public_key)

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
