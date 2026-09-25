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
buyers can withdraw what they haven't spent.

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
from .paywall import DEPOSIT_MEMO_PREFIX, SCHEME_MEMO, SCHEME_PREPAID, signing_message
from .rpc import RpcError
from .tx import build_send

WITHDRAW_PATH = "/.well-known/x402/withdraw"

_INVOICE_PREFIX = "x402-"
_SIGNED_WINDOW = 300
_REQUEST_ID_RETENTION = 24 * 3600
_MAX_SIGNED_BODY = 10 << 20
_SEQUENCE_SPENT_GRACE = 120
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


@dataclass
class Payment:
    """Who paid for a request."""
    payer: str
    scheme: str
    tx_hash: Optional[str] = None  # aether-memo
    balance_uaeth: Optional[int] = None  # aether-prepaid: left with this seller


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
                 payout_key: Optional[Key] = None, now: Callable[[], float] = time.time):
        """price and min_deposit carry their unit ("0.01 AETH"). prepaid_ledger (a
        FileLedger or a path) offers aether-prepaid: it holds customers'
        balances, so back it up. payout_key pays back unspent balances on
        request: keep only a small float in that account."""
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

    def _schemes(self):
        return [SCHEME_MEMO, SCHEME_PREPAID] if self.ledger is not None else [SCHEME_MEMO]

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
        pay = _decode_header(req.header("x-payment") or "")
        return bool(pay) and pay.get("scheme") == SCHEME_PREPAID

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
        return Serve(Payment(payer, SCHEME_MEMO, tx_hash=tx_hash), {"X-PAYMENT-RESPONSE": settlement}, finish)

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
        return _json(402, body, headers)

    # --- aether-prepaid ---

    def _verify(self, req: SellerRequest, pay, body: bytes):
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
        msg = signing_message(network=self.network, pay_to=self.pay_to, host=req.host, method=req.method, path=req.path,
                              body=body, max_price=max_price, timestamp=pay["timestamp"], request_id=pay["requestId"],
                              deposit_tx=deposit)
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
        return Serve(Payment(account, SCHEME_PREPAID, balance_uaeth=bal), {"X-PAYMENT-RESPONSE": settlement}, finish)

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
            if route == "withdraw":
                return reply(self.withdraw(req, body))
            d = self.handle(req, body)
            if isinstance(d, Respond):
                return reply(d)
            environ["aether.payment"] = d.payment

            def paid_start_response(status, response_headers, exc_info=None):
                d.finish(int(status.split()[0]))
                return start_response(status, list(response_headers) + list(d.headers.items()), exc_info)
            return app(environ, paid_start_response)
        return middleware

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

            async def paid_send(message):
                if message["type"] == "http.response.start":
                    d.finish(message["status"])
                    message = {**message, "headers": list(message.get("headers", [])) +
                               [(k.lower().encode("latin-1"), v.encode("latin-1")) for k, v in d.headers.items()]}
                await send(message)
            return await app(scope, downstream_receive, paid_send)
        return middleware


_REASONS = {200: "OK", 202: "Accepted", 400: "Bad Request", 402: "Payment Required", 403: "Forbidden", 405: "Method Not Allowed",
            409: "Conflict", 500: "Internal Server Error", 501: "Not Implemented", 503: "Service Unavailable"}
