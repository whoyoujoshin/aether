import re
import time
from dataclasses import dataclass, field
from typing import List, Optional

from .amount import DENOM, parse_amount
from .keys import Key, is_address
from .proto import Writer, first, read_fields
from .rpc import Rpc, RpcError
from .tx import MSG_SEND_TYPE_URL, SEND_AUTHORIZATION_TYPE_URL, SignedTx, build_send, build_tx, memo_of


@dataclass
class Transfer:
    sender: str
    recipient: str
    amount_uaeth: int


@dataclass
class TransactionInfo:
    hash: str
    status: str  # pending | confirmed | failed
    height: Optional[int] = None
    code: Optional[int] = None
    codespace: str = ""
    log: str = ""
    memo: str = ""  # set by the sender: untrusted
    transfers: List[Transfer] = field(default_factory=list)


@dataclass
class IncomingPayment:
    hash: str
    height: int
    code: int
    sender: str
    amount_uaeth: int  # everything this transaction moved to the address
    memo: str  # set by the sender: untrusted


@dataclass
class SendResult:
    hash: str
    status: str  # "pending" (accepted, not in a block yet), "failed", or "confirmed" (re-sent bytes already in a block)
    code: int
    log: str
    signed: SignedTx  # re-broadcast these exact bytes to retry without paying twice


def transfers(events) -> List[Transfer]:
    out = []
    for e in events:
        if e.get("type") != "transfer":
            continue
        a = {x["key"]: x["value"] for x in e.get("attributes", [])}
        for part in (a.get("amount") or "").split(","):
            m = re.fullmatch(r"([0-9]+)uaeth", part.strip())
            if m:
                out.append(Transfer(a.get("sender", ""), a.get("recipient", ""), int(m.group(1))))
    return out


@dataclass
class SendGrant:
    """An x/authz permission to send from one account, as the chain stores it."""
    unlimited: bool  # a GenericAuthorization; otherwise spend_limit_uaeth is what's left
    spend_limit_uaeth: int
    allow_list: List[str]  # if non-empty, the only recipients allowed
    expiration: Optional[int] = None  # Unix seconds; None if it never expires


_GENERIC_AUTHORIZATION_TYPE_URL = "/cosmos.authz.v1beta1.GenericAuthorization"


def decode_send_grant(resp: bytes) -> Optional[SendGrant]:
    """Decodes a Query/Grants response for MsgSend; None if it holds none."""
    grant = first(resp, 1)
    if not grant:
        return None
    auth = first(grant, 1)
    if not auth:
        return None
    type_url = first(auth, 1, b"").decode()
    value = first(auth, 2, b"")
    ts = first(grant, 2)
    expiration = first(ts, 1, 0) if ts is not None else None
    if type_url == _GENERIC_AUTHORIZATION_TYPE_URL:
        return SendGrant(True, 0, [], expiration)
    if type_url != SEND_AUTHORIZATION_TYPE_URL:
        raise ValueError(f"unsupported authorization type {type_url}")
    limit, allow = 0, []
    for f, _, v in read_fields(value):
        if f == 1 and first(v, 1, b"").decode() == DENOM:
            limit += int(first(v, 2, b"0").decode() or "0")
        elif f == 2:
            allow.append(v.decode())
    return SendGrant(False, limit, allow, expiration)


