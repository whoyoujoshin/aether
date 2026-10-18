"""quote_swap: what an amount of AETH would fetch in OSMO from the AETH/OSMO pool on
Osmosis testnet, from Osmosis's own estimate (which includes its taker fee as well
as the pool's swap fee). Read-only. The same tool as agentmcp's
(cmd/agentmcp/swapquote.go) and the TypeScript client's: the same reads, cache,
pacing, fields and rounding; clients/testdata/swapquote.json keeps them alike.
"""

import hashlib
import json
import re
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timezone
from fractions import Fraction
from typing import Callable, Dict, List, Optional, Tuple

from .amount import format_aeth, parse_amount

TESTNET_OSMOSIS_POOL = 1370
TESTNET_OSMOSIS_REST = ["https://lcd.osmotest5.osmosis.zone", "https://lcd.testnet.osmosis.zone"]
_CHANNEL_ON_AETHER = "channel-3"
_CHANNEL_ON_OSMOSIS = "channel-11852"
_QUOTE_TTL = 10.0
_UPSTREAM_PER_MIN = 30  # Osmosis's public endpoints ban callers that poll hard
_MAX_SLIPPAGE = 50


def _ibc_denom(trace: str) -> str:
    return "ibc/" + hashlib.sha256(trace.encode()).hexdigest().upper()


AETH_ON_OSMOSIS = _ibc_denom(f"transfer/{_CHANNEL_ON_OSMOSIS}/uaeth")
"""AETH's denom on Osmosis."""
OSMO_ON_AETHER = _ibc_denom(f"transfer/{_CHANNEL_ON_AETHER}/uosmo")
"""OSMO's denom on Aether."""

# fetch(url) -> (HTTP status, body text); raises on a network failure.
Fetch = Callable[[str], Tuple[int, str]]


def _error(code: str, message: str, **extra) -> dict:
    return {"error": {"code": code, "message": message, **extra}}


def _fixed(r: Fraction, places: int) -> str:
    """r as a decimal with places digits, halves rounded away from zero (Go's big.Rat FloatString)."""
    neg = r < 0
    a = -r if neg else r
    scaled = a * 10 ** places
    q, rem = divmod(scaled.numerator, scaled.denominator)
    if rem * 2 >= scaled.denominator:
        q += 1
    s = str(q).rjust(places + 1, "0")
    return ("-" if neg else "") + (f"{s[:-places]}.{s[-places:]}" if places else s)


def _format_micro(n: int) -> str:
    """A 6-decimal base amount in whole units, trailing zeros trimmed."""
    s = _fixed(Fraction(n, 1_000_000), 6).rstrip("0")
    return s[:-1] if s.endswith(".") else s


def _osmo(uosmo: int) -> dict:
    return {"osmo": _format_micro(uosmo), "uosmo": str(uosmo), "denomOnAether": OSMO_ON_AETHER}


