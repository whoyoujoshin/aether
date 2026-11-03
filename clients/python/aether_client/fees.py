"""What a transaction pays for gas. Every Aether node asks a minimum price per gas
(minimum-gas-prices; on aether-1 0.0001uaeth, or 0.0005 in the USDC voucher) and refuses a fee
under it; paying more is a tip, which goes first when blocks are full. Same rule as the Go
wallet's FeeFor (shared vectors: clients/testdata/fees.json)."""

import re
from decimal import ROUND_CEILING, Decimal
from typing import List, Optional, Tuple

GasPrice = Tuple[Decimal, str]  # (amount per gas, denom)

_PRICE = re.compile(r"^([0-9]+(?:\.[0-9]+)?)([a-zA-Z][a-zA-Z0-9/:._-]{2,127})$")


def parse_gas_prices(s: str) -> List[GasPrice]:
    """Parses a price list as nodes print it: "0.000100000000000000uaeth,0.0005ibc/..."."""
    out: List[GasPrice] = []
    for part in s.split(","):
        p = part.strip()
        if not p:
            continue
        m = _PRICE.match(p)
        if not m:
            raise ValueError(f'price "{p}" isn\'t an amount followed by a denom')
        out.append((Decimal(m.group(1)), m.group(2)))
    return out


def fee_for(prices: List[GasPrice], denom: str, gas: int, tip: float = 1) -> Optional[Tuple[int, str]]:
    """The fee (amount, denom) a transaction asking gas pays at prices, times tip (at least 1): in
    denom if prices name it, else in uaeth, else in the first denom priced; rounded up. None when
    nothing is priced (no fee)."""
    if not tip >= 1:
        raise ValueError(f"tip {tip}: at least 1")
    positive = [p for p in prices if p[0] > 0]
    price = next((p for p in positive if p[1] == denom), None) or next((p for p in positive if p[1] == "uaeth"), None) \
        or (positive[0] if positive else None)
    if price is None:
        return None
    amount = (price[0] * gas * Decimal(str(tip))).to_integral_value(rounding=ROUND_CEILING)
    return int(amount), price[1]
