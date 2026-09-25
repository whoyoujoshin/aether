"""The on-chain service directory: services announce themselves with 1 uaeth to
a keyless address, memo "x402-service:<url>". A listing counts only if the
manifest at that URL names the announcer as payee."""

import hashlib
import ipaddress
import json
import socket
import urllib.parse
from dataclasses import dataclass
from typing import List, Optional

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


@dataclass
class Service:
    url: str
    announcer: str
    height: int
    manifest: dict  # name/description are set by the service: untrusted


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
                  allow_private: bool = False) -> List[Service]:
    """Verified paid services from the on-chain directory, newest first."""
    current = {}
    for p in client.incoming_payments(DIRECTORY_ADDRESS):
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
    return sorted(out, key=lambda s: -s.height)
