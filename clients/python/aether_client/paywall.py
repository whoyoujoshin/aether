"""Buying from paid APIs (x402 wire format; package paywall on the Go side).

aether-memo: pay the quoted invoice (memo = invoice), wait for the block,
repeat the request with proof. aether-prepaid (bots): deposit once, then sign
each request with the account's key; the seller deducts the price instantly.
Response bodies come from the seller: untrusted.
"""

import base64
import hashlib
import json
import os
import time
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from typing import Optional

from .amount import parse_amount, parse_uaeth
from .client import AetherClient
from .keys import Key
from .receipt import RECEIPT_HEADER, ReceiptCheck, check_receipt, decode_receipt

SCHEME_MEMO = "aether-memo"
SCHEME_PREPAID = "aether-prepaid"
DEPOSIT_MEMO_PREFIX = "prepaid:"
_SIGNING_DOMAIN = "aether-prepaid-request/v1\n"


def signing_message(*, network, pay_to, host, method, path, body: bytes, max_price: int, timestamp: int,
                    request_id: str, deposit_tx: str = "") -> bytes:
    """The exact bytes an aether-prepaid request signs (matches the Go seller)."""
    lines = [network, pay_to, host, method, path, hashlib.sha256(body).hexdigest(), str(max_price), str(timestamp), request_id, deposit_tx]
    return (_SIGNING_DOMAIN + "".join(line + "\n" for line in lines)).encode()


def _header(v) -> str:
    return base64.b64encode(json.dumps(v, separators=(",", ":")).encode()).decode()


def memo_payment_header(network: str, invoice: str, tx_hash: str) -> str:
    return _header({"x402Version": 1, "scheme": SCHEME_MEMO, "network": network, "payload": {"invoice": invoice, "txHash": tx_hash}})


def prepaid_payment_header(key: Key, **fields) -> str:
    sig = key.sign(signing_message(**fields))
    payload = {"account": key.address, "pubKey": base64.b64encode(key.public_key).decode(), "timestamp": fields["timestamp"],
               "requestId": fields["request_id"], "maxPrice": str(fields["max_price"]), "signature": base64.b64encode(sig).decode()}
    if fields.get("deposit_tx"):
        payload["depositTx"] = fields["deposit_tx"]
    return _header({"x402Version": 1, "scheme": SCHEME_PREPAID, "network": fields["network"], "payload": payload})


class PaymentError(Exception):
    def __init__(self, code: str, message: str, tx_hash: Optional[str] = None):
        super().__init__(message)
        self.code, self.tx_hash = code, tx_hash


@dataclass
class HttpResponse:
    status: int
    headers: dict
    body: bytes
    truncated: bool = False


@dataclass
class FetchPaidResult:
    status: str  # ok | paid | payment_pending
    response: Optional[HttpResponse] = None
    scheme: Optional[str] = None
    tx_hash: Optional[str] = None  # memo payment, or a prepaid deposit
    invoice: Optional[str] = None
    amount_uaeth: Optional[int] = None
    balance_uaeth: Optional[int] = None  # prepaid: left with the seller
    receipt: Optional[ReceiptCheck] = None  # the seller's signed receipt, if it gives them, and whether it matches


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None  # a redirect would send the payment proof elsewhere


_opener = urllib.request.build_opener(_NoRedirect)


_MAX_RESPONSE = 10 << 20


def _http(method: str, url: str, body: bytes, headers: dict, timeout: float = 30) -> HttpResponse:
    req = urllib.request.Request(url, data=body or None, method=method, headers=headers)
    try:
        with _opener.open(req, timeout=timeout) as r:
            status, hdrs, data = r.status, dict(r.headers), r.read(_MAX_RESPONSE + 1)
    except urllib.error.HTTPError as e:
        status, hdrs, data = e.code, dict(e.headers), e.read(_MAX_RESPONSE + 1)
    truncated = len(data) > _MAX_RESPONSE
    return HttpResponse(status, hdrs, data[:_MAX_RESPONSE], truncated)


