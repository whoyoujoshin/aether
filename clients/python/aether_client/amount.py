"""Amounts carry a unit: "1.5 AETH" or "1500000uaeth" (1 AETH = 1,000,000 uaeth).

A bare number is refused rather than guessed at -- mixing the two up is a
million-fold error with real money.
"""

import hashlib
import re
from dataclasses import dataclass
from typing import List, Optional, Tuple

DENOM = "uaeth"
_DECIMALS = 6
_PATTERN = re.compile(r"^\s*([0-9]+)(?:\.([0-9]+))?\s*([A-Za-z]+)\s*$")
_MAX = (1 << 63) - 1


def parse_amount(s: str) -> int:
    """Parses an amount with its unit into uaeth."""
    m = _PATTERN.match(s)
    if not m:
        if re.fullmatch(r"\s*[0-9]+\s*", s):
            raise ValueError(f'amount "{s}" has no unit: write e.g. "1.5 AETH" or "1500000uaeth" (1 AETH = 1,000,000 uaeth)')
        raise ValueError(f'invalid amount "{s}": write e.g. "1.5 AETH" or "1500000uaeth"')
    whole, frac, unit = m.group(1), m.group(2) or "", m.group(3).lower()
    if unit == "aeth":
        if len(frac) > _DECIMALS:
            raise ValueError(f'amount "{s}" has more than {_DECIMALS} decimal places')
        v = int(whole + frac.ljust(_DECIMALS, "0"), 10)
    elif unit == DENOM:
        if frac:
            raise ValueError(f'amount "{s}": uaeth can\'t be fractional')
        v = int(whole, 10)
    else:
        raise ValueError(f'amount "{s}" has unknown unit "{m.group(3)}": use AETH or uaeth')
    if v <= 0:
        raise ValueError(f'amount "{s}" must be greater than zero')
    if v > _MAX:
        raise ValueError(f'amount "{s}" is too large')
    return v


def parse_uaeth(s: str) -> int:
    """A strictly base-10 whole number of uaeth ("1500000"), as wire formats carry it."""
    if not re.fullmatch(r"[0-9]+", s or ""):
        raise ValueError(f'invalid uaeth amount "{s}"')
    return int(s, 10)


def format_aeth(uaeth: int) -> str:
    """1500000 -> "1.5"."""
    sign = "-" if uaeth < 0 else ""
    a = abs(uaeth)
    frac = str(a % 1_000_000).rjust(_DECIMALS, "0").rstrip("0")
    return sign + str(a // 1_000_000) + ("." + frac if frac else "")


@dataclass(frozen=True)
class Asset:
    """A token the client knows by name. Amounts name their asset by unit ("5 USDC", "1.5 AETH",
    "5000000uusdc"), so a USDC amount can never be read as AETH or the other way round."""
    symbol: str  # what people write: "AETH", "USDC"
    denom: str  # the chain's name for it: "uaeth", or "ibc/<hash>" for a token that arrived over IBC
    base_unit: str  # the smallest unit's name, which people may also write: "uaeth", "uusdc"
    decimals: int  # how many base units make one symbol, as a power of ten
    origin: str  # "Aether", or "Noble over transfer/channel-3"


AETH = Asset("AETH", DENOM, DENOM, _DECIMALS, "Aether")


def usdc(channel: str) -> Asset:
    """Noble's USDC as it exists on Aether after crossing channel, the Aether end of Aether's own
    channel to Noble. The same token reaching Aether any other way (through Osmosis, say) has a
    different denom and isn't interchangeable with it, so it's never accepted as USDC."""
    if not re.fullmatch(r"channel-[0-9]+", channel or ""):
        raise ValueError(f'USDC channel "{channel}": want Aether\'s end of its channel to Noble, like channel-3')
    h = hashlib.sha256(f"transfer/{channel}/uusdc".encode()).hexdigest().upper()
    return Asset("USDC", f"ibc/{h}", "uusdc", 6, f"Noble over transfer/{channel}")


def decimal_of(asset: Asset, base: int) -> str:
    """Base units as a decimal amount without trailing zeros or symbol: 1500000 -> "1.5"."""
    sign = "-" if base < 0 else ""
    a = abs(base)
    if asset.decimals == 0:
        return sign + str(a)
    one = 10 ** asset.decimals
    frac = str(a % one).rjust(asset.decimals, "0").rstrip("0")
    return sign + str(a // one) + ("." + frac if frac else "")


def format_amount(asset: Asset, base: int) -> str:
    """Base units with the asset's symbol: 1500000 -> "1.5 USDC"."""
    return f"{decimal_of(asset, base)} {asset.symbol}"


def receipt_amount(amount: int, denom: str = DENOM) -> str:
    """How a signed receipt states an amount: bare uaeth for AETH, else the amount followed by the denom."""
    return str(amount) if denom in ("", DENOM) else f"{amount}{denom}"


class Assets:
    """The assets a client accepts: AETH always, USDC over usdc_channel if given."""

    def __init__(self, usdc_channel: Optional[str] = None):
        self._all: List[Asset] = [AETH]
        if usdc_channel:
            self._all.append(usdc(usdc_channel))

    def list(self) -> List[Asset]:
        """Every asset, AETH first."""
        return list(self._all)

    def by_symbol(self, symbol: str) -> Optional[Asset]:
        return next((a for a in self._all if a.symbol.lower() == symbol.lower()), None)

    def by_denom(self, denom: str) -> Optional[Asset]:
        return next((a for a in self._all if a.denom == denom), None)

    def parse(self, s: str) -> Tuple[Asset, int]:
        """Reads an amount with its unit: a symbol with up to its decimals ("1.5 AETH", "2.25usdc")
        or a whole number of base units ("2250000 uusdc"). A bare number, or a unit it doesn't know,
        is refused. Returns the asset and the amount in its base units."""
        m = _PATTERN.match(s)
        if not m:
            if re.fullmatch(r"\s*[0-9]+(\.[0-9]+)?\s*", s):
                raise ValueError(f'amount "{s}" has no unit: write e.g. "1.5 AETH" or "1500000uaeth"')
            raise ValueError(f'invalid amount "{s}": write e.g. "1.5 AETH" or "1500000uaeth"')
        whole, frac, unit = m.group(1), m.group(2) or "", m.group(3).lower()
        for a in self._all:
            if unit == a.symbol.lower():
                if len(frac) > a.decimals:
                    raise ValueError(f'amount "{s}" has more than {a.decimals} decimal places')
                v = int(whole + frac.ljust(a.decimals, "0"), 10)
            elif unit == a.base_unit.lower():
                if frac:
                    raise ValueError(f'amount "{s}": {a.base_unit} is the smallest unit and can\'t be fractional')
                v = int(whole, 10)
            else:
                continue
            if v <= 0:
                raise ValueError(f'amount "{s}" must be greater than zero')
            if v > _MAX:
                raise ValueError(f'amount "{s}" is too large')
            return a, v
        units = ", ".join(u for a in self._all for u in (a.symbol, a.base_unit))
        raise ValueError(f'amount "{s}" has unknown unit "{m.group(3)}": use {units}')
