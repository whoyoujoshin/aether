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


class PullAccount:
    """Where one aether-pull buyer stands, in uaeth."""

    def __init__(self, accrued=0, in_flight=0, unpaid=0):
        self.accrued, self.in_flight, self.unpaid = accrued, in_flight, unpaid  # charged; being collected; from failed collections

    @property
    def owed(self) -> int:
        return self.accrued + self.in_flight + self.unpaid


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
        for k in ("pull", "pullRequests", "collections"):
            if not isinstance(self.state.get(k), dict):
                self.state[k] = {}

    # --- aether-pull: what buyers owe (the Go paywall's PullLedger) ---

    def pull_account(self, account: str) -> PullAccount:
        with self._lock:
            r = self.state["pull"].get(account) or {}
            return PullAccount(_num(r.get("accrued")), _num((r.get("open") or {}).get("amount")), _num(r.get("unpaid")))

    def _drop_if_empty(self, account: str):
        r = self.state["pull"].get(account)
        if r is not None and not r.get("accrued") and not r.get("unpaid") and not r.get("open"):
            del self.state["pull"][account]

    def accrue(self, account: str, request_id: str, amount: int, limit: int, forget_after: float):
        """Charges amount for a request_id not charged before: (accrued, ok, fresh). ok is False
        (nothing changes) if what's accrued would exceed limit."""
        with self._lock:
            key = f"{account}/{request_id}"
            accrued = self.pull_account(account).accrued
            if key in self.state["pullRequests"]:
                return accrued, False, False
            if accrued + amount > limit:
                return accrued, False, True

            def change():
                self.state["pull"].setdefault(account, {})["accrued"] = str(accrued + amount)
                self.state["pullRequests"][key] = {"amount": str(amount), "forgetAfter": _iso(forget_after)}
                return accrued + amount, True, True
            return self._mutate(change)

    def unaccrue(self, account: str, request_id: str, amount: int):
        """Reverses an accrue not yet taken into a collection."""
        with self._lock:
            key = f"{account}/{request_id}"
            if key not in self.state["pullRequests"]:
                raise KeyError(f"request {key} was not charged")
            accrued = self.pull_account(account).accrued
            if accrued < amount:
                raise ValueError(f"request {key} is already being collected")

            def change():
                r = self.state["pull"].setdefault(account, {})
                if accrued - amount:
                    r["accrued"] = str(accrued - amount)
                else:
                    r.pop("accrued", None)
                del self.state["pullRequests"][key]
                self._drop_if_empty(account)
            self._mutate(change)

    def collectable(self):
        """Accounts with something accrued or a collection open."""
        with self._lock:
            return sorted(a for a, r in self.state["pull"].items() if r.get("accrued") or r.get("open"))

    def open_collection(self, account: str, cid: str, at: float) -> Optional[dict]:
        """The account's open collection, or a new one for everything accrued (None: nothing to collect)."""
        with self._lock:
            r = self.state["pull"].get(account)
            if not r:
                return None
            if r.get("open"):
                return dict(r["open"])
            if not r.get("accrued"):
                return None

            def change():
                c = {"id": cid, "account": account, "amount": r["accrued"], "status": "reserved", "at": _iso(at)}
                r["open"] = c
                del r["accrued"]
                return dict(c)
            return self._mutate(change)

    def save_collection(self, c: dict):
        with self._lock:
            r = self.state["pull"].get(c["account"])
            if not r or not r.get("open") or r["open"]["id"] != c["id"]:
                raise KeyError(f"collection {c['id']} is not open")

            def change():
                r["open"] = {k: v for k, v in c.items() if v is not None}
            self._mutate(change)

    def close_collection(self, account: str, collected: bool, log: str = ""):
        """Ends the open collection: collected, or failed (its amount becomes unpaid)."""
        with self._lock:
            r = self.state["pull"].get(account)
            if not r or not r.get("open"):
                raise KeyError(f"{account} has no open collection")

            def change():
                c = {k: v for k, v in r["open"].items() if k != "txBytes"}
                c["status"] = "confirmed" if collected else "failed"
                if log:
                    c["log"] = log
                if not collected:
                    r["unpaid"] = str(_num(r.get("unpaid")) + _num(c["amount"]))
                self.state["collections"][c["id"]] = c
                del r["open"]
                self._drop_if_empty(account)
            self._mutate(change)

    def reinstate(self, account: str):
        """Moves unpaid back to accrued, to collect again."""
        with self._lock:
            r = self.state["pull"].get(account)
            if not r or not r.get("unpaid"):
                return

            def change():
                r["accrued"] = str(_num(r.get("accrued")) + _num(r["unpaid"]))
                del r["unpaid"]
            self._mutate(change)

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
        for m in (self.state["requests"], self.state["pullRequests"]):
            for k, r in list(m.items()):
                t = _parse_time(r.get("forgetAfter", ""))
                if t is not None and t < now:
                    del m[k]
        if not self.path:
            return
        d = os.path.dirname(os.path.abspath(self.path))
        os.makedirs(d, mode=0o700, exist_ok=True)
        tmp = self.path + ".tmp"
        fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, "wb") as f:
            # Like the Go ledger: empty aether-pull maps are left out.
            out = {k: v for k, v in self.state.items() if v or k not in ("pull", "pullRequests", "collections")}
            f.write(json.dumps(out, indent=2).encode())
            f.flush()
            os.fsync(f.fileno())
        os.replace(tmp, self.path)


def _now() -> float:
    return time.time()