def _get_header(headers: dict, name: str) -> Optional[str]:
    for k, v in headers.items():
        if k.lower() == name.lower():
            return v
    return None


def _with_receipt(r: "FetchPaidResult", **want) -> "FetchPaidResult":
    """Checks a paid response's receipt against the purchase."""
    header = r.response and _get_header(r.response.headers, RECEIPT_HEADER)
    if not header:
        return r
    receipt = decode_receipt(header)
    if receipt is None:
        r.receipt = ReceiptCheck(None, False, "the receipt is unreadable")
        return r
    body = None if r.response.truncated else r.response.body
    problem = check_receipt(receipt, status=r.response.status, response_body=body, **want)
    r.receipt = ReceiptCheck(receipt, problem is None, problem)
    return r


def _signing_host(url: str) -> str:
    # urllib sends the URL's host[:port] as written; the seller verifies against it.
    return urllib.parse.urlsplit(url).netloc


def fetch_paid(client: AetherClient, key: Key, url: str, *, max_amount: str, prepay: Optional[str] = None,
               method: str = "GET", body: bytes = b"", headers: Optional[dict] = None, request_id: Optional[str] = None,
               confirm_timeout: float = 150) -> FetchPaidResult:
    """Requests url; if it answers 402 with an Aether payment option, pays and
    returns the response. If this returns payment_pending (or raises with a
    tx_hash), the payment was made: finish with present_payment, don't pay again."""
    maximum = parse_amount(max_amount)
    method = method.upper()
    hdrs = dict(headers or {})
    if body and "Content-Type" not in hdrs:
        hdrs["Content-Type"] = "application/json"

    def send(payment: Optional[str] = None) -> HttpResponse:
        h = dict(hdrs)
        if payment:
            h["X-PAYMENT"] = payment
        return _http(method, url, body, h)

    first = send()
    if first.status != 402:
        return FetchPaidResult("ok", first)
    quote = json.loads(first.body)

    def pick(scheme):
        for a in quote.get("accepts", []):
            if a.get("scheme") == scheme and a.get("network") == client.chain_id and a.get("asset") == "uaeth":
                return a
        return None

    prepaid = pick(SCHEME_PREPAID)
    if prepay and prepaid:
        return _fetch_prepaid(client, key, url, method, body, prepaid, maximum, parse_amount(prepay), send, request_id, confirm_timeout)

    memo = pick(SCHEME_MEMO)
    if not memo or not memo.get("extra", {}).get("invoice"):
        raise PaymentError("PAYMENT_UNSUPPORTED", f"the server doesn't accept {SCHEME_MEMO} payments on {client.chain_id}")
    price = parse_uaeth(memo["maxAmountRequired"])
    if price > maximum:
        raise PaymentError("PRICE_EXCEEDS_MAX", f"the server asks {price} uaeth; max_amount is {maximum}. Nothing was paid")
    invoice = memo["extra"]["invoice"]
    sent = client.send(key, memo["payTo"], f"{price}uaeth", memo=invoice)
    if sent.status == "failed":
        raise PaymentError("TX_REJECTED", f"the payment was rejected: {sent.log}", sent.hash)
    conf = client.wait_for_transaction(sent.hash, timeout=confirm_timeout)
    if conf.status == "failed":
        raise PaymentError("TX_FAILED", f"the payment failed on chain: {conf.log}", sent.hash)
    base = dict(scheme=SCHEME_MEMO, tx_hash=sent.hash, invoice=invoice, amount_uaeth=price)
    if conf.status == "pending":
        return FetchPaidResult("payment_pending", **base)
    r = present_payment(client, url, invoice, sent.hash, send=send)
    parts = urllib.parse.urlsplit(url)
    return _with_receipt(FetchPaidResult(r.status, r.response, **base), network=client.chain_id, pay_to=memo["payTo"],
                         payer=key.address, scheme=SCHEME_MEMO, payment=sent.hash, amount=price, method=method,
                         host=_signing_host(url), path=urllib.parse.unquote(parts.path or "/"), request_body=body)


