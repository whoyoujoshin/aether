"""Bech32 (BIP-173), as Cosmos addresses use it."""

CHARSET = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
_GEN = (0x3B6A57B2, 0x26508E6D, 0x1EA119FA, 0x3D4233DD, 0x2A1462B3)


def _polymod(values):
    chk = 1
    for v in values:
        top = chk >> 25
        chk = (chk & 0x1FFFFFF) << 5 ^ v
        for i in range(5):
            chk ^= _GEN[i] if (top >> i) & 1 else 0
    return chk


def _hrp_expand(hrp):
    return [ord(c) >> 5 for c in hrp] + [0] + [ord(c) & 31 for c in hrp]


def _convertbits(data, frombits, tobits, pad):
    acc = bits = 0
    out = []
    maxv = (1 << tobits) - 1
    for value in data:
        if value < 0 or value >> frombits:
            raise ValueError("bech32: invalid value")
        acc = (acc << frombits) | value
        bits += frombits
        while bits >= tobits:
            bits -= tobits
            out.append((acc >> bits) & maxv)
    if pad:
        if bits:
            out.append((acc << (tobits - bits)) & maxv)
    elif bits >= frombits or ((acc << (tobits - bits)) & maxv):
        raise ValueError("bech32: invalid padding")
    return out


def encode(hrp: str, data: bytes) -> str:
    words = _convertbits(data, 8, 5, True)
    values = _hrp_expand(hrp) + words
    polymod = _polymod(values + [0] * 6) ^ 1
    checksum = [(polymod >> 5 * (5 - i)) & 31 for i in range(6)]
    return hrp + "1" + "".join(CHARSET[d] for d in words + checksum)


def decode(s: str):
    """Returns (hrp, data bytes); raises ValueError if invalid."""
    if s.lower() != s and s.upper() != s:
        raise ValueError("bech32: mixed case")
    s = s.lower()
    pos = s.rfind("1")
    if pos < 1 or pos + 7 > len(s):
        raise ValueError("bech32: bad separator")
    hrp, rest = s[:pos], s[pos + 1:]
    try:
        values = [CHARSET.index(c) for c in rest]
    except ValueError:
        raise ValueError("bech32: invalid character") from None
    if _polymod(_hrp_expand(hrp) + values) != 1:
        raise ValueError("bech32: bad checksum")
    return hrp, bytes(_convertbits(values[:-6], 5, 8, False))
