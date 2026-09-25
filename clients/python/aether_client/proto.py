"""Minimal protobuf encoding for the few Cosmos messages this client needs.

Fields are written in field-number order and zero values omitted, exactly as
the chain's Go encoder does -- signatures cover these bytes.
"""


def _varint(n: int) -> bytes:
    n &= (1 << 64) - 1
    out = bytearray()
    while n >= 0x80:
        out.append((n & 0x7F) | 0x80)
        n >>= 7
    out.append(n)
    return bytes(out)


class Writer:
    def __init__(self):
        self._buf = bytearray()

    def _tag(self, field: int, wire: int):
        self._buf += _varint((field << 3) | wire)

    def uint64(self, field: int, v: int) -> "Writer":
        if v:
            self._tag(field, 0)
            self._buf += _varint(v)
        return self

    def bytes(self, field: int, v: bytes) -> "Writer":
        if v:
            self._tag(field, 2)
            self._buf += _varint(len(v)) + v
        return self

    def string(self, field: int, v: str) -> "Writer":
        return self.bytes(field, v.encode())

    def message(self, field: int, v: bytes) -> "Writer":
        """An embedded message, written even when empty."""
        self._tag(field, 2)
        self._buf += _varint(len(v)) + v
        return self

    def finish(self) -> bytes:
        return bytes(self._buf)


def read_fields(buf: bytes):
    """Yields (field, wire, value) for each field: value is int (varint) or bytes."""
    i = 0

    def varint():
        nonlocal i
        shift = result = 0
        while True:
            if i >= len(buf):
                raise ValueError("protobuf: truncated varint")
            b = buf[i]
            i += 1
            result |= (b & 0x7F) << shift
            if not b & 0x80:
                return result
            shift += 7
            if shift > 63:
                raise ValueError("protobuf: varint too long")

    while i < len(buf):
        key = varint()
        field, wire = key >> 3, key & 7
        if wire == 0:
            yield field, wire, varint()
        elif wire == 2:
            n = varint()
            if i + n > len(buf):
                raise ValueError("protobuf: truncated field")
            yield field, wire, buf[i:i + n]
            i += n
        elif wire == 1:
            i += 8
        elif wire == 5:
            i += 4
        else:
            raise ValueError(f"protobuf: unsupported wire type {wire}")


def first(buf: bytes, field: int, default=None):
    for f, _, v in read_fields(buf):
        if f == field:
            return v
    return default