def present_payment(client: AetherClient, url: str, invoice: str, tx_hash: str, send=None) -> FetchPaidResult:
    """Repeats a request with proof of an aether-memo payment (e.g. after payment_pending)."""
    send = send or (lambda p: _http("GET", url, b"", {"X-PAYMENT": p}))
    proof = memo_payment_header(client.chain_id, invoice, tx_hash)
    for attempt in range(5):
        resp = send(proof)
        if resp.status != 402:
            return FetchPaidResult("paid", resp)
        pr = json.loads(resp.body)
        if pr.get("error") == "payment_not_confirmed" and attempt < 4:
            time.sleep(3)
            continue
        code = "PAYMENT_ALREADY_REDEEMED" if pr.get("error") == "invoice_already_redeemed" else "PAYMENT_REJECTED"
        raise PaymentError(code, f"the server refused the payment ({pr.get('error')}): {pr.get('message', '')}", tx_hash)


def _fetch_prepaid(client, key, url, method, body, req, maximum, prepay, send, request_id, confirm_timeout):
    price = parse_uaeth(req["maxAmountRequired"])
    if price > maximum:
        raise PaymentError("PRICE_EXCEEDS_MAX", f"the server asks {price} uaeth per request; max_amount is {maximum}. Nothing was paid")
    min_dep = req.get("extra", {}).get("minDeposit")
    if min_dep and prepay < parse_uaeth(min_dep):
        raise PaymentError("PAYMENT_UNSUPPORTED", f"the minimum deposit is {min_dep} uaeth")
    parts = urllib.parse.urlsplit(url)
    request_id = request_id or os.urandom(16).hex()

    def attempt(deposit_tx=""):
        return send(prepaid_payment_header(
            key, network=client.chain_id, pay_to=req["payTo"], host=_signing_host(url), method=method,
            path=urllib.parse.unquote(parts.path or "/"), body=body, max_price=price, timestamp=int(time.time()),
            request_id=request_id, deposit_tx=deposit_tx))

    def done(resp, deposit_tx=None):
        balance = None
        s = resp.headers.get("X-Payment-Response") or resp.headers.get("X-PAYMENT-RESPONSE")
        if s:
            balance = int(json.loads(base64.b64decode(s)).get("balance") or 0)
        return _with_receipt(FetchPaidResult("paid", resp, SCHEME_PREPAID, deposit_tx, None, price, balance),
                             network=client.chain_id, pay_to=req["payTo"], payer=key.address, scheme=SCHEME_PREPAID,
                             payment=request_id, amount=price, method=method, host=_signing_host(url),
                             path=urllib.parse.unquote(parts.path or "/"), request_body=body)

    resp = attempt()
    if resp.status != 402:
        return done(resp)
    pr = json.loads(resp.body)
    if pr.get("error") != "insufficient_balance":
        code = "PAYMENT_ALREADY_REDEEMED" if pr.get("error") == "invoice_already_redeemed" else "PAYMENT_REJECTED"
        raise PaymentError(code, f"{pr.get('error')}: {pr.get('message', '')}")
    dep = client.send(key, req["payTo"], f"{prepay}uaeth", memo=DEPOSIT_MEMO_PREFIX + key.address)
    if dep.status == "failed":
        raise PaymentError("TX_REJECTED", f"the deposit was rejected: {dep.log}", dep.hash)
    conf = client.wait_for_transaction(dep.hash, timeout=confirm_timeout)
    if conf.status == "failed":
        raise PaymentError("TX_FAILED", f"the deposit failed on chain: {conf.log}", dep.hash)
    if conf.status == "pending":
        return FetchPaidResult("payment_pending", scheme=SCHEME_PREPAID, tx_hash=dep.hash, amount_uaeth=prepay)
    for i in range(5):
        resp = attempt(dep.hash)
        if resp.status != 402:
            return done(resp, dep.hash)
        pr = json.loads(resp.body)
        if pr.get("error") == "payment_not_confirmed" and i < 4:
            time.sleep(3)
            continue
        raise PaymentError("PAYMENT_REJECTED", f"{pr.get('error')}: {pr.get('message', '')}", dep.hash)
