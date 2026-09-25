"""Receipts: a seller's signed statement about one paid response.

Who paid what for which request, and what came back (status and a hash of
the body). Signed by the payee account's own key, or by a key the payee
delegated receipts to, so anyone can check one against the payee's
address alone. The same format as the Go paywall (package paywall).
"""

import base64
import hashlib
import json
import re
from dataclasses import dataclass
from typing import Optional

from .keys import Key, address_of

RECEIPT_HEADER = "X-PAYMENT-RECEIPT"
_RECEIPT_DOMAIN = "aether-x402-receipt/v1\n"
_DELEGATION_DOMAIN = "aether-x402-receipt-delegation/v1\n"


def _lines(domain: str, fields) -> bytes:
    return (domain + "".join(f"{f}\n" for f in fields)).encode()


def receipt_signing_message(r: dict) -> bytes:
    """The exact bytes a receipt's signer signs."""
    return _lines(_RECEIPT_DOMAIN, [r["network"], r["payTo"], r["payer"], r["scheme"], r["payment"], r["amount"], r["method"],
                                    r["host"], r["path"], r["requestHash"], r["status"], r.get("responseHash") or "", r["at"]])


def delegation_signing_message(pay_to: str, signer: str, expires: int) -> bytes:
    """The exact bytes a payee signs to delegate receipts."""
    return _lines(_DELEGATION_DOMAIN, [pay_to, signer, expires])


def create_receipt_delegation(payee: Key, signer: str, expires: int) -> dict:
    """Lets the key with address `signer` sign receipts for payee's account until `expires` (unix seconds)."""
    return {"payTo": payee.address, "signer": signer, "expires": expires,
            "payToPubKey": base64.b64encode(payee.public_key).decode(),
            "signature": base64.b64encode(payee.sign(delegation_signing_message(payee.address, signer, expires))).decode()}


def sign_receipt(key: Key, fields: dict, delegation: Optional[dict] = None) -> dict:
    """Signs a receipt (sellers). fields: network, payTo, payer, scheme, payment, amount,
    method, host, path, requestHash, status, responseHash (optional), at."""
    r = {"x402Version": 1, **{k: v for k, v in fields.items() if not (k == "responseHash" and not v)},
         "signer": base64.b64encode(key.public_key).decode()}
    if delegation:
        r["delegation"] = delegation
    r["signature"] = base64.b64encode(key.sign(receipt_signing_message(r))).decode()
    return r


def _pub(s) -> Optional[bytes]:
    try:
        b = base64.b64decode(s, validate=True)
    except (ValueError, TypeError):
        return None
    return b if len(b) == 1312 else None


def _sig_ok(pub: bytes, msg: bytes, sig) -> bool:
    try:
        return Key.verify(pub, msg, base64.b64decode(sig, validate=True))
    except (ValueError, TypeError):
        return False


_HEX_HASH = re.compile(r"[0-9a-f]{64}")


def malformed(r: dict) -> Optional[str]:
    """A field with a line break could shift the signed lines after it into other fields."""
    for k in ("network", "payTo", "payer", "scheme", "payment", "amount", "method", "host", "path"):
        v = r.get(k)
        if not isinstance(v, str) or "\n" in v or "\r" in v:
            return "a field contains a line break"
    rh = r.get("responseHash")
    if not isinstance(r.get("requestHash"), str) or not _HEX_HASH.fullmatch(r["requestHash"]) or \
            (rh not in (None, "") and not (isinstance(rh, str) and _HEX_HASH.fullmatch(rh))):
        return "hashes must be hex SHA-256"
    for k in ("status", "at"):
        if not isinstance(r.get(k), int) or isinstance(r.get(k), bool):
            return "status and at must be integers"
    return None


def verify_receipt(r: dict) -> Optional[str]:
    """Why the receipt wasn't signed for its payee (directly or by delegation), or None if it was.
    Says nothing about whether it matches a purchase: see check_receipt."""
    try:
        if r.get("x402Version") != 1:
            return "unsupported receipt version"
        bad = malformed(r)
        if bad:
            return bad
        pub = _pub(r.get("signer"))
        if not pub:
            return "the signer isn't a base64 ML-DSA-44 public key"
        signer = address_of(pub)
        if signer != r["payTo"]:
            d = r.get("delegation")
            if not d:
                return "signed by a key that isn't the payee's, with no delegation"
            if d.get("payTo") != r["payTo"] or d.get("signer") != signer:
                return "the delegation is for another payee or key"
            payee = _pub(d.get("payToPubKey"))
            if not payee or address_of(payee) != r["payTo"]:
                return "the delegation isn't signed by the payee's key"
            if not _sig_ok(payee, delegation_signing_message(d["payTo"], d["signer"], d["expires"]), d.get("signature")):
                return "the delegation's signature is bad"
            if r["at"] > d["expires"]:
                return "signed after its delegation expired"
        if not _sig_ok(pub, receipt_signing_message(r), r.get("signature")):
            return "bad signature"
    except (KeyError, TypeError, AttributeError):
        return "the receipt is malformed"
    return None


def check_receipt(r: dict, *, network, pay_to, payer, scheme, payment, amount: int, method, host, path,
                  request_body: bytes, status: int, response_body: Optional[bytes] = None) -> Optional[str]:
    """verify_receipt, and that it describes exactly this purchase: the problem, or None."""
    bad = verify_receipt(r)
    if bad:
        return bad
    checks = [("network", r["network"], network), ("payTo", r["payTo"], pay_to), ("payer", r["payer"], payer),
              ("scheme", r["scheme"], scheme), ("payment", str(r["payment"]).upper(), payment.upper()),
              ("amount", r["amount"], str(amount)), ("method", r["method"], method), ("host", r["host"], host),
              ("path", r["path"], path), ("requestHash", r["requestHash"], hashlib.sha256(request_body).hexdigest()),
              ("status", str(r["status"]), str(status))]
    for name, got, want in checks:
        if got != want:
            return f'{name} is "{got}", not "{want}"'
    if r.get("responseHash") and response_body is not None and r["responseHash"] != hashlib.sha256(response_body).hexdigest():
        return "responseHash doesn't match the response received"
    return None


def decode_receipt(header: str) -> Optional[dict]:
    try:
        v = json.loads(base64.b64decode(header, validate=True))
        return v if isinstance(v, dict) else None
    except ValueError:
        return None


def encode_receipt(r: dict) -> str:
    return base64.b64encode(json.dumps(r, separators=(",", ":")).encode()).decode()


@dataclass
class ReceiptCheck:
    receipt: Optional[dict]
    verified: bool
    problem: Optional[str] = None
