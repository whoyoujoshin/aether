"""Key rotation (docs/PQ-KEY-ROTATION.md).

An account keeps its address while the ML-DSA-44 key that signs for it
changes. These builders match the chain's protobuf and the sha256 signing
bytes in that doc.
"""

import hashlib
from dataclasses import dataclass
from typing import List, Optional, Sequence

from .keys import PUBKEY_BYTES, PUBKEY_TYPE_URL, address_of
from .proto import Writer, first, read_fields

_PKG = "/aether.accountauth.v1."
MSG_ROTATE_KEY_TYPE_URL = _PKG + "MsgRotateKey"
MSG_CANCEL_PENDING_ROTATION_TYPE_URL = _PKG + "MsgCancelPendingRotation"
MSG_SET_RECOVERY_TYPE_URL = _PKG + "MsgSetRecovery"
MSG_RECOVER_KEY_TYPE_URL = _PKG + "MsgRecoverKey"
MSG_GUARDIAN_CANCEL_TYPE_URL = _PKG + "MsgGuardianCancel"
MSG_GUARDIAN_SET_RECOVERY_TYPE_URL = _PKG + "MsgGuardianSetRecovery"
MSG_WATCHTOWER_CANCEL_TYPE_URL = _PKG + "MsgWatchtowerCancel"
ROTATION_QUERY_PATH = "/aether.accountauth.v1.Query/Rotation"


def _digest(text: str) -> bytes:
    return hashlib.sha256(text.encode()).digest()


def _hex_list(keys: Sequence[bytes]) -> str:
    return ",".join(k.hex() for k in keys)


def rotate_key_signing_bytes(chain_id: str, account: str, nonce: int, new_pubkey: bytes) -> bytes:
    """What the new key signs to prove it is held. MsgRotateKey uses the account's
    nonce; MsgRecoverKey uses its recovery nonce."""
    return _digest(f"aether-accountauth-rotate/v1\n{chain_id}\n{account}\n{nonce}\n{new_pubkey.hex()}\n")


def recover_key_signing_bytes(chain_id: str, account: str, nonce: int, new_pubkey: bytes) -> bytes:
    """What each recovery guardian signs, at recovery_nonce, to rotate the account at once."""
    return _digest(f"aether-accountauth-recover/v1\n{chain_id}\n{account}\n{nonce}\n{new_pubkey.hex()}\n")


def guardian_cancel_signing_bytes(chain_id: str, account: str, nonce: int) -> bytes:
    """What each recovery guardian signs, at recovery_nonce, to cancel pending changes."""
    return _digest(f"aether-accountauth-guardian-cancel/v1\n{chain_id}\n{account}\n{nonce}\n")


def guardian_set_recovery_signing_bytes(chain_id: str, account: str, nonce: int, threshold: int,
                                        guardian_pubkeys: Sequence[bytes], watchtower_pubkeys: Sequence[bytes] = ()) -> bytes:
    """What each recovery guardian signs, at recovery_nonce, to replace the recovery set
    at once. Keys are hex in the message's order; an empty list (threshold 0, no keys) removes the set."""
    return _digest(
        f"aether-accountauth-guardian-set-recovery/v1\n{chain_id}\n{account}\n{nonce}\n{threshold}\n"
        f"guardians:{_hex_list(guardian_pubkeys)}\nwatchtowers:{_hex_list(watchtower_pubkeys)}\n")


def watchtower_cancel_signing_bytes(chain_id: str, account: str, nonce: int) -> bytes:
    """What a watchtower signs, at the account's nonce, to cancel a change the current key scheduled."""
    return _digest(f"aether-accountauth-watchtower-cancel/v1\n{chain_id}\n{account}\n{nonce}\n")


def _gsig(pubkey: bytes, signature: bytes) -> bytes:
    return Writer().bytes(1, pubkey).bytes(2, signature).finish()


def msg_rotate_key(account: str, new_pubkey: bytes, new_key_signature: bytes):
    """(type URL, protobuf) for MsgRotateKey."""
    return MSG_ROTATE_KEY_TYPE_URL, Writer().string(1, account).bytes(2, new_pubkey).bytes(3, new_key_signature).finish()


def msg_cancel_pending_rotation(account: str):
    return MSG_CANCEL_PENDING_ROTATION_TYPE_URL, Writer().string(1, account).finish()


def msg_set_recovery(account: str, guardian_pubkeys: Sequence[bytes], threshold: int, watchtower_pubkeys: Sequence[bytes] = ()):
    w = Writer().string(1, account)
    for g in guardian_pubkeys:
        w.bytes(2, g)
    w.uint64(3, threshold)
    for t in watchtower_pubkeys:
        w.bytes(4, t)
    return MSG_SET_RECOVERY_TYPE_URL, w.finish()


def msg_recover_key(submitter: str, account: str, new_pubkey: bytes, new_key_signature: bytes, guardian_signatures: Sequence[tuple]):
    """guardian_signatures: (pubkey, signature) pairs."""
    w = Writer().string(1, submitter).string(2, account).bytes(3, new_pubkey).bytes(4, new_key_signature)
    for pub, sig in guardian_signatures:
        w.message(5, _gsig(pub, sig))
    return MSG_RECOVER_KEY_TYPE_URL, w.finish()


