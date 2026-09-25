"""The node's CometBFT RPC (default port 26657), which every Aether node serves.
Queries go through abci_query with gRPC method paths: no REST gateway needed."""

import base64
import json
import urllib.error
import urllib.request
from dataclasses import dataclass, field


class RpcError(Exception):
    def __init__(self, message: str, code=None):
        super().__init__(message)
        self.code = code


@dataclass
class TxResult:
    hash: str
    height: int
    code: int
    codespace: str
    log: str
    events: list = field(default_factory=list)
    tx: bytes = b""


def _to_tx(r: dict) -> TxResult:
    res = r["tx_result"]
    return TxResult(r["hash"].upper(), int(r["height"]), res.get("code", 0), res.get("codespace", ""),
                    res.get("log", ""), res.get("events") or [], base64.b64decode(r["tx"]))


class Rpc:
    def __init__(self, url: str, timeout: float = 30):
        self.url = url.rstrip("/")
        self.timeout = timeout

    def call(self, method: str, params: dict = None):
        body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": method, "params": params or {}}).encode()
        req = urllib.request.Request(self.url, data=body, headers={"Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                payload = json.load(resp)
        except urllib.error.HTTPError as e:
            if e.code != 500:
                raise RpcError(f"{method}: HTTP {e.code}") from None
            payload = json.loads(e.read() or b"{}")
        if payload.get("error"):
            err = payload["error"]
            raise RpcError(f"{method}: {err.get('data') or err.get('message')}", err.get("code"))
        return payload["result"]

    def latest_height(self) -> int:
        return int(self.call("status")["sync_info"]["latest_block_height"])

    def abci_query(self, path: str, data: bytes) -> bytes:
        r = self.call("abci_query", {"path": path, "data": data.hex()})["response"]
        if r.get("code", 0):
            raise RpcError(f"{path}: {r.get('log')}", r.get("code"))
        return base64.b64decode(r["value"]) if r.get("value") else b""

    def broadcast_sync(self, tx: bytes) -> dict:
        return self.call("broadcast_tx_sync", {"tx": base64.b64encode(tx).decode()})

    def tx(self, tx_hash: str):
        """The transaction in a block, or None if the node has none by that hash."""
        try:
            return _to_tx(self.call("tx", {"hash": base64.b64encode(bytes.fromhex(tx_hash)).decode()}))
        except RpcError as e:
            if "not found" in str(e).lower():
                return None
            raise

    def tx_search(self, query: str, page: int, per_page: int):
        r = self.call("tx_search", {"query": query, "page": str(page), "per_page": str(per_page), "order_by": "asc"})
        return [_to_tx(t) for t in r["txs"]], int(r["total_count"])
