"""Amounts carry a unit: "1.5 AETH" or "1500000uaeth" (1 AETH = 1,000,000 uaeth).

A bare number is refused rather than guessed at -- mixing the two up is a
million-fold error with real money.
"""

import re

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
