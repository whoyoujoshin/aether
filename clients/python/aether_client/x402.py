"""The standard x402 (v2) "exact" scheme on Aether, for the x402 Python SDK's
x402Client (or any client with the same interface)::

    from x402 import x402Client
    from aether_client import AetherClient, Key
    from aether_client.x402 import ExactAetherScheme

    client = x402Client()
    client.register("cosmos:aether-testnet-1", ExactAetherScheme(AetherClient(rpc), key))

The payment is a signed bank send of exactly the amount to payTo, which the
resource server's facilitator checks and broadcasts (see the Go package x402
and cmd/facilitator). It needs nothing from the x402 package at run time:
requirements may be its PaymentRequirements or a plain dict in the wire's
camelCase. Same rules as the TypeScript ExactAetherScheme.
"""

import base64
import re
from typing import Any, Dict, Mapping, Optional

from .client import AetherClient
from .keys import Key, is_address
from .tx import DEFAULT_GAS_LIMIT, build_send

_AMOUNT = re.compile(r"[1-9][0-9]*")
_DENOM = re.compile(r"[a-zA-Z][a-zA-Z0-9/:._-]{2,127}")


def x402_network(chain_id: str) -> str:
    """The CAIP-2 network of an Aether chain: "cosmos:aether-testnet-1" for the testnet."""
    return f"cosmos:{chain_id}"


def _field(req: Any, snake: str, camel: Optional[str] = None) -> Any:
    """A requirements field, from the SDK's model (snake_case attributes) or a wire dict (camelCase keys)."""
    if isinstance(req, Mapping):
        if camel is not None and camel in req:
            return req[camel]
        return req.get(snake)
    return getattr(req, snake, None)


class ExactAetherScheme:
    """The x402 "exact" scheme on Aether, as a client: it pays a 402 by signing a send of exactly what
    the requirement asks, from key's account. The account pays the transaction's fee (zero on the
    testnet) and must exist on chain. Payments from one key go one at a time: each is signed for the
    account's next sequence, so a second one built before the first is in a block is refused by the
    facilitator.

    max_amount caps one payment per asset, in base units ({"uaeth": 1_000_000}); unlisted assets are
    uncapped here, and x402Client's spend controls apply too."""

    scheme = "exact"

    def __init__(self, client: AetherClient, key: Key, gas_limit: int = DEFAULT_GAS_LIMIT,
                 max_amount: Optional[Dict[str, int]] = None):
        self.client = client
        self.key = key
        self.gas_limit = gas_limit
        self.max_amount = dict(max_amount or {})

    def find_default_asset(self, asset: str, network: str) -> Optional[Dict[str, Any]]:
        """USDC, for x402Client's USD spend caps, when the client knows it."""
        if network != x402_network(self.client.chain_id):
            return None
        a = self.client.assets.by_denom(asset)
        if a is None or a.symbol != "USDC":
            return None
        return {"asset": a.denom, "decimals": a.decimals, "symbol": a.symbol}

    def create_payment_payload(self, requirements: Any, context: Any = None) -> Dict[str, Any]:
        """The inner payload, {"transaction": <base64 TxRaw>}, which x402Client wraps into a PaymentPayload."""
        scheme = _field(requirements, "scheme")
        if scheme != self.scheme:
            raise ValueError(f'scheme "{scheme}" isn\'t exact')
        network = str(_field(requirements, "network"))
        want = x402_network(self.client.chain_id)
        if network != want:
            raise ValueError(f'network "{network}": this client pays on {want}')
        amount_s = str(_field(requirements, "amount"))
        if not _AMOUNT.fullmatch(amount_s):
            raise ValueError(f'amount "{amount_s}" isn\'t a positive integer in base units')
        asset = str(_field(requirements, "asset"))
        if not _DENOM.fullmatch(asset):
            raise ValueError(f'asset "{asset}" isn\'t a denom')
        pay_to = str(_field(requirements, "pay_to", "payTo"))
        if not is_address(pay_to):
            raise ValueError(f'payTo "{pay_to}" isn\'t an Aether address')

        amount = int(amount_s)
        cap = self.max_amount.get(asset)
        if cap is not None and amount > cap:
            raise ValueError(f"{amount}{asset} is over this client's limit of {cap}{asset}")
        spend_cap = _field(context, "max_amount_per_payment", "maxAmountPerPayment") if context is not None else None
        if spend_cap is not None and amount > int(spend_cap):
            raise ValueError(f"{amount}{asset} is over the spend limit of {spend_cap}")

        info = self.client.account_info(self.key.address)
        if info is None:
            raise ValueError(f"account {self.key.address} doesn't exist on chain yet: fund it first")
        account_number, sequence = info
        signed = build_send(self.key, chain_id=self.client.chain_id, account_number=account_number, sequence=sequence,
                            to=pay_to, amount_uaeth=amount, denom=asset, gas_limit=self.gas_limit)
        return {"transaction": base64.b64encode(signed.tx_bytes).decode()}
