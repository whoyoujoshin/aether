"""Taking back unspent prepaid balance.

The seller pays it back on chain to the signing account itself; each
withdrawal_id pays out at most once, so retry with the same one.
"""

import json
import os
import re
import time
import urllib.parse
from dataclasses import dataclass
from typing import Optional

from .amount import DENOM, Asset, parse_uaeth
from .client import AetherClient
from .directory import MANIFEST_PATH
from .keys import Key
from .paywall import PaymentError, _http, prepaid_payment_header

_CODES = {
    "withdrawals_unavailable": "WITHDRAWALS_UNAVAILABLE",
    "insufficient_balance": "INSUFFICIENT_PREPAID_BALANCE",
    "below_minimum_withdrawal": "INSUFFICIENT_PREPAID_BALANCE",
    "withdrawal_id_reused": "IDEMPOTENCY_CONFLICT",
    "payout_failed": "WITHDRAWAL_FAILED",  # nothing paid; the balance is intact
    "payout_unavailable": "WITHDRAWAL_FAILED",
}


@dataclass
class WithdrawResult:
    status: str  # pending (sent, not in a block yet) | confirmed | reserved (not sent yet: call again, same id)
    withdrawal_id: str
    amount_uaeth: int  # amount, when the asset is AETH; 0 otherwise
    tx_hash: Optional[str] = None
    balance_uaeth: Optional[int] = None  # balance, when the asset is AETH
    message: Optional[str] = None
    asset: Optional[Asset] = None  # the service's asset; None if the client doesn't know it (denom still says which)
    denom: str = DENOM
    amount: int = 0  # paid back, in denom's base units
    balance: Optional[int] = None  # left with the seller, in denom's base units


def withdraw_prepaid(client: AetherClient, key: Key, service: str, amount: str = "all",
                     withdrawal_id: Optional[str] = None) -> WithdrawResult:
    """Withdraws unspent prepaid balance from the service at `service` (any URL on it).
    amount is "all" or carries its unit, in the asset the service holds it in ("0.5 AETH", "2 USDC")."""
    u = urllib.parse.urlsplit(service)
    if u.scheme not in ("http", "https") or not u.netloc:
        raise PaymentError("INVALID_ARGUMENT", f"service {service} must be an http(s) URL")
    origin = f"{u.scheme}://{u.netloc}"
    wanted = None if not amount or amount.strip().lower() == "all" else client.assets.parse(amount)
    want = str(wanted[1]) if wanted else "all"
    withdrawal_id = withdrawal_id or os.urandom(16).hex()

    m = _http("GET", origin + MANIFEST_PATH, b"", {})
    if m.status != 200:
        raise PaymentError("PAYMENT_UNSUPPORTED", f"{origin}{MANIFEST_PATH} answered HTTP {m.status}: not an Aether paid service")
    manifest = json.loads(m.body)
    if manifest.get("network") != client.chain_id:
        raise PaymentError("PAYMENT_UNSUPPORTED", f"the service is on network {manifest.get('network')}, not {client.chain_id}")
    path = manifest.get("withdrawPath")
    if not path:
        raise PaymentError("WITHDRAWALS_UNAVAILABLE", "this service doesn't offer withdrawals: its operator holds the balance")
    denom = manifest.get("asset") or DENOM
    asset = client.assets.by_denom(denom)
    if wanted and wanted[0].denom != denom:
        raise PaymentError("ASSET_MISMATCH", f"this service holds balances in {asset.symbol if asset else denom}; amount is in {wanted[0].symbol}")
    # A path, never something that would change the host when appended ("@evil.example/").
    if not isinstance(path, str) or not path.startswith("/") or path.startswith("//"):
        raise PaymentError("PAYMENT_UNSUPPORTED", "the manifest's withdrawPath is not a path on this service")

    body = json.dumps({"amount": want}, separators=(",", ":")).encode()
    header = prepaid_payment_header(key, network=client.chain_id, pay_to=manifest["payTo"], host=u.netloc, method="POST",
                                    path=path, body=body, max_price=0, timestamp=int(time.time()), request_id=withdrawal_id)
    resp = _http("POST", origin + path, body, {"Content-Type": "application/json", "X-PAYMENT": header})
    try:
        r = json.loads(resp.body)
    except ValueError:
        raise PaymentError("HTTP_ERROR", f"the service answered HTTP {resp.status} without a withdrawal result")
    if r.get("error"):
        raise PaymentError(_CODES.get(r["error"], "WITHDRAWAL_REJECTED"),
                           f"the service refused the withdrawal ({r['error']}): {r.get('message', '')}")
    bal = r.get("balance")
    paid = parse_uaeth(r["amount"]) if r.get("amount") else 0
    balance = int(bal) if isinstance(bal, str) and re.fullmatch(r"[0-9]+", bal) else None
    aeth = denom == DENOM
    return WithdrawResult(r.get("status", "pending"), withdrawal_id, paid if aeth else 0, r.get("txHash"), balance if aeth else None,
                          r.get("message"), asset, denom, paid, balance)