def msg_guardian_cancel(submitter: str, account: str, guardian_signatures: Sequence[tuple]):
    w = Writer().string(1, submitter).string(2, account)
    for pub, sig in guardian_signatures:
        w.message(3, _gsig(pub, sig))
    return MSG_GUARDIAN_CANCEL_TYPE_URL, w.finish()


def msg_guardian_set_recovery(submitter: str, account: str, guardian_pubkeys: Sequence[bytes], threshold: int,
                              watchtower_pubkeys: Sequence[bytes], guardian_signatures: Sequence[tuple]):
    w = Writer().string(1, submitter).string(2, account)
    for g in guardian_pubkeys:
        w.bytes(3, g)
    w.uint64(4, threshold)
    for t in watchtower_pubkeys:
        w.bytes(5, t)
    for pub, sig in guardian_signatures:
        w.message(6, _gsig(pub, sig))
    return MSG_GUARDIAN_SET_RECOVERY_TYPE_URL, w.finish()


def msg_watchtower_cancel(submitter: str, account: str, watchtower_pubkey: bytes, signature: bytes):
    return MSG_WATCHTOWER_CANCEL_TYPE_URL, Writer().string(1, submitter).string(2, account).bytes(3, watchtower_pubkey).bytes(4, signature).finish()


def signs_for(account: str, pubkey: bytes, stored: Optional[bytes] = None) -> bool:
    """The chain's rule (wallet.SignsFor): the key stored on the account signs for it;
    while none is stored, the key its address hashes from does. After a rotation that
    is the new key, not the retired one. ``stored`` empty or None means none is stored.
    A key that isn't ML-DSA-44 is never a signer. This does not look the key up;
    AetherClient.signs_for does, and raises when the lookup fails."""
    if len(pubkey) != PUBKEY_BYTES:
        return False
    if stored:
        return stored == pubkey
    return address_of(pubkey) == account


def rotation_request(account: str) -> bytes:
    return Writer().string(1, account).finish()


def _bytes_of(buf: bytes, field: int) -> List[bytes]:
    return [v for f, _, v in read_fields(buf) if f == field and isinstance(v, bytes)]


def _recovery(buf: Optional[bytes]):
    if not buf:
        return [], 0, []
    return _bytes_of(buf, 1), int(first(buf, 2, 0) or 0), _bytes_of(buf, 3)


@dataclass
class PendingRotation:
    new_pubkey: bytes
    apply_height: int


@dataclass
class PendingRecovery:
    guardians: List[bytes]
    threshold: int
    watchtowers: List[bytes]
    apply_height: int


@dataclass
class Rotation:
    nonce: int
    recovery_nonce: int
    guardians: List[bytes]
    threshold: int
    watchtowers: List[bytes]
    pending_rotation: Optional[PendingRotation]
    pending_recovery: Optional[PendingRecovery]
    current_pubkey: Optional[bytes]
    active: bool
    delay_blocks: int


def decode_rotation(resp: bytes) -> Rotation:
    """Decodes a Query/Rotation response."""
    state = first(resp, 1, b"") or b""
    guardians, threshold, watchtowers = _recovery(first(state, 2))
    pend_key = first(state, 3)
    pend_set = first(state, 4)
    pending_rotation = None
    if pend_key:
        pk = first(pend_key, 1, b"")
        if pk:
            pending_rotation = PendingRotation(pk, int(first(pend_key, 2, 0) or 0))
    pending_recovery = None
    if pend_set:
        g, t, w = _recovery(first(pend_set, 1))
        pending_recovery = PendingRecovery(g, t, w, int(first(pend_set, 2, 0) or 0))
    current = first(resp, 2, b"") or b""
    return Rotation(
        nonce=int(first(state, 1, 0) or 0),
        recovery_nonce=int(first(state, 5, 0) or 0),
        guardians=guardians, threshold=threshold, watchtowers=watchtowers,
        pending_rotation=pending_rotation, pending_recovery=pending_recovery,
        current_pubkey=current or None,
        active=bool(first(resp, 3, 0)),
        delay_blocks=int(first(resp, 4, 0) or 0),
    )


def decode_account_pubkey(resp: bytes) -> Optional[bytes]:
    """The ML-DSA-44 key inside a Query/AccountInfo response, or None if the account
    has no key stored. A key that is present but isn't ML-DSA-44 is an error:
    treating it as "none" would accept the retired key."""
    info = first(resp, 1)
    if not info:
        return None
    any_msg = first(info, 2)
    if not any_msg:
        return None
    type_url = first(any_msg, 1, b"") or b""
    if isinstance(type_url, bytes):
        type_url = type_url.decode()
    value = first(any_msg, 2, b"") or b""
    if not type_url and not value:
        return None
    if type_url != PUBKEY_TYPE_URL:
        raise ValueError(f"account key type {type_url or 'unknown'} is not an ML-DSA-44 key")
    raw = first(value, 1, b"") or b""
    if not raw:
        return None
    if len(raw) != PUBKEY_BYTES:
        raise ValueError(f"stored key is {len(raw)} bytes, not an ML-DSA-44 public key")
    return raw
