"""The on-chain service directory: services announce themselves with 1 uaeth to
a keyless address, memo "x402-service:<url>". A listing counts only if the
manifest at that URL names the announcer as payee."""

import hashlib
import ipaddress
import json
import socket
import urllib.parse
from dataclasses import dataclass, field
from typing import Iterable, List, Optional

from . import bech32
from .amount import parse_amount, parse_uaeth
from .client import AetherClient
from .keys import PREFIX
from .paywall import _NoRedirect

ANNOUNCE_PREFIX = "x402-service:"
DELIST_PREFIX = "x402-delist:"
MANIFEST_PATH = "/.well-known/x402"
# sha256("aether-x402-directory")[:20], as Go's address.Module derives it.
DIRECTORY_ADDRESS = bech32.encode(PREFIX, hashlib.sha256(b"aether-x402-directory").digest()[:20])


RATE_PREFIX = "x402-rate:"
DEFAULT_WINDOW = 10_080  # blocks reputation looks back: about a week of ~60s blocks


@dataclass
class Rating:
    rater: str
    url: str
    score: int  # 1-5
    height: int
    tx_hash: str


@dataclass
class RatingSummary:
    count: int = 0
    average: Optional[float] = None


@dataclass
class Reputation:
    """What the chain says about a service. Fees are zero, so a seller can pay
    itself from accounts it controls for free: payments, payers and ratings from
    unknown accounts can be manufactured. trusted_ratings -- from accounts you
    pass as trusted (your own, your owner's) -- can't."""
    window_blocks: int
    payments: int
    payers: int
    volume_uaeth: int
    ratings: RatingSummary  # from accounts that paid the service before rating it
    trusted_ratings: RatingSummary
    raters: List[Rating] = field(default_factory=list)


@dataclass
class Service:
    url: str
    announcer: str
    height: int
    manifest: dict  # name/description are set by the service: untrusted
    reputation: Optional[Reputation] = None


def rating_memo(url: str, score: int) -> str:
    """The memo that rates url 1-5."""
    if not isinstance(score, int) or isinstance(score, bool) or not 1 <= score <= 5:
        raise ValueError("a rating's score is 1 to 5")
    return f"{RATE_PREFIX}{score}:{normalize_url(url)}"


def rate_service(client: AetherClient, key, url: str, score: int):
    """Rates a service you've paid (only raters who paid it count). Costs 1 uaeth;
    your latest rating of a service replaces earlier ones."""
    return client.send(key, DIRECTORY_ADDRESS, "1uaeth", memo=rating_memo(url, score))


def _summarize(ratings, include=None) -> RatingSummary:
    picked = [r for r in ratings if include is None or include(r.rater)]
    return RatingSummary(len(picked), sum(r.score for r in picked) / len(picked)) if picked else RatingSummary()


def normalize_url(raw: str) -> str:
    u = urllib.parse.urlsplit(raw.strip())
    if u.scheme not in ("http", "https") or not u.netloc or "@" in u.netloc or u.query or u.fragment:
        raise ValueError(f'service URL "{raw}" must be a plain http(s) URL')
    s = f"{u.scheme}://{u.netloc.lower()}{u.path}".rstrip("/")
    if len(s) > 200:
        raise ValueError("service URL is longer than 200 characters")
    return s


def fetch_manifest(base_url: str, allow_private: bool = False, timeout: float = 5) -> dict:
    """Announced URLs come from anyone, so by default hosts resolving to
    internal addresses are refused (checked before connecting)."""
    import urllib.request
    url = base_url + MANIFEST_PATH
    if not allow_private:
        host = urllib.parse.urlsplit(url).hostname or ""
        for info in socket.getaddrinfo(host, None):
            if not ipaddress.ip_address(info[4][0].split("%")[0]).is_global:
                raise ValueError("refusing to fetch from a private or internal address")
    opener = urllib.request.build_opener(_NoRedirect)
    with opener.open(urllib.request.Request(url), timeout=timeout) as r:
        if r.status != 200:
            raise ValueError(f"manifest: HTTP {r.status}")
        data = r.read(64 * 1024 + 1)
    if len(data) > 64 * 1024:
        raise ValueError("manifest too large")
    return json.loads(data)


def find_services(client: AetherClient, query: str = "", max_price: Optional[str] = None,
                  allow_private: bool = False, reputation: bool = True, trusted: Iterable[str] = (),
                  window_blocks: int = DEFAULT_WINDOW) -> List[Service]:
    """Verified paid services from the on-chain directory, newest first. With
    reputation (one more scan per service), each carries what the chain says
    about it; trusted are the accounts whose ratings you trust."""
    current = {}
    directory_payments = client.incoming_payments(DIRECTORY_ADDRESS)
    for p in directory_payments:
        if p.code != 0 or p.amount_uaeth < 1:
            continue
        if p.memo.startswith(DELIST_PREFIX):
            raw, delist = p.memo[len(DELIST_PREFIX):], True
        elif p.memo.startswith(ANNOUNCE_PREFIX):
            raw, delist = p.memo[len(ANNOUNCE_PREFIX):], False
        else:
            continue
        try:
            url = normalize_url(raw)
        except ValueError:
            continue
        k = (p.sender, url)
        if delist:
            current.pop(k, None)
        else:
            current[k] = (url, p.sender, p.height)
    maximum = parse_amount(max_price) if max_price else None
    words = query.lower().split()
    out = []
    for url, announcer, height in current.values():
        try:
            m = fetch_manifest(url, allow_private)
            if m.get("x402Version") != 1 or m.get("network") != client.chain_id or m.get("payTo") != announcer:
                continue
            price = parse_uaeth(m.get("price", ""))
        except Exception:
            continue
        if maximum is not None and price > maximum:
            continue
        hay = f"{m.get('name', '')} {m.get('description', '')} {url}".lower()
        if all(w in hay for w in words):
            out.append(Service(url, announcer, height, m))
    out.sort(key=lambda s: -s.height)
    if reputation and out:
        _assess(client, out, directory_payments, set(trusted), window_blocks)
    return out


def _assess(client, services, directory_payments, trusted, window):
    since = max(1, client.latest_height() - window)
    latest = {}
    for p in directory_payments:
        if p.code != 0 or p.amount_uaeth < 1 or p.height < since or not p.memo.startswith(RATE_PREFIX):
            continue
        rest = p.memo[len(RATE_PREFIX):]
        if len(rest) < 3 or rest[1] != ":" or rest[0] not in "12345":
            continue
        try:
            url = normalize_url(rest[2:])
        except ValueError:
            continue
        latest[(p.sender, url)] = Rating(p.sender, url, int(rest[0]), p.height, p.hash)
    for s in services:
        pay_to = s.manifest["payTo"]
        try:
            paid = client.incoming_payments(pay_to, since)
        except Exception:
            continue
        first_paid, payments, volume = {}, 0, 0
        for p in paid:
            if p.code != 0 or not p.sender or p.sender == pay_to or p.height < since:
                continue
            payments += 1
            volume += p.amount_uaeth
            if p.sender not in first_paid or p.height < first_paid[p.sender]:
                first_paid[p.sender] = p.height
        raters = [r for r in latest.values()
                  if r.url == s.url and r.rater != pay_to and first_paid.get(r.rater, float("inf")) <= r.height]
        s.reputation = Reputation(window, payments, len(first_paid), volume, _summarize(raters),
                                  _summarize(raters, lambda a: a in trusted), raters)