def _urllib_fetch(url: str) -> Tuple[int, str]:
    req = urllib.request.Request(url, headers={"Accept": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            return resp.status, resp.read().decode()
    except urllib.error.HTTPError as e:
        return e.code, (e.read() or b"").decode(errors="replace")


class SwapQuoter:
    """Quotes AETH into OSMO, caching answers for 10 s and asking Osmosis at most 30 times a minute."""

    def __init__(self, rest: Optional[List[str]] = None, pool: int = TESTNET_OSMOSIS_POOL,
                 fetch: Optional[Fetch] = None, clock: Callable[[], float] = time.time):
        self.rest = [u.strip().rstrip("/") for u in (TESTNET_OSMOSIS_REST if rest is None else rest) if u.strip()]
        self.pool = pool
        self._fetch = fetch or _urllib_fetch
        self._clock = clock
        self._lock = threading.Lock()
        self._answers: Dict[str, dict] = {}
        self._upstream: List[float] = []

    def quote(self, amount: str = "", slippage_percent=0) -> dict:
        if not self.rest:
            return _error("SWAP_NOT_CONFIGURED", "quote_swap is off: no Osmosis endpoint is set")
        text = str(amount or "")
        if re.search(r"usdc", text, re.I):
            return _error("INVALID_AMOUNT", 'quote_swap quotes AETH into OSMO; amount must be AETH, e.g. "1 AETH"')
        try:
            uaeth = parse_amount(text)
        except ValueError as e:
            return _error("INVALID_AMOUNT", str(e))
        if slippage_percent is None:
            slippage_percent = 0
        if isinstance(slippage_percent, bool) or not isinstance(slippage_percent, (int, float)) or slippage_percent != slippage_percent:
            return _error("INVALID_ARGUMENT", "slippage_percent must be a number")
        slip = slippage_percent or 1
        if slip < 0 or slip > _MAX_SLIPPAGE:
            return _error("INVALID_ARGUMENT", f"slippage_percent must be between 0 and {_MAX_SLIPPAGE}")

        q = self._get(uaeth)
        if "error" in q:
            return q
        return self._output(q, uaeth, Fraction(repr(slip)))

    def _get(self, amount: int) -> dict:
        key = f"{self.pool}/{amount}"
        with self._lock:
            now = self._clock()
            cached = self._answers.get(key)
            if cached and now - cached["at"] < _QUOTE_TTL:
                return cached
            self._answers = {k: v for k, v in self._answers.items() if now - v["at"] < _QUOTE_TTL}
            self._upstream = [t for t in self._upstream if now - t < 60]
            if len(self._upstream) >= _UPSTREAM_PER_MIN:
                wait = int(60 - (now - self._upstream[0])) + 1
                return _error("QUOTE_UNAVAILABLE", "too many quotes this minute; try again shortly", retryAfterSeconds=wait)
            self._upstream.append(now)
        last = ""
        for base in self.rest:
            q = self._fetch_from(base, amount)
            if "error" not in q:
                with self._lock:
                    self._answers[key] = q
                return q
            if q["error"]["code"] == "INVALID_AMOUNT":
                return q  # Osmosis refused this amount; another endpoint won't differ
            last = q["error"]["message"]
        return _error("QUOTE_UNAVAILABLE", f"no Osmosis endpoint answered: {last}")

    def _get_json(self, url: str):
        """(body, None) or (None, error result)."""
        try:
            status, text = self._fetch(url)
        except Exception as e:  # noqa: BLE001
            return None, _error("UNAVAILABLE", f"{url}: {e}")
        if status == 400:
            msg = text.strip()
            try:
                msg = json.loads(text).get("message") or msg
            except (ValueError, AttributeError):
                pass
            return None, _error("INVALID_AMOUNT", f"Osmosis refused the estimate: {msg}")
        if status != 200:
            return None, _error("UNAVAILABLE", f"{url}: HTTP {status}")
        try:
            return json.loads(text), None
        except ValueError as e:
            return None, _error("UNAVAILABLE", f"{url}: {e}")

    def _fetch_from(self, base: str, amount: int) -> dict:
        body, err = self._get_json(f"{base}/osmosis/poolmanager/v1beta1/pools/{self.pool}")
        if err:
            return err
        pool = (body or {}).get("pool") or {}
        if pool.get("@type") != "/osmosis.gamm.v1beta1.Pool":
            return _error("UNAVAILABLE", f'pool {self.pool} is "{pool.get("@type")}", not a balancer pool')
        reserve_in = reserve_out = weight_in = weight_out = 0
        for pa in pool.get("pool_assets") or []:
            token = (pa or {}).get("token") or {}
            denom, amt, weight = token.get("denom"), str(token.get("amount")), str((pa or {}).get("weight"))
            if not amt.isdigit() or not weight.isdigit():
                return _error("UNAVAILABLE", f"pool {self.pool}: unreadable asset {denom}")
            if denom == AETH_ON_OSMOSIS:
                reserve_in, weight_in = int(amt), int(weight)
            if denom == "uosmo":
                reserve_out, weight_out = int(amt), int(weight)
        if min(reserve_in, reserve_out, weight_in, weight_out) <= 0:
            return _error("UNAVAILABLE", f"pool {self.pool} does not hold both AETH and OSMO")
        try:
            swap_fee = Fraction(str((pool.get("pool_params") or {}).get("swap_fee")))
        except ValueError:
            return _error("UNAVAILABLE", f"pool {self.pool}: unreadable swap fee")

        token_in = urllib.parse.quote(f"{amount}{AETH_ON_OSMOSIS}", safe="")
        body, err = self._get_json(f"{base}/osmosis/poolmanager/v1beta1/{self.pool}/estimate/single_pool_swap_exact_amount_in"
                                   f"?pool_id={self.pool}&token_in={token_in}&token_out_denom=uosmo")
        if err:
            return err
        out = str((body or {}).get("token_out_amount", ""))
        if not out.isdigit() or int(out) <= 0:
            return _error("INVALID_AMOUNT", f"Osmosis estimates no OSMO for {format_aeth(amount)} AETH")
        return {"pool": self.pool, "reserve_in": reserve_in, "reserve_out": reserve_out, "weight_in": weight_in,
                "weight_out": weight_out, "swap_fee": swap_fee, "out": int(out), "at": self._clock()}

    def _output(self, q: dict, amount: int, slip: Fraction) -> dict:
        # Spot, OSMO per AETH: (Ro/Wo)/(Ri/Wi).
        spot = Fraction(q["reserve_out"] * q["weight_in"], q["weight_out"] * q["reserve_in"])
        price = Fraction(q["out"], amount)
        cost = (1 - price / spot) * 100
        min_out = q["out"] * (100 - slip) // 100  # floor
        a = format_aeth(amount)
        at = datetime.fromtimestamp(q["at"], timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
        return {
            "chain": "osmo-test-5",
            "pool": q["pool"],
            "in": {"asset": "AETH", "amount": a, "base": str(amount), "denom": "uaeth", "uaeth": str(amount), "aeth": a},
            "out": _osmo(q["out"]),
            "spotPrice": _fixed(spot, 6),
            "price": _fixed(price, 6),
            "costPercent": _fixed(cost, 2),
            "swapFeePercent": _fixed(q["swap_fee"] * 100, 2),
            "reserves": {"aeth": format_aeth(q["reserve_in"]), "osmo": _format_micro(q["reserve_out"])},
            "slippagePercent": _fixed(slip, 2),
            "minOutput": _osmo(int(min_out)),
            "quotedAt": at,
            "note": "A quote, not a promise: the pool moves with every trade, and it is small, so a large swap gets a poor "
                    "price (watch costPercent). Swapping from Aether goes through contracts/aether-swap on Osmosis and back "
                    "over IBC, about 22 s; this tool only quotes it.",
        }
