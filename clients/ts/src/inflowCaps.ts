// x/inflowcap's allowlist (docs/IBC-INFLOW-CAP.md): the tokens other
// chains may send onto Aether, each over one channel and up to a cap on
// how much of it there may be on Aether. A transfer of anything else, or
// over the room left, is refused, and the sending chain refunds it.

import { readFields, first, text } from "./proto.js";

export const INFLOW_CAPS_PATH = "/aether.inflowcap.v1.Query/Caps";

export interface InflowCap {
  /** Aether's end of the channel, e.g. "channel-2". */
  channel: string;
  /** The token as the other chain sends it. */
  denom: string;
  /** What it arrives as on Aether (ibc/...). */
  voucherDenom: string;
  /** Base units. */
  cap: bigint;
  supply: bigint;
  /** Sent back from Aether, not yet settled: holds room until it is. 0 from nodes before the field existed. */
  inFlight: bigint;
  /** cap - supply - inFlight, or 0. */
  room: bigint;
}

/** active is false where the chain has no x/inflowcap yet: every token comes in uncapped. */
export interface InflowCaps {
  active: boolean;
  caps: InflowCap[];
}

const int = (b?: Uint8Array): bigint => BigInt(text(b) || "0");

/** Decodes a QueryCapsResponse. */
export function decodeInflowCaps(bytes: Uint8Array): InflowCap[] {
  return readFields(bytes)
    .filter((f) => f.field === 1 && f.bytes)
    .map((f) => {
      const status = readFields(f.bytes!);
      const entry = readFields(first(status, 1)?.bytes ?? new Uint8Array());
      return {
        channel: text(first(entry, 1)?.bytes),
        denom: text(first(entry, 2)?.bytes),
        cap: int(first(entry, 3)?.bytes),
        voucherDenom: text(first(status, 2)?.bytes),
        supply: int(first(status, 3)?.bytes),
        room: int(first(status, 4)?.bytes),
        inFlight: int(first(status, 5)?.bytes),
      };
    });
}

/**
 * How much more of voucher may come onto Aether: limited is false when
 * nothing caps it, listed false when it isn't on the allowlist (every
 * transfer of it is refused).
 */
export function inflowRoom(caps: InflowCaps, voucher: string): { room?: bigint; limited: boolean; listed: boolean } {
  if (!caps.active) return { limited: false, listed: true };
  const c = caps.caps.find((x) => x.voucherDenom === voucher);
  return c ? { room: c.room, limited: true, listed: true } : { room: 0n, limited: true, listed: false };
}
