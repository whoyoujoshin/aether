// Minimal protobuf encoding for the handful of Cosmos messages this
// client needs. Fields are written in field-number order and zero values
// are omitted, exactly as the chain's Go (gogoproto) encoder does -- the
// signature covers these bytes, so they must match.

export class Writer {
  private parts: number[] = [];

  private varint(v: bigint): void {
    let n = BigInt.asUintN(64, v);
    while (n >= 0x80n) {
      this.parts.push(Number((n & 0x7fn) | 0x80n));
      n >>= 7n;
    }
    this.parts.push(Number(n));
  }

  private tag(field: number, wire: number): void {
    this.varint(BigInt((field << 3) | wire));
  }

  uint64(field: number, v: bigint | number): this {
    const n = BigInt(v);
    if (n !== 0n) {
      this.tag(field, 0);
      this.varint(n);
    }
    return this;
  }

  bytes(field: number, v: Uint8Array): this {
    if (v.length > 0) {
      this.tag(field, 2);
      this.varint(BigInt(v.length));
      for (const b of v) this.parts.push(b);
    }
    return this;
  }

  string(field: number, v: string): this {
    return this.bytes(field, new TextEncoder().encode(v));
  }

  /** Embedded message; written even when empty (a present sub-message). */
  message(field: number, v: Uint8Array): this {
    this.tag(field, 2);
    this.varint(BigInt(v.length));
    for (const b of v) this.parts.push(b);
    return this;
  }

  finish(): Uint8Array {
    return Uint8Array.from(this.parts);
  }
}

export type Field = { field: number; wire: number; varint?: bigint; bytes?: Uint8Array };

/** Reads every field of one message, in order. */
export function readFields(buf: Uint8Array): Field[] {
  const out: Field[] = [];
  let i = 0;
  const varint = (): bigint => {
    let shift = 0n;
    let result = 0n;
    for (;;) {
      if (i >= buf.length) throw new Error("protobuf: truncated varint");
      const b = buf[i++];
      result |= BigInt(b & 0x7f) << shift;
      if ((b & 0x80) === 0) return result;
      shift += 7n;
      if (shift > 63n) throw new Error("protobuf: varint too long");
    }
  };
  while (i < buf.length) {
    const key = Number(varint());
    const field = key >> 3;
    const wire = key & 7;
    if (wire === 0) out.push({ field, wire, varint: varint() });
    else if (wire === 2) {
      const len = Number(varint());
      if (i + len > buf.length) throw new Error("protobuf: truncated field");
      out.push({ field, wire, bytes: buf.subarray(i, i + len) });
      i += len;
    } else if (wire === 1) i += 8;
    else if (wire === 5) i += 4;
    else throw new Error(`protobuf: unsupported wire type ${wire}`);
  }
  return out;
}

export const text = (b?: Uint8Array): string => (b ? new TextDecoder().decode(b) : "");
export const first = (fields: Field[], n: number): Field | undefined => fields.find((f) => f.field === n);
