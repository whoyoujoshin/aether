"""Selling: charge AETH per HTTP request from a Python service.

Compatible with the Go paywall (package paywall) and every Aether buyer --
agentmcp, these clients, or a person paying an invoice by hand::

    pw = Paywall(client, pay_to="aether1...", price="0.01 AETH", prepaid_ledger="ledger.json")
    app = pw.wsgi(app, free=["/health"])     # Flask, Django, any WSGI app
    app = pw.asgi(app, free=["/health"])     # FastAPI, Starlette, any ASGI app

aether-memo: an unpaid request gets 402 with a one-time invoice; the buyer
pays it on chain (memo = invoice) and repeats the request with proof, which
is checked on chain and served once. aether-prepaid (bots): deposit once,
then sign each request; the price is deducted instantly. With payout_key,
buyers can withdraw what they haven't spent. aether-pull (bots): the buyer
grants pull_collector_key's account a capped, expiring allowance on chain,
payable only to pay_to, and signs each request; call start_collecting() to
collect what's owed in batches.

Who paid is in environ["aether.payment"] (WSGI) or scope["aether"] (ASGI).
"""

import asyncio
import base64
import hashlib
import hmac
import io
import json
import os
import re
import struct
import threading
import time
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Callable, Dict, Iterable, Optional, Union

from .amount import format_aeth, parse_amount, parse_uaeth
from .client import AetherClient
from .directory import MANIFEST_PATH
from .keys import Key, address_of, is_address
from .ledger import FileLedger, LedgerError, _parse_time
from .paywall import DEPOSIT_MEMO_PREFIX, SCHEME_MEMO, SCHEME_PREPAID, SCHEME_PULL, pull_signing_message, signing_message
from .receipt import RECEIPT_HEADER, encode_receipt, malformed, sign_receipt, verify_receipt
from .rpc import RpcError
from .tx import build_send, build_tx, exec_send_msg

WITHDRAW_PATH = "/.well-known/x402/withdraw"

_INVOICE_PREFIX = "x402-"
_SIGNED_WINDOW = 300
_REQUEST_ID_RETENTION = 24 * 3600
_MAX_SIGNED_BODY = 10 << 20
_SEQUENCE_SPENT_GRACE = 120
_MAX_RECEIPT_BODY = 4 << 20
_HASH = re.compile(r"[0-9A-F]{64}")

_MEMO_INSTRUCTIONS = (
    "Send maxAmountRequired uaeth to payTo with memo set to exactly this invoice, wait until the transaction is in a block, "
    "then repeat this request with header X-PAYMENT: base64 of the JSON "
    '{"x402Version":1,"scheme":"aether-memo","network":"<network>","payload":{"invoice":"<invoice>","txHash":"<hash>"}}. '
    "Each invoice pays for one response, and must be presented by expiresAt.")
_PREPAID_INSTRUCTIONS = (
    "For many requests: deposit at least minDeposit uaeth to payTo with memo depositMemo "
    '("prepaid:" + the address to credit). Then sign each request with that account\'s ML-DSA key and send X-PAYMENT: base64 of '
    '{"x402Version":1,"scheme":"aether-prepaid","network":"<network>","payload":{account,pubKey,timestamp,requestId,maxPrice,depositTx?,signature}}'
    " -- see package paywall's SigningMessage. The first request after a deposit names it in depositTx. "
    'Each requestId is charged once. Unspent balance stays with the seller; if withdrawPath is set, POST a request signed the same way there (body {"amount":"all"}) to get it back.')


_PULL_INSTRUCTIONS = (
    "For many requests, paying only for what you use: grant grantee a send allowance on chain "
    "(x/authz MsgGrant with a SendAuthorization: spend_limit, allow_list [payTo], and an expiration), then sign each request "
    "with your account's ML-DSA key and send X-PAYMENT: base64 of "
    '{"x402Version":1,"scheme":"aether-pull","network":"<network>","payload":{account,pubKey,timestamp,requestId,maxPrice,signature}}'
    " -- see package paywall's PullSigningMessage. Each requestId is charged once, and what you owe is collected from your account "
    "in batches under the allowance, up to credit at a time. Revoke the allowance to stop.")
COLLECTION_MEMO_PREFIX = "x402-pull:"
_GRANT_CACHE = 15


@dataclass
class Payment:
    """Who paid for a request."""
    payer: str
    scheme: str
    tx_hash: Optional[str] = None  # aether-memo
    balance_uaeth: Optional[int] = None  # aether-prepaid: left with this seller
    owed_uaeth: Optional[int] = None  # aether-pull: owed, not yet collected


@dataclass
class SellerRequest:
    method: str
    host: str
    path: str  # decoded, no query
    headers: Dict[str, str]  # lower-case names
    https: bool = False

    def header(self, name: str) -> Optional[str]:
        return self.headers.get(name.lower())


@dataclass
class Respond:
    status: int
    headers: Dict[str, str]
    body: bytes


@dataclass
class Serve:
    payment: Payment
    headers: Dict[str, str]
    finish: Callable[[int], None] = field(repr=False)  # call with the response status
    # With receipts on: the X-PAYMENT-RECEIPT value for (status, response body or None if too big/streamed).
    receipt: Optional[Callable[[int, Optional[bytes]], str]] = field(default=None, repr=False)


def _json(status: int, v, headers: Optional[dict] = None) -> Respond:
    return Respond(status, {"Content-Type": "application/json", **(headers or {})},
                   (json.dumps(v, separators=(",", ":"), ensure_ascii=False) + "\n").encode())


def _decode_header(s: str):
    try:
        v = json.loads(base64.b64decode(s, validate=True))
        return v if isinstance(v, dict) else None
    except Exception:
        return None


def _encode_header(v) -> str:
    return base64.b64encode(json.dumps(v, separators=(",", ":")).encode()).decode()


def _resource_key(method: str, path: str) -> bytes:
    return hashlib.sha256(f"{method} {path}".encode()).digest()[:16]


