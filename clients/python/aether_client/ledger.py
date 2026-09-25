"""Prepaid balances a seller holds for its buyers.

The file format is the Go paywall's (paywall.FileLedger), so a seller can
move between them.
"""

import copy
import json
import os
import re
import threading
import time
from datetime import datetime, timezone
from typing import Optional

_UINT = re.compile(r"[0-9]+")


def _num(s) -> int:
    return int(s) if isinstance(s, str) and _UINT.fullmatch(s) else 0


def _iso(ts: float) -> str:
    return datetime.fromtimestamp(ts, timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.%fZ")


def _parse_time(s: str) -> Optional[float]:
    if not s or s.startswith("0001-"):
        return None
    m = re.fullmatch(r"(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(?:\.(\d+))?(Z|[+-]\d\d:\d\d)", s)
    if not m:
        return None
    base = datetime.strptime(m.group(1), "%Y-%m-%dT%H:%M:%S")
    tz = timezone.utc if m.group(3) == "Z" else datetime.strptime(m.group(3).replace(":", ""), "%z").tzinfo
    frac = float("0." + m.group(2)) if m.group(2) else 0.0
    return base.replace(tzinfo=tz).timestamp() + frac


class LedgerError(Exception):
    def __init__(self, code: str, message: str):
        super().__init__(message)
        self.code = code  # insufficient | below_minimum


class FileLedger:
    """A ledger in one JSON file, rewritten atomically (temp file, fsync,
    rename) on every change. Every method is atomic: these are customers'
    funds. For one server process; several need a shared ledger (e.g. a
    database) with the same methods. path "" keeps it in memory (tests)."""

    def __init__(self, path: str):
        self.path = path
        self._lock = threading.RLock()
        self.state = {"balances": {}, "deposits": {}, "requests": {}, "withdrawals": {}}
        if path and os.path.exists(path):
            try:
                with open(path) as f:
                    self.state = json.load(f)
            except ValueError as e:
                raise ValueError(f"prepaid ledger {path} is unreadable: {e}") from None
            for k in ("balances", "deposits", "requests", "withdrawals"):
                if not isinstance(self.state.get(k), dict):
                    self.state[k] = {}

    def balance(self, account: str) -> int:
        with self._lock:
            return _num(self.state["balances"].get(account))

    def credit(self, deposit_tx: str, account: str, amount: int):
        """Adds a deposit to account, once per transaction hash: (balance, credited)."""
        with self._lock:
            if deposit_tx in self.state["deposits"]:
                return self.balance(account), False

            def change():
                bal = self.balance(account) + amount
                self.state["balances"][account] = str(bal)
                self.state["deposits"][deposit_tx] = {"account": account, "amount": str(amount), "at": _iso(_now())}
                return bal, True
            return self._mutate(change)

    def charge(self, account: str, request_id: str, amount: int, forget_after: float):
        """Deducts amount for a request ID not charged before: (balance, ok, fresh)."""
        with self._lock:
            key = f"{account}/{request_id}"
            bal = self.balance(account)
            if key in self.state["requests"]:
                return bal, False, False
            if bal < amount:
                return bal, False, True

            def change():
                self.state["balances"][account] = str(bal - amount)
                self.state["requests"][key] = {"amount": str(amount), "forgetAfter": _iso(forget_after)}
                return bal - amount, True, True
            return self._mutate(change)

    def refund(self, account: str, request_id: str, amount: int):
        """Reverses a charge, forgetting the request ID."""
        with self._lock:
            key = f"{account}/{request_id}"
            if key not in self.state["requests"]:
                return

            def change():
                self.state["balances"][account] = str(self.balance(account) + amount)
                del self.state["requests"][key]
            self._mutate(change)

    def reserve_withdrawal(self, account: str, wid: str, amount: Optional[int], minimum: int, at: float):
        """Deducts a withdrawal (amount None: the whole balance) and records it
        under wid, once: (withdrawal, balance, fresh). Raises LedgerError."""
        with self._lock:
            key = f"{account}/{wid}"
            bal = self.balance(account)
            seen = self.state["withdrawals"].get(key)
            if seen:
                return dict(seen), bal, False
            take = bal if amount is None else amount
            if bal <= 0 or take <= 0 or take > bal:
                raise LedgerError("insufficient", "insufficient balance")
            if take < minimum and take != bal:
                raise LedgerError("below_minimum", "below the minimum withdrawal")

            def change():
                w = {"id": wid, "account": account, "requested": "all" if amount is None else str(amount),
                     "amount": str(take), "status": "reserved", "at": _iso(at)}
                self.state["balances"][account] = str(bal - take)
                self.state["withdrawals"][key] = w
                return dict(w), bal - take, True
            return self._mutate(change)

    def save_withdrawal(self, w: dict):
        with self._lock:
            key = f"{w['account']}/{w['id']}"
            if key not in self.state["withdrawals"]:
                raise KeyError("withdrawal not found")

            def change():
                self.state["withdrawals"][key] = {k: v for k, v in w.items() if v is not None}
            self._mutate(change)

    def cancel_withdrawal(self, account: str, wid: str):
        """Returns a reserved withdrawal to the balance and forgets it: only for a payout that can never land."""
        with self._lock:
            key = f"{account}/{wid}"
            w = self.state["withdrawals"].get(key)
            if not w:
                raise KeyError("withdrawal not found")

            def change():
                self.state["balances"][account] = str(self.balance(account) + _num(w.get("amount")))
                del self.state["withdrawals"][key]
            self._mutate(change)

    def _mutate(self, fn):
        snapshot = copy.deepcopy(self.state)
        try:
            r = fn()
            self._save()
            return r
        except BaseException:
            self.state = snapshot
            raise

    def _save(self):
        now = _now()
        for k, r in list(self.state["requests"].items()):
            t = _parse_time(r.get("forgetAfter", ""))
            if t is not None and t < now:
                del self.state["requests"][k]
        if not self.path:
            return
        d = os.path.dirname(os.path.abspath(self.path))
        os.makedirs(d, mode=0o700, exist_ok=True)
        tmp = self.path + ".tmp"
        fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, "wb") as f:
            f.write(json.dumps(self.state, indent=2).encode())
            f.flush()
            os.fsync(f.fileno())
        os.replace(tmp, self.path)


def _now() -> float:
    return time.time()