class AetherClient:
    def __init__(self, rpc: str, chain_id: str = "aether-testnet-1"):
        self.rpc = Rpc(rpc)
        self.chain_id = chain_id
        # Sequences used but maybe not reported by the chain yet (a second
        # send within one block would otherwise reuse one).
        self._next_seq = {}

    def latest_height(self) -> int:
        return self.rpc.latest_height()

    def balance(self, address: str) -> int:
        """Balance in uaeth."""
        resp = self.rpc.abci_query("/cosmos.bank.v1beta1.Query/Balance", Writer().string(1, address).string(2, DENOM).finish())
        coin = first(resp, 1, b"")
        return int(first(coin, 2, b"0").decode() or "0") if coin else 0

    def account_info(self, address: str):
        """(account_number, sequence), or None if the account doesn't exist yet."""
        try:
            resp = self.rpc.abci_query("/cosmos.auth.v1beta1.Query/AccountInfo", Writer().string(1, address).finish())
        except RpcError as e:
            if "not found" in str(e).lower():
                return None
            raise
        info = first(resp, 1, b"")
        return first(info, 3, 0), first(info, 4, 0)

    def send(self, key: Key, to: str, amount: str, memo: str = "", gas_limit: Optional[int] = None) -> SendResult:
        """Signs and broadcasts a payment; `amount` carries its unit ("1.5 AETH").
        Returns once the node accepts it -- call wait_for_transaction to confirm.
        To retry after an error without paying twice, rebroadcast(result.signed)."""
        if not is_address(to):
            raise ValueError(f'invalid recipient address "{to}"')
        amount_uaeth = parse_amount(amount)
        if len(memo) > 256:
            raise ValueError("memo is limited to 256 characters")
        info = self.account_info(key.address)
        if info is None:
            raise ValueError(f"account {key.address} doesn't exist on chain yet: fund it first")
        account_number, chain_seq = info
        sequence = max(chain_seq, self._next_seq.get(key.address, 0))
        signed = build_send(key, chain_id=self.chain_id, account_number=account_number, sequence=sequence, to=to,
                            amount_uaeth=amount_uaeth, memo=memo, **({"gas_limit": gas_limit} if gas_limit else {}))
        r = self.rebroadcast(signed)
        if r.status != "failed":
            self._next_seq[key.address] = sequence + 1
        return r

    def send_grant(self, granter: str, grantee: str) -> Optional["SendGrant"]:
        """The permission granter gave grantee to send from its account (x/authz), or None if
        there's none (never granted, revoked, used up or expired)."""
        req = Writer().string(1, granter).string(2, grantee).string(3, MSG_SEND_TYPE_URL).finish()
        try:
            resp = self.rpc.abci_query("/cosmos.authz.v1beta1.Query/Grants", req)
        except RpcError as e:
            if "not found" in str(e).lower():
                return None
            raise
        return decode_send_grant(resp)

    def sign_and_broadcast(self, key: Key, msgs, memo: str = "", gas_limit: Optional[int] = None) -> SendResult:
        """Signs and broadcasts a transaction carrying msgs from key's account. Like send, for any messages."""
        info = self.account_info(key.address)
        if info is None:
            raise ValueError(f"account {key.address} doesn't exist on chain yet: fund it first")
        account_number, chain_seq = info
        sequence = max(chain_seq, self._next_seq.get(key.address, 0))
        signed = build_tx(key, msgs, chain_id=self.chain_id, account_number=account_number, sequence=sequence, memo=memo,
                          **({"gas_limit": gas_limit} if gas_limit else {}))
        r = self.rebroadcast(signed)
        if r.status != "failed":
            self._next_seq[key.address] = sequence + 1
        return r

    def rebroadcast(self, signed: SignedTx) -> SendResult:
        """(Re)broadcasts already-signed bytes: the chain includes them at most once.
        When the node already knows them (cache, mempool or a block), reports
        where they actually stand instead of an error."""
        try:
            r = self.rpc.broadcast_sync(signed.tx_bytes)
        except RpcError as e:
            if "already exists in cache" not in str(e).lower():
                raise
            return self._known(signed, 19, str(e))
        code, codespace, log = r.get("code", 0), r.get("codespace", ""), r.get("log", "")
        if code == 0:
            return SendResult(signed.hash, "pending", 0, log, signed)
        # 19: already in the mempool; 32: its sequence is used -- possibly by these bytes, in a block.
        if codespace == "sdk" and code in (19, 32):
            return self._known(signed, code, log)
        return SendResult(signed.hash, "failed", code, log, signed)

    def _known(self, signed: SignedTx, code: int, log: str) -> SendResult:
        t = self.get_transaction(signed.hash)
        if t.status == "confirmed":
            return SendResult(signed.hash, "confirmed", 0, t.log, signed)
        if t.status == "failed":
            return SendResult(signed.hash, "failed", t.code or code, t.log or log, signed)
        return SendResult(signed.hash, "pending" if code == 19 else "failed", code, log, signed)

    def get_transaction(self, tx_hash: str) -> TransactionInfo:
        t = self.rpc.tx(tx_hash)
        if t is None:
            return TransactionInfo(tx_hash.upper(), "pending")
        return TransactionInfo(t.hash, "confirmed" if t.code == 0 else "failed", t.height, t.code, t.codespace, t.log,
                               memo_of(t.tx), transfers(t.events))

    def wait_for_transaction(self, tx_hash: str, timeout: float = 90, poll: float = 3) -> TransactionInfo:
        deadline = time.monotonic() + timeout
        while True:
            info = self.get_transaction(tx_hash)
            if info.status != "pending" or time.monotonic() >= deadline:
                return info
            time.sleep(min(poll, max(0.0, deadline - time.monotonic())))

    def incoming_payments(self, address: str, since_height: int = 1, max_results: int = 5000) -> List[IncomingPayment]:
        """Every payment to address at or above since_height, oldest first (reads all pages)."""
        if not is_address(address):
            raise ValueError(f'invalid address "{address}"')  # it goes into a query string
        query = f"transfer.recipient='{address}' AND tx.height>={max(1, since_height)}"
        out, page = [], 1
        while True:
            txs, total = self.rpc.tx_search(query, page, 100)
            for t in txs:
                mine = [x for x in transfers(t.events) if x.recipient == address]
                if mine:
                    out.append(IncomingPayment(t.hash, t.height, t.code, mine[0].sender, sum(x.amount_uaeth for x in mine), memo_of(t.tx)))
            if len(out) >= max_results:
                raise ValueError(f"more than {max_results} incoming transactions since height {since_height}: pass a later since_height")
            if len(txs) < 100 or page * 100 >= total:
                return out
            page += 1

    def wait_for_payment(self, address: str, memo: str, min_amount: str, since_height: int = 1,
                         timeout: float = 90, poll: float = 3) -> Optional[IncomingPayment]:
        """A confirmed payment to address with exactly memo and at least min_amount, or None on timeout."""
        minimum = parse_amount(min_amount)
        deadline = time.monotonic() + timeout
        while True:
            for p in self.incoming_payments(address, since_height):
                if p.code == 0 and p.memo == memo and p.amount_uaeth >= minimum:
                    return p
            if time.monotonic() >= deadline:
                return None
            time.sleep(min(poll, max(0.0, deadline - time.monotonic())))