def _rfc3339(ts: float) -> str:
    return datetime.fromtimestamp(ts, timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


class KeyPayout:
    """Pays withdrawals from a key's account: signs first (so the payout is
    saved before it's sent), then (re)submits the same bytes."""

    def __init__(self, client: AetherClient, key: Key):
        self.client, self.key = client, key
        self._next = 0
        self._lock = threading.Lock()

    def sign(self, to: str, amount_uaeth: int, memo: str):
        with self._lock:
            info = self.client.account_info(self.key.address)
            if info is None:
                raise RuntimeError(f"payout account {self.key.address} doesn't exist on chain yet: fund it")
            account_number, seq = info
            seq = max(seq, self._next)  # an earlier payout may still be in the mempool
            s = build_send(self.key, chain_id=self.client.chain_id, account_number=account_number, sequence=seq, to=to,
                           amount_uaeth=amount_uaeth, memo=memo)
            self._next = seq + 1
            return s.tx_bytes, seq, s.hash

    def sign_exec(self, frm: str, to: str, amount_uaeth: int, memo: str):
        """Signs, as the grantee, a transfer of amount_uaeth from frm to `to` under frm's allowance."""
        with self._lock:
            info = self.client.account_info(self.key.address)
            if info is None:
                raise RuntimeError(f"collector account {self.key.address} doesn't exist on chain yet: the first allowance granted to it creates it")
            account_number, seq = info
            seq = max(seq, self._next)
            s = build_tx(self.key, [exec_send_msg(self.key.address, frm, to, amount_uaeth)], chain_id=self.client.chain_id,
                         account_number=account_number, sequence=seq, memo=memo)
            self._next = seq + 1
            return s.tx_bytes, seq, s.hash

    def submit(self, tx_bytes: bytes, sequence: int):
        """(status, log): pending | confirmed | failed (can never land) | sequence_spent."""
        h = hashlib.sha256(tx_bytes).hexdigest().upper()

        def known():
            t = self.client.get_transaction(h)
            return None if t.status == "pending" else (t.status, t.log)
        k = known()
        if k:
            return k
        try:
            r = self.client.rpc.broadcast_sync(tx_bytes)
        except RpcError as e:
            if "already exists in cache" in str(e).lower():
                return "pending", ""
            raise
        code, codespace = r.get("code", 0), r.get("codespace", "")
        if code == 0 or (codespace == "sdk" and code == 19):
            return "pending", ""
        k = known()  # rejected -- unless it has landed since
        if k:
            return k
        with self._lock:
            if sequence < self._next:
                self._next = 0
        return ("sequence_spent" if codespace == "sdk" and code == 32 else "failed"), r.get("log", "")


class Paywall:
    def __init__(self, client: AetherClient, pay_to: str, price: str, *, name: str = "", description: str = "",
                 mime_type: str = "", invoice_ttl: int = 86400, secret: Optional[bytes] = None,
                 prepaid_ledger: Union[None, str, FileLedger] = None, min_deposit: Optional[str] = None,
                 payout_key: Optional[Key] = None, receipt_key: Optional[Key] = None,
                 receipt_delegation: Optional[dict] = None, pull_collector_key: Optional[Key] = None,
                 pull_ledger: Union[None, str, FileLedger] = None, pull_credit: Optional[str] = None,
                 pull_collect_every: float = 60, now: Callable[[], float] = time.time):
        """price and min_deposit carry their unit ("0.01 AETH"). prepaid_ledger (a
        FileLedger or a path) offers aether-prepaid: it holds customers'
        balances, so back it up. payout_key pays back unspent balances on
        request: keep only a small float in that account. receipt_key signs a
        receipt for every paid response: pay_to's own key, or one pay_to
        delegated receipts to (receipt_delegation, from
        create_receipt_delegation or `paywall delegate-receipts`).
        pull_collector_key offers aether-pull: buyers grant its account an
        allowance, and start_collecting() collects what they owe (it needs no
        funds: the first allowance granted to it creates its account).
        pull_ledger (default: the prepaid ledger) records what's owed;
        pull_credit ("1 AETH", default 100 requests) is the most a buyer may
        owe before it's collected -- your loss if one revokes just before."""
        if not is_address(pay_to):
            raise ValueError(f'invalid pay_to address "{pay_to}"')
        self.client, self.pay_to = client, pay_to
        self.price = parse_amount(price)
        self.network = client.chain_id
        self.name, self.description, self.mime_type = name, description, mime_type
        self.ttl = invoice_ttl
        self.secret = secret or os.urandom(32)
        self.now = now
        self.ledger = FileLedger(prepaid_ledger) if isinstance(prepaid_ledger, str) else prepaid_ledger
        self.min_deposit = max(parse_amount(min_deposit) if min_deposit else self.price, self.price)
        self.payout = KeyPayout(client, payout_key) if payout_key and self.ledger is not None else None
        self._redeemed: Dict[str, float] = {}
        self._redeemed_lock = threading.Lock()
        self._last_prune = 0.0
        self._withdraw_lock = threading.Lock()
        self.pull_ledger = None
        self.collector = None
        if pull_collector_key:
            ledger = pull_ledger if pull_ledger is not None else self.ledger
            if ledger is None:
                raise ValueError("pull needs a ledger (pull_ledger, or prepaid_ledger)")
            if isinstance(ledger, str):
                ledger = self.ledger if ledger == prepaid_ledger and self.ledger is not None else FileLedger(ledger)
            self.pull_ledger = ledger
            self.collector = KeyPayout(client, pull_collector_key)
            self.credit = parse_amount(pull_credit) if pull_credit else self.price * 100
            if self.credit < self.price:
                raise ValueError("pull credit must be at least the price")
            self.collect_every = pull_collect_every
            self._grants: Dict[str, tuple] = {}
            self._grants_lock = threading.Lock()
            self._collect_lock = threading.Lock()
            self._wake = threading.Event()
            self._stop: Optional[threading.Event] = None
        self.receipt_key, self.receipt_delegation = receipt_key, receipt_delegation
        if receipt_key:
            probe = sign_receipt(receipt_key, {"network": self.network, "payTo": pay_to, "payer": "", "scheme": "", "payment": "",
                                               "amount": "", "method": "", "host": "", "path": "",
                                               "requestHash": hashlib.sha256(b"").hexdigest(), "status": 0, "at": int(self.now())},
                                 receipt_delegation)
            bad = verify_receipt(probe)
            if bad:
                raise ValueError(f"receipts wouldn't verify: {bad}")

    def _receipt_for(self, req: SellerRequest, body: bytes, scheme: str, payer: str, payment: str):
        if not self.receipt_key:
            return None

        def make(status: int, response_body: Optional[bytes]) -> str:
            fields = {
                "network": self.network, "payTo": self.pay_to, "payer": payer, "scheme": scheme, "payment": payment,
                "amount": str(self.price), "method": req.method, "host": req.host, "path": req.path,
                "requestHash": hashlib.sha256(body).hexdigest(), "status": status,
                "responseHash": hashlib.sha256(response_body).hexdigest() if response_body is not None else None,
                "at": int(self.now())}
            if malformed(fields):
                return ""  # e.g. a path with a line break: no receipt rather than an ambiguous one
            return encode_receipt(sign_receipt(self.receipt_key, fields, self.receipt_delegation))
        return make

    def _schemes(self):
        return [SCHEME_MEMO] + ([SCHEME_PREPAID] if self.ledger is not None else []) + ([SCHEME_PULL] if self.pull_ledger is not None else [])

    def manifest(self) -> dict:
        """The service's self-description, served (free) at MANIFEST_PATH."""
        m = {"x402Version": 1, "name": self.name, "description": self.description, "network": self.network,
             "payTo": self.pay_to, "price": str(self.price), "priceAeth": format_aeth(self.price), "schemes": self._schemes()}
        if self.ledger is not None:
            m["minDeposit"] = str(self.min_deposit)
        if self.payout:
            m["withdrawPath"] = WITHDRAW_PATH
        return m

    def needs_body(self, req: SellerRequest) -> bool:
        """Whether handle/withdraw needs the request body (to check a signature)."""
        if req.path == WITHDRAW_PATH:
            return True
        if self.receipt_key and req.header("x-payment"):
            return True
        pay = _decode_header(req.header("x-payment") or "")
        return bool(pay) and pay.get("scheme") in (SCHEME_PREPAID, SCHEME_PULL)

    # --- aether-memo ---

    def handle(self, req: SellerRequest, body: bytes = b"") -> Union[Respond, Serve]:
        """Decides a request to a paid route: Serve it (then call finish with
        the response status) or Respond. Blocking: looks payments up on chain."""
        header = req.header("x-payment")
        if not header:
            return self.payment_required(req, "payment_required", f"this resource costs {format_aeth(self.price)} AETH per request")
        pay = _decode_header(header)
        if pay is None:
            return self.payment_required(req, "invalid_payment", "X-PAYMENT must be base64-encoded JSON")
        if pay.get("network") == self.network and pay.get("scheme") == SCHEME_PREPAID and self.ledger is not None:
            return self._serve_prepaid(req, pay.get("payload"), body)
        if pay.get("network") == self.network and pay.get("scheme") == SCHEME_PULL and self.pull_ledger is not None:
            return self._serve_pull(req, pay.get("payload"), body)
        if pay.get("scheme") != SCHEME_MEMO or pay.get("network") != self.network:
            return self.payment_required(req, "unsupported_scheme",
                                         f'this server accepts {" or ".join(self._schemes())} on network "{self.network}"')
        p = pay.get("payload")
        if not isinstance(p, dict) or not isinstance(p.get("invoice"), str) or not isinstance(p.get("txHash"), str):
            return self.payment_required(req, "invalid_payment", "payload must be {invoice, txHash}")
        invoice, tx_hash = p["invoice"], p["txHash"].strip().upper()
        inv = self._parse_invoice(invoice)
        if inv is None:
            return self.payment_required(req, "invalid_invoice", "invoice was not issued by this server")
        expiry, price, resource = inv
        if not hmac.compare_digest(resource, _resource_key(req.method, req.path)):
            return self.payment_required(req, "invoice_for_other_resource", "this invoice was issued for a different request")
        if self.now() > expiry:
            return self.payment_required(req, "invoice_expired", "this invoice can no longer be redeemed")
        if not _HASH.fullmatch(tx_hash):
            return self.payment_required(req, "invalid_payment", "txHash must be a transaction hash")
        try:
            t = self.client.get_transaction(tx_hash)
        except Exception as e:
            print(f"paywall: looking up {tx_hash}: {e}")
            return Respond(503, {"Retry-After": "10", "Content-Type": "text/plain"},
                           b"could not reach the chain to verify payment; retry with the same X-PAYMENT\n")
        if t.status == "pending":
            return self.payment_required(req, "payment_not_confirmed",
                                         f"transaction {tx_hash} is not in a block yet; retry shortly with the same X-PAYMENT",
                                         invoice=invoice, headers={"Retry-After": "10"})
        if t.status == "failed":
            return self.payment_required(req, "payment_failed", f"transaction {tx_hash} failed on chain")
        if t.memo != invoice:
            return self.payment_required(req, "memo_mismatch", "the transaction's memo must be exactly the invoice")
        paid, payer = self._received(t.transfers)
        if paid < price:
            return self.payment_required(req, "insufficient_payment",
                                         f"paid {paid} uaeth to {self.pay_to}; the invoice is for {price} uaeth")
        if not self._redeem(invoice, expiry):
            return self.payment_required(req, "invoice_already_redeemed", "this invoice has already been used for a response")
        settlement = _encode_header({"success": True, "transaction": tx_hash, "network": self.network, "payer": payer})

        def finish(status: int):
            if status >= 500:  # the server failed, not the buyer: the payment may be used again
                with self._redeemed_lock:
                    self._redeemed.pop(invoice, None)
        return Serve(Payment(payer, SCHEME_MEMO, tx_hash=tx_hash), {"X-PAYMENT-RESPONSE": settlement}, finish,
                     self._receipt_for(req, body, SCHEME_MEMO, payer, tx_hash))

    def _received(self, transfers):
        paid, payer = 0, ""
        for t in transfers:
            if t.recipient == self.pay_to:
                paid += t.amount_uaeth
                payer = payer or t.sender
        return paid, payer

    def _redeem(self, invoice: str, until: float) -> bool:
        with self._redeemed_lock:
            now = self.now()
            if now - self._last_prune > 60:
                for k, t in list(self._redeemed.items()):
                    if now > t:
                        del self._redeemed[k]
                self._last_prune = now
            if invoice in self._redeemed:
                return False
            self._redeemed[invoice] = until
            return True

    # Invoices are HMAC-signed, so issuing one stores nothing (the Go paywall's format).

    def _new_invoice(self, expiry: int, resource: bytes) -> str:
        b = struct.pack(">BQQ", 1, expiry, self.price) + os.urandom(12) + resource
        mac = hmac.new(self.secret, b, hashlib.sha256).digest()[:16]
        return _INVOICE_PREFIX + base64.urlsafe_b64encode(b + mac).decode().rstrip("=")

    def _parse_invoice(self, s: str):
        if not s.startswith(_INVOICE_PREFIX):
            return None
        raw = s[len(_INVOICE_PREFIX):]
        if not re.fullmatch(r"[A-Za-z0-9_-]+", raw):
            return None
        try:
            b = base64.urlsafe_b64decode(raw + "=" * (-len(raw) % 4))
        except ValueError:
            return None
        if len(b) != 61 or b[0] != 1:
            return None
        if not hmac.compare_digest(b[45:], hmac.new(self.secret, b[:45], hashlib.sha256).digest()[:16]):
            return None
        _, expiry, price = struct.unpack(">BQQ", b[:17])
        return expiry, price, b[29:45]

    def payment_required(self, req: SellerRequest, code: str, message: str, invoice: str = "", account: str = "",
                         headers: Optional[dict] = None) -> Respond:
        """A 402, offering invoice if set (a payment that may still confirm) or a fresh one."""
        expiry = int(self.now() + self.ttl)
        if invoice:
            inv = self._parse_invoice(invoice)
            if inv:
                expiry = inv[0]
        else:
            invoice = self._new_invoice(expiry, _resource_key(req.method, req.path))
        memo = {
            "scheme": SCHEME_MEMO, "network": self.network, "maxAmountRequired": str(self.price), "asset": "uaeth",
            "payTo": self.pay_to, "resource": f"{'https' if req.https else 'http'}://{req.host}{req.path}",
            "description": self.description, "mimeType": self.mime_type, "maxTimeoutSeconds": self.ttl,
            "extra": {"invoice": invoice, "amountAeth": format_aeth(self.price), "expiresAt": _rfc3339(expiry),
                      "instructions": _MEMO_INSTRUCTIONS},
        }
        body = {"x402Version": 1, "error": code, "message": message, "accepts": [memo]}
        if self.ledger is not None:
            extra = {"amountAeth": format_aeth(self.price), "depositMemo": DEPOSIT_MEMO_PREFIX + "<address>",
                     "minDeposit": str(self.min_deposit), "instructions": _PREPAID_INSTRUCTIONS}
            if account:
                extra["balance"] = str(self.ledger.balance(account))
            if self.payout:
                extra["withdrawPath"] = WITHDRAW_PATH
            body["accepts"].append({**memo, "scheme": SCHEME_PREPAID, "extra": extra})
        if self.pull_ledger is not None:
            extra = {"amountAeth": format_aeth(self.price), "grantee": self.collector.key.address, "credit": str(self.credit),
                     "instructions": _PULL_INSTRUCTIONS}
            if account:
                extra["owed"] = str(self.pull_ledger.pull_account(account).owed)
            body["accepts"].append({**memo, "scheme": SCHEME_PULL, "extra": extra})
        return _json(402, body, headers)

    # --- aether-prepaid ---

    def _verify(self, req: SellerRequest, pay, body: bytes, pull: bool = False):
        """(payload, max_price) or (code, message)."""
        if not isinstance(pay, dict) or not all(isinstance(pay.get(k), str) for k in ("account", "pubKey", "signature", "requestId", "maxPrice")) \
                or not isinstance(pay.get("timestamp"), int) or isinstance(pay.get("timestamp"), bool):
            return None, ("invalid_payment", "payload must be a signed prepaid request")
        if not is_address(pay["account"]):
            return None, ("invalid_signature", "invalid account address")
        try:
            pub = base64.b64decode(pay["pubKey"], validate=True)
        except ValueError:
            pub = b""
        if len(pub) != 1312:
            return None, ("invalid_signature", "pubKey must be a base64 ML-DSA-44 public key")
        if address_of(pub) != pay["account"]:
            return None, ("invalid_signature", "pubKey does not belong to account")
        try:
            sig = base64.b64decode(pay["signature"], validate=True)
        except ValueError:
            return None, ("invalid_signature", "signature must be base64")
        if abs(self.now() - pay["timestamp"]) > _SIGNED_WINDOW:
            return None, ("stale_request", "sign each request fresh: timestamp must be within 5m0s of the server's clock")
        if not pay["requestId"] or len(pay["requestId"]) > 128:
            return None, ("invalid_payment", "requestId is required (1-128 characters)")
        max_price = 0
        if pay["maxPrice"] != "0":
            try:
                max_price = parse_uaeth(pay["maxPrice"])
            except ValueError as e:
                return None, ("invalid_payment", f"maxPrice: {e}")
        if len(body) > _MAX_SIGNED_BODY:
            return None, ("request_too_large", f"signed requests' bodies are limited to {_MAX_SIGNED_BODY} bytes")
        deposit = pay.get("depositTx") or ""
        if not isinstance(deposit, str):
            return None, ("invalid_payment", "depositTx must be a string")
        msg = (pull_signing_message if pull else signing_message)(
            network=self.network, pay_to=self.pay_to, host=req.host, method=req.method, path=req.path,
            body=body, max_price=max_price, timestamp=pay["timestamp"], request_id=pay["requestId"], deposit_tx=deposit)
        if not Key.verify(pub, msg, sig):
            return None, ("invalid_signature", "signature does not match this request")
        return (pay, max_price), None

    def _serve_prepaid(self, req: SellerRequest, raw, body: bytes):
        ok, bad = self._verify(req, raw, body)
        if bad:
            return self.payment_required(req, *bad)
        pay, max_price = ok
        account = pay["account"]

        def refuse(code, msg, headers=None):
            return self.payment_required(req, code, msg, account=account, headers=headers)
        if max_price < self.price:
            return refuse("price_above_signed_max", f"the price is {self.price} uaeth; the request allows at most {max_price}")
        if pay.get("depositTx"):
            refused = self._credit_deposit(req, pay["depositTx"].strip().upper(), account)
            if refused:
                return refused
        bal, charged, fresh = self.ledger.charge(account, pay["requestId"], self.price, self.now() + _REQUEST_ID_RETENTION)
        if not fresh:
            return refuse("invoice_already_redeemed", "this requestId was already charged and served")
        if not charged:
            return refuse("insufficient_balance", f"balance {bal} uaeth is less than the price {self.price} uaeth: "
                                                  f"deposit to {self.pay_to} with memo {DEPOSIT_MEMO_PREFIX}{account}")
        settlement = _encode_header({"success": True, "network": self.network, "payer": account, "balance": str(bal)})

        def finish(status: int):
            if status >= 500:
                try:
                    self.ledger.refund(account, pay["requestId"], self.price)
                except Exception as e:
                    print(f"paywall: refunding {account} for a failed request: {e}")
        return Serve(Payment(account, SCHEME_PREPAID, balance_uaeth=bal), {"X-PAYMENT-RESPONSE": settlement}, finish,
                     self._receipt_for(req, body, SCHEME_PREPAID, account, pay["requestId"]))

    # --- aether-pull ---

    def _grant(self, buyer: str, fresh: bool):
        """(grant or None, cached). Only allowances found are cached: a buyer who
        just granted one must not be told it has none."""
        with self._grants_lock:
            c = self._grants.get(buyer)
        if not fresh and c and self.now() - c[1] < _GRANT_CACHE:
            return c[0], True
        g = self.client.send_grant(buyer, self.collector.key.address)
        with self._grants_lock:
            if g:
                self._grants[buyer] = (g, self.now())
            else:
                self._grants.pop(buyer, None)
        return g, False

    def _serve_pull(self, req: SellerRequest, raw, body: bytes):
        ok, bad = self._verify(req, raw, body, pull=True)
        if bad:
            return self.payment_required(req, *bad)
        pay, max_price = ok
        buyer = pay["account"]
        ledger, grantee = self.pull_ledger, self.collector.key.address

        def refuse(code, msg, headers=None):
            return self.payment_required(req, code, msg, account=buyer, headers=headers)
        if pay.get("depositTx"):
            return self.payment_required(req, "invalid_payment", "aether-pull requests carry no deposit")
        if max_price < self.price:
            return refuse("price_above_signed_max", f"the price is {self.price} uaeth; the request allows at most {max_price}")
        acct = ledger.pull_account(buyer)

        def check(g):
            if g.expiration is not None and g.expiration < self.now() + 2 * self.collect_every:
                return ("no_grant", f"your allowance for {grantee} expires too soon to collect under; grant one lasting at least {int(2 * self.collect_every)}s")
            if g.allow_list and self.pay_to not in g.allow_list:
                return ("no_grant", "your allowance doesn't allow paying " + self.pay_to)
            need = acct.owed + self.price
            if not g.unlimited and g.spend_limit_uaeth < need:
                if acct.unpaid:
                    return ("pull_unpaid", f"collecting {acct.unpaid} uaeth you owe failed; grant an allowance covering it plus this request ({need} uaeth) to continue")
                return ("grant_too_low", f"your allowance has {g.spend_limit_uaeth} uaeth left and you owe {acct.owed} uaeth not yet collected; this request needs {self.price} more")
            return None
        fresh = False
        while True:
            try:
                g, cached = self._grant(buyer, fresh)
            except Exception as e:
                print(f"paywall: looking up {buyer}'s allowance: {e}")
                return Respond(503, {"Retry-After": "10", "Content-Type": "text/plain"}, b"could not reach the chain to check your allowance; retry\n")
            if g is None:
                return refuse("no_grant", f"grant {grantee} a send allowance limited to {self.pay_to} first (x/authz SendAuthorization)")
            why = check(g)
            if not why:
                break
            if not cached:
                return refuse(*why)
            fresh = True  # it may have been raised since: look again before refusing
        if acct.unpaid:
            ledger.reinstate(buyer)  # the allowance covers the failed collection too: collect it with the next batch
            acct.accrued, acct.unpaid = acct.accrued + acct.unpaid, 0
        # What's being collected counts against the credit until it lands.
        accrued, charged, new = ledger.accrue(buyer, pay["requestId"], self.price, self.credit - acct.in_flight, self.now() + _REQUEST_ID_RETENTION)
        if not new:
            return refuse("invoice_already_redeemed", "this requestId was already charged and served")
        if not charged:
            self._wake.set()
            return refuse("settlement_pending", f"you owe {accrued + acct.in_flight} uaeth, this service's limit before collecting; it's being collected -- retry shortly",
                          {"Retry-After": "10"})
        owed = accrued + acct.in_flight
        if accrued * 2 >= self.credit:
            self._wake.set()
        settle = {"success": True, "network": self.network, "payer": buyer, "owed": str(owed)}
        if not g.unlimited:
            settle["allowance"] = str(g.spend_limit_uaeth - owed)

        def finish(status: int):
            if status >= 500:
                try:
                    ledger.unaccrue(buyer, pay["requestId"], self.price)
                except Exception as e:
                    print(f"paywall: uncharging {buyer} for a failed request: {e}")
        return Serve(Payment(buyer, SCHEME_PULL, owed_uaeth=owed), {"X-PAYMENT-RESPONSE": _encode_header(settle)}, finish,
                     self._receipt_for(req, body, SCHEME_PULL, buyer, pay["requestId"]))

    def start_collecting(self) -> Callable[[], None]:
        """Collects what aether-pull buyers owe every pull_collect_every seconds, in a
        daemon thread; returns a stop function."""
        if self.pull_ledger is None:
            raise ValueError("this paywall doesn't offer aether-pull")
        stop = self._stop = threading.Event()

        def loop():
            while not stop.is_set():
                self.collect_all()
                self._wake.wait(self.collect_every)
                self._wake.clear()
        threading.Thread(target=loop, daemon=True, name="aether-pull-collector").start()
        return stop.set

    def collect_all(self):
        """One pass over every buyer with something to collect."""
        with self._collect_lock:
            for account in self.pull_ledger.collectable():
                try:
                    self._collect(account)
                except Exception as e:
                    print(f"paywall: collecting from {account}: {e}")

    def _collect(self, account: str):
        """One step towards collecting what account owes: open, sign, save (before broadcast) and send, or follow up."""
        ledger = self.pull_ledger
        c = ledger.open_collection(account, os.urandom(8).hex(), self.now())
        if not c:
            return
        for _ in range(2):
            if c["status"] == "reserved":
                tx, seq, h = self.collector.sign_exec(account, self.pay_to, int(c["amount"]), COLLECTION_MEMO_PREFIX + c["id"])
                c = {**c, "status": "pending", "txBytes": base64.b64encode(tx).decode(), "sequence": seq, "txHash": h, "sequenceSpentAt": None}
                ledger.save_collection(c)  # saved before it's broadcast: from here it's only re-sent
            status, log = self.collector.submit(base64.b64decode(c["txBytes"]), int(c.get("sequence") or 0))
            if status == "confirmed":
                with self._grants_lock:
                    self._grants.pop(account, None)
                return ledger.close_collection(account, True)
            if status == "pending":
                return  # checked again next pass
            if status == "failed":
                # Revoked, expired or exhausted allowance, or an empty account: the buyer owes it, and is refused until an allowance covers it.
                print(f"paywall: collecting {c['amount']} uaeth from {account} failed; refusing it until it grants enough: {log}")
                with self._grants_lock:
                    self._grants.pop(account, None)
                return ledger.close_collection(account, False, log)
            # sequence_spent
            now = self.now()
            if not _parse_time(c.get("sequenceSpentAt") or ""):
                c = {**c, "sequenceSpentAt": datetime.fromtimestamp(now, timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.%fZ")}
                ledger.save_collection(c)
            if now - _parse_time(c["sequenceSpentAt"]) < _SEQUENCE_SPENT_GRACE:
                return
            c = {**c, "status": "reserved"}  # can never land: sign afresh

    def _credit_deposit(self, req, tx_hash: str, account: str) -> Optional[Respond]:
        def refuse(code, msg, headers=None):
            return self.payment_required(req, code, msg, account=account, headers=headers)
        if not _HASH.fullmatch(tx_hash):
            return refuse("invalid_deposit", "depositTx must be a transaction hash")
        try:
            t = self.client.get_transaction(tx_hash)
        except Exception as e:
            print(f"paywall: looking up deposit {tx_hash}: {e}")
            return Respond(503, {"Retry-After": "10", "Content-Type": "text/plain"},
                           b"could not reach the chain to verify the deposit; retry\n")
        if t.status == "pending":
            return refuse("payment_not_confirmed", f"deposit {tx_hash} is not in a block yet; retry shortly", {"Retry-After": "10"})
        if t.status == "failed":
            return refuse("payment_failed", f"deposit {tx_hash} failed on chain")
        if not t.memo.startswith(DEPOSIT_MEMO_PREFIX):
            return refuse("invalid_deposit", f"a deposit's memo must be {DEPOSIT_MEMO_PREFIX}<address>")
        beneficiary = t.memo[len(DEPOSIT_MEMO_PREFIX):]
        if not is_address(beneficiary):
            return refuse("invalid_deposit", "the deposit's memo names an invalid address")
        paid, _ = self._received(t.transfers)
        if paid < self.min_deposit:
            return refuse("insufficient_payment", f"deposits must be at least {self.min_deposit} uaeth to {self.pay_to}")
        # Credit whoever the memo names: presenting someone else's deposit only credits them.
        self.ledger.credit(tx_hash, beneficiary, paid)
        return None

    # --- withdrawals ---

    def withdraw(self, req: SellerRequest, body: bytes) -> Respond:
        """Answers a POST to WITHDRAW_PATH. Blocking."""
        def fail(status, error, message, headers=None):
            return _json(status, {"x402Version": 1, "error": error, "message": message}, headers)
        if self.ledger is None or not self.payout:
            return fail(501, "withdrawals_unavailable", "this service doesn't pay back prepaid balances; ask its operator")
        if req.method != "POST":
            return fail(405, "invalid_payment", "POST a signed withdrawal request", {"Allow": "POST"})
        pay = _decode_header(req.header("x-payment") or "")
        if pay is None:
            return fail(400, "invalid_payment", "sign the withdrawal like a prepaid request: X-PAYMENT must be base64-encoded JSON")
        if pay.get("scheme") != SCHEME_PREPAID or pay.get("network") != self.network:
            return fail(400, "unsupported_scheme", f'withdrawals are signed with {SCHEME_PREPAID} on network "{self.network}"')
        ok, bad = self._verify(req, pay.get("payload"), body)
        if bad:
            return fail(403 if bad[0] in ("invalid_signature", "stale_request") else 400, *bad)
        signed_pay, _ = ok
        amount = None
        text = body.decode("utf-8", "replace").strip()
        if text:
            try:
                parsed = json.loads(text)
            except ValueError:
                return fail(400, "invalid_payment", 'the body must be {"amount":"all"} or {"amount":"<uaeth>"}')
            a = parsed.get("amount", "") if isinstance(parsed, dict) else None
            if not isinstance(a, str):
                return fail(400, "invalid_payment", 'amount must be "all" or a positive whole number of uaeth')
            a = a.strip()
            if a and a != "all":
                try:
                    amount = parse_uaeth(a)
                except ValueError:
                    return fail(400, "invalid_payment", 'amount must be "all" or a positive whole number of uaeth')
                if amount <= 0:
                    return fail(400, "invalid_payment", 'amount must be "all" or a positive whole number of uaeth')
        # One at a time: payouts come from one account, and an ID must never be worked on twice at once.
        with self._withdraw_lock:
            return self._do_withdraw(signed_pay["account"], signed_pay["requestId"], amount)

    def _do_withdraw(self, account: str, wid: str, amount: Optional[int]) -> Respond:
        ledger, payout = self.ledger, self.payout

        def respond(status, w, message):
            out = {"x402Version": 1, "withdrawalId": w["id"], "account": account, "amount": w["amount"],
                   "amountAeth": format_aeth(int(w["amount"])), "status": w["status"]}
            if w.get("txHash"):
                out["txHash"] = w["txHash"]
            out.update(balance=str(ledger.balance(account)), message=message)
            return _json(status, out)

        def fail(status, error, message):
            return _json(status, {"x402Version": 1, "withdrawalId": wid, "account": account,
                                  "balance": str(ledger.balance(account)), "error": error, "message": message})
        try:
            w, _, fresh = ledger.reserve_withdrawal(account, wid, amount, self.min_deposit, self.now())
        except LedgerError as e:
            if e.code == "insufficient":
                return fail(409, "insufficient_balance", "the balance doesn't cover that withdrawal")
            return fail(409, "below_minimum_withdrawal", f"withdraw at least {self.min_deposit} uaeth, or the whole balance")
        except Exception as e:
            print(f"paywall: reserving withdrawal {account}/{wid}: {e}")
            return fail(500, "payout_unavailable", "failed to record the withdrawal; nothing was taken")
        requested = "all" if amount is None else str(amount)
        if not fresh and w.get("requested") != requested:
            return fail(409, "withdrawal_id_reused", f'withdrawal "{wid}" was for {w.get("requested")}; use a new ID for a new withdrawal')

        for _ in range(2):
            if w["status"] == "confirmed":
                return respond(200, w, "paid back")
            if w["status"] == "reserved":
                try:
                    tx_bytes, seq, tx_hash = payout.sign(account, int(w["amount"]), "prepaid-withdrawal:" + wid)
                except Exception as e:
                    print(f"paywall: signing withdrawal {account}/{wid}: {e}")
                    return respond(503, w, "the payout couldn't be signed right now; the amount is set aside -- ask again with the same withdrawal ID")
                reserved = dict(w)
                w = {**w, "status": "pending", "txBytes": base64.b64encode(tx_bytes).decode(), "sequence": seq,
                     "txHash": tx_hash, "sequenceSpentAt": None}
                try:  # saved before it's broadcast: from here it's only re-sent
                    ledger.save_withdrawal(w)
                except Exception as e:
                    print(f"paywall: saving withdrawal {account}/{wid}: {e}")
                    return respond(503, {**reserved, "status": "reserved"}, "failed to record the payout; ask again with the same withdrawal ID")
            try:
                status, log = payout.submit(base64.b64decode(w["txBytes"]), int(w.get("sequence") or 0))
            except Exception as e:
                print(f"paywall: submitting withdrawal {account}/{wid}: {e}")
                return respond(202, w, "the payout is signed but the chain couldn't be reached to confirm it was sent; ask again with the same withdrawal ID")
            if status == "confirmed":
                w = {**w, "status": "confirmed", "txBytes": None}
                try:
                    ledger.save_withdrawal(w)
                except Exception as e:
                    print(f"paywall: saving withdrawal {account}/{wid}: {e}")
                return respond(200, w, "paid back")
            if status == "pending":
                return respond(200, w, "sent; in a block within about a minute")
            if status == "failed":
                try:
                    ledger.cancel_withdrawal(account, wid)
                except Exception as e:
                    print(f"paywall: cancelling withdrawal {account}/{wid}: {e}")
                    return respond(503, w, "the payout failed; ask again with the same withdrawal ID")
                print(f"paywall: withdrawal {account}/{wid} failed: {log}")
                return fail(503, "payout_failed", "the service couldn't pay this out right now, so nothing was paid and the balance is back; try again later")
            # sequence_spent
            now = self.now()
            spent_at = _parse_time(w.get("sequenceSpentAt") or "")
            if spent_at is None:
                spent_at = now
                w = {**w, "sequenceSpentAt": datetime.fromtimestamp(now, timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.%fZ")}
                try:
                    ledger.save_withdrawal(w)
                except Exception as e:
                    print(f"paywall: saving withdrawal {account}/{wid}: {e}")
            if now - spent_at < _SEQUENCE_SPENT_GRACE:
                return respond(200, w, "sent; not visible on chain yet -- ask again with the same withdrawal ID")
            w = {**w, "status": "reserved"}  # long enough: these bytes can never land. Sign afresh.
        return respond(200, w, "sent")

    # --- adapters ---

    def _route(self, req: SellerRequest, free: Iterable[str]):
        """'manifest', 'withdraw', 'free' or 'paid'."""
        if req.path == MANIFEST_PATH:
            return "manifest"
        if req.path == WITHDRAW_PATH:
            return "withdraw"
        if any(req.path.startswith(p) for p in free):
            return "free"
        return "paid"

    def _manifest_response(self, req: SellerRequest) -> Respond:
        if req.method not in ("GET", "HEAD"):
            return Respond(405, {"Allow": "GET, HEAD", "Content-Type": "text/plain"}, b"use GET\n")
        return _json(200, self.manifest(), {"Cache-Control": "max-age=300"})

    def wsgi(self, app, free: Iterable[str] = ()):
        """WSGI middleware (Flask: app.wsgi_app = pw.wsgi(app.wsgi_app))."""
        free = tuple(free)

        def middleware(environ, start_response):
            path = (environ.get("SCRIPT_NAME", "") + environ.get("PATH_INFO", "")) or "/"
            path = path.encode("latin-1").decode("utf-8", "replace")  # WSGI carries the raw bytes as latin-1
            host = environ.get("HTTP_HOST") or f"{environ.get('SERVER_NAME', '')}:{environ.get('SERVER_PORT', '')}"
            headers = {k[5:].replace("_", "-").lower(): v for k, v in environ.items() if k.startswith("HTTP_")}
            https = environ.get("wsgi.url_scheme") == "https" or headers.get("x-forwarded-proto") == "https"
            req = SellerRequest(environ.get("REQUEST_METHOD", "GET").upper(), host, path, headers, https)

            def reply(r: Respond):
                start_response(f"{r.status} {_REASONS.get(r.status, '')}".strip(), list(r.headers.items()))
                return [r.body]
            route = self._route(req, free)
            if route == "manifest":
                return reply(self._manifest_response(req))
            if route == "free":
                return app(environ, start_response)
            body = b""
            if self.needs_body(req):
                try:
                    n = int(environ.get("CONTENT_LENGTH") or 0)
                except ValueError:
                    n = 0
                body = environ["wsgi.input"].read(min(n, _MAX_SIGNED_BODY + 1)) if n > 0 else b""
                environ["wsgi.input"] = io.BytesIO(body)
                environ["CONTENT_LENGTH"] = str(len(body))
                if len(body) > _MAX_SIGNED_BODY and self.receipt_key:
                    return reply(Respond(413, {"Content-Type": "text/plain"}, f"paid requests' bodies are limited to {_MAX_SIGNED_BODY} bytes\n".encode()))
            if route == "withdraw":
                return reply(self.withdraw(req, body))
            d = self.handle(req, body)
            if isinstance(d, Respond):
                return reply(d)
            environ["aether.payment"] = d.payment

            if d.receipt:
                return self._wsgi_with_receipt(app, environ, start_response, d)

            def paid_start_response(status, response_headers, exc_info=None):
                d.finish(int(status.split()[0]))
                return start_response(status, list(response_headers) + list(d.headers.items()), exc_info)
            return app(environ, paid_start_response)
        return middleware

    @staticmethod
    def _wsgi_with_receipt(app, environ, start_response, d: Serve):
        """Holds the app's response until it's complete, so its receipt (a header)
        can cover the body; past _MAX_RECEIPT_BODY it streams, with a receipt
        that has no response hash."""
        held = {}

        def hold(status, response_headers, exc_info=None):
            held.update(status=status, headers=list(response_headers) + list(d.headers.items()))
            return lambda data: chunks.append(data)  # the legacy write() callable
        chunks = []
        result = app(environ, hold)

        def start(body: Optional[bytes]):
            code = int(held["status"].split()[0])
            d.finish(code)
            receipt = d.receipt(code, body)
            start_response(held["status"], held["headers"] + ([(RECEIPT_HEADER, receipt)] if receipt else []))

        def gen():
            try:
                size, streaming = sum(len(c) for c in chunks), False
                for chunk in result:
                    if streaming:
                        yield chunk
                        continue
                    chunks.append(chunk)
                    size += len(chunk)
                    if size > _MAX_RECEIPT_BODY:
                        streaming = True
                        start(None)
                        yield b"".join(chunks)
                        chunks.clear()
                if not streaming:
                    body = b"".join(chunks)
                    start(body)
                    yield body
            finally:
                if hasattr(result, "close"):
                    result.close()
        return gen()

    def asgi(self, app, free: Iterable[str] = ()):
        """ASGI middleware (FastAPI/Starlette: app = pw.asgi(app))."""
        free = tuple(free)

        async def middleware(scope, receive, send):
            if scope["type"] != "http":
                return await app(scope, receive, send)
            headers = {k.decode("latin-1").lower(): v.decode("latin-1") for k, v in scope.get("headers", [])}
            https = scope.get("scheme") == "https" or headers.get("x-forwarded-proto") == "https"
            req = SellerRequest(scope["method"].upper(), headers.get("host", ""), scope.get("path") or "/", headers, https)

            async def reply(r: Respond):
                await send({"type": "http.response.start", "status": r.status,
                            "headers": [(k.lower().encode("latin-1"), v.encode("latin-1")) for k, v in r.headers.items()]})
                await send({"type": "http.response.body", "body": r.body})
            route = self._route(req, free)
            if route == "manifest":
                return await reply(self._manifest_response(req))
            if route == "free":
                return await app(scope, receive, send)
            body, downstream_receive = b"", receive
            if self.needs_body(req):
                chunks, size, more = [], 0, True
                while more:
                    msg = await receive()
                    if msg["type"] == "http.disconnect":
                        return
                    chunk = msg.get("body", b"")
                    size += len(chunk)
                    if size <= _MAX_SIGNED_BODY + 1:
                        chunks.append(chunk)
                    more = msg.get("more_body", False)
                body = b"".join(chunks)[: _MAX_SIGNED_BODY + 1]
                replayed = False

                async def replay():
                    nonlocal replayed
                    if not replayed:
                        replayed = True
                        return {"type": "http.request", "body": body, "more_body": False}
                    return await receive()
                downstream_receive = replay
            loop = asyncio.get_running_loop()
            if route == "withdraw":
                return await reply(await loop.run_in_executor(None, self.withdraw, req, body))
            d = await loop.run_in_executor(None, self.handle, req, body)
            if isinstance(d, Respond):
                return await reply(d)
            scope = {**scope, "aether": d.payment}

            extra = [(k.lower().encode("latin-1"), v.encode("latin-1")) for k, v in d.headers.items()]
            if d.receipt:
                return await self._asgi_with_receipt(app, scope, downstream_receive, send, d, extra)

            async def paid_send(message):
                if message["type"] == "http.response.start":
                    d.finish(message["status"])
                    message = {**message, "headers": list(message.get("headers", [])) + extra}
                await send(message)
            return await app(scope, downstream_receive, paid_send)
        return middleware

    @staticmethod
    async def _asgi_with_receipt(app, scope, receive, send, d: Serve, extra):
        """Holds the response until its last body message, so its receipt can
        cover the body; past _MAX_RECEIPT_BODY it streams without a response hash."""
        start, chunks, state = {}, [], {"size": 0, "streaming": False}

        async def begin(body: Optional[bytes]):
            d.finish(start["status"])
            receipt = d.receipt(start["status"], body)
            receipt_header = [(RECEIPT_HEADER.lower().encode(), receipt.encode("latin-1"))] if receipt else []
            await send({**start, "headers": list(start.get("headers", [])) + extra + receipt_header})

        async def held_send(message):
            if message["type"] == "http.response.start":
                start.update(message)
                return
            if message["type"] != "http.response.body" or state["streaming"]:
                return await send(message)
            chunks.append(message.get("body", b""))
            state["size"] += len(chunks[-1])
            more = message.get("more_body", False)
            if state["size"] > _MAX_RECEIPT_BODY:
                state["streaming"] = True
                await begin(None)
                await send({"type": "http.response.body", "body": b"".join(chunks), "more_body": more})
                chunks.clear()
            elif not more:
                body = b"".join(chunks)
                await begin(body)
                await send({"type": "http.response.body", "body": body})
        await app(scope, receive, held_send)


_REASONS = {200: "OK", 202: "Accepted", 400: "Bad Request", 402: "Payment Required", 403: "Forbidden", 405: "Method Not Allowed",
            409: "Conflict", 500: "Internal Server Error", 501: "Not Implemented", 503: "Service Unavailable"}
