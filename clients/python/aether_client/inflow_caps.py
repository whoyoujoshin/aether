"""x/inflowcap's allowlist (docs/IBC-INFLOW-CAP.md): the tokens other chains may send onto Aether,
each over one channel and up to a cap on how much of it there may be on Aether. A transfer of anything
else, or over the room left, is refused, and the sending chain refunds it."""

from dataclasses import dataclass, field
from typing import List, Optional, Tuple

from .proto import first, read_fields

INFLOW_CAPS_PATH = "/aether.inflowcap.v1.Query/Caps"


@dataclass
class InflowCap:
    channel: str  # Aether's end, e.g. "channel-2"
    denom: str  # the token as the other chain sends it
    voucher_denom: str  # what it arrives as on Aether (ibc/...)
    cap: int  # base units
    supply: int
    room: int  # cap - supply, or 0


@dataclass
class InflowCaps:
    """active is False where the chain has no x/inflowcap yet: every token comes in uncapped."""
    active: bool
    caps: List[InflowCap] = field(default_factory=list)

    def room(self, voucher: str) -> Tuple[Optional[int], bool, bool]:
        """(room, limited, listed) for voucher: limited is False when nothing caps it, listed False when
        it isn't on the allowlist (every transfer of it is refused)."""
        if not self.active:
            return None, False, True
        for c in self.caps:
            if c.voucher_denom == voucher:
                return c.room, True, True
        return 0, True, False


def _int(b: bytes) -> int:
    return int(b.decode() or "0")


def decode_inflow_caps(buf: bytes) -> List[InflowCap]:
    """Decodes a QueryCapsResponse."""
    out = []
    for f, _, status in read_fields(buf):
        if f != 1 or not isinstance(status, bytes):
            continue
        entry = first(status, 1, b"")
        out.append(InflowCap(
            channel=first(entry, 1, b"").decode(), denom=first(entry, 2, b"").decode(), cap=_int(first(entry, 3, b"")),
            voucher_denom=first(status, 2, b"").decode(), supply=_int(first(status, 3, b"")), room=_int(first(status, 4, b""))))
    return out
