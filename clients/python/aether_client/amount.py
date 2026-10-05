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
    origin: str  # "Aether"; for USDC, its issuer and route, like "Noble over transfer/channel-3"


AETH = Asset("AETH", DENOM, DENOM, _DECIMALS, "Aether")


DEFAULT_USDC_BASE_DENOM = "uusdc"  # USDC's denom on Noble, which issues it

_HOP = re.compile(r"[a-zA-Z0-9._+\-#\[\]<>]{2,128}/channel-[0-9]+")
_BASE_DENOM = re.compile(r"[a-zA-Z][a-zA-Z0-9/:._-]{2,127}")


def usdc(channel: str) -> Asset:
    """Noble's USDC as it exists on Aether after crossing channel, the Aether end of Aether's own
    channel to Noble. It's usdc_at with a single hop and Noble's base denom."""
    if not re.fullmatch(r"channel-[0-9]+", channel or ""):
        raise ValueError(f'USDC channel "{channel}": want Aether\'s end of its channel to Noble, like channel-3')
    return usdc_at(f"transfer/{channel}", DEFAULT_USDC_BASE_DENOM)


def usdc_at(path: str, base_denom: str) -> Asset:
    """The USDC that reaches Aether along path, its ICS-20 denom trace as Aether records it (Aether's
    own hop first), with base_denom its denom on the chain that issues it: "transfer/channel-1/
    transfer/channel-4280" and "uusdc" for Noble's USDC through Osmosis, say. The same token reaching
    Aether any other way has a different denom and isn't interchangeable with it, so it's never
    accepted as USDC. base_denom is case-sensitive."""
    hops = (path or "").split("/")
    if not path or len(hops) % 2:
        raise ValueError(f'USDC path "{path}": want port/channel hops from Aether\'s end, like transfer/channel-1/transfer/channel-4280')
    for i in range(0, len(hops), 2):
        hop = f"{hops[i]}/{hops[i + 1]}"
        if not _HOP.fullmatch(hop):
            raise ValueError(f'USDC path "{path}": hop "{hop}" isn\'t port/channel-N')
    if (base_denom or "").startswith("ibc/") or not _BASE_DENOM.fullmatch(base_denom or ""):
        raise ValueError(f'USDC base denom "{base_denom}": want its denom on the chain that issues it, like uusdc, not an ibc/ hash')
    h = hashlib.sha256(f"{path}/{base_denom}".encode()).hexdigest().upper()
    issuer = "Noble" if base_denom == DEFAULT_USDC_BASE_DENOM else base_denom
    return Asset("USDC", f"ibc/{h}", "uusdc", 6, f"{issuer} over {path}")


def usdc_for(usdc_channel: Optional[str] = None, usdc_path: Optional[str] = None,
             usdc_base_denom: Optional[str] = None, usdc_issuer: Optional[str] = None) -> Optional[Asset]:
    """Which USDC a client accepts: usdc_channel, as shorthand for Noble's USDC over Aether's direct
    channel to Noble, or usdc_path and usdc_base_denom for any other route or issuer (usdc_base_denom
    defaults to uusdc). None of them: no USDC. usdc_issuer names the issuer in the asset's origin,
    which tools label USDC by ("USDC (Injective)"); default Noble for uusdc, else the base denom."""
    u: Optional[Asset] = None
    if usdc_channel and (usdc_path or usdc_base_denom):
        raise ValueError("set either usdc_channel or usdc_path and usdc_base_denom, not both")
    if usdc_channel:
        u = usdc(usdc_channel)
    elif usdc_path:
        u = usdc_at(usdc_path, usdc_base_denom or DEFAULT_USDC_BASE_DENOM)
    elif usdc_base_denom:
        raise ValueError(f'USDC base denom "{usdc_base_denom}" needs usdc_path too')
    if not usdc_issuer:
        return u
    if u is None:
        raise ValueError(f'USDC issuer "{usdc_issuer}" needs usdc_channel or usdc_path too')
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]{0,31}", usdc_issuer):
        raise ValueError(f'USDC issuer "{usdc_issuer}": want one word, like Injective')
    return Asset(u.symbol, u.denom, u.base_unit, u.decimals, f"{usdc_issuer} over {u.origin.split(' over ', 1)[1]}")


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
    """The assets a client accepts: AETH always, and the USDC its setting names, if any (see usdc_for)."""

    def __init__(self, usdc_channel: Optional[str] = None, usdc_path: Optional[str] = None,
                 usdc_base_denom: Optional[str] = None, usdc_issuer: Optional[str] = None):
        self._all: List[Asset] = [AETH]
        u = usdc_for(usdc_channel, usdc_path, usdc_base_denom, usdc_issuer)
        if u:
            self._all.append(u)

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
