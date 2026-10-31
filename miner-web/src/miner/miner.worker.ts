import { aetherScrypt } from "./scrypt.mjs";

export type MinerTick = {
  hashes: number;
  shares: number;
  ms: number;
  last: string;
};

let running = false;
let header = new Uint8Array(40);
let nonce = 0;

function hex(bytes: Uint8Array): string {
  let out = "";
  for (let i = 0; i < bytes.length; i++) out += (bytes[i] ?? 0).toString(16).padStart(2, "0");
  return out;
}

function loop(): void {
  const started = performance.now();
  let hashes = 0;
  let shares = 0;
  let last = "";
  const view = new DataView(header.buffer, header.byteOffset, header.byteLength);
  const nonceAt = header.length - 4;
  while (running && performance.now() - started < 280) {
    view.setUint32(nonceAt, nonce >>> 0, true);
    nonce = (nonce + 1) >>> 0;
    const hash = aetherScrypt(header);
    hashes += 1;
    last = hex(hash);
    if (hash[0] === 0) shares += 1;
  }
  const tick: MinerTick = { hashes, shares, ms: performance.now() - started, last };
  self.postMessage(tick);
  if (running) setTimeout(loop, 12);
}

self.onmessage = (event: MessageEvent<{ type: string; header?: number[]; nonce?: number }>) => {
  const data = event.data;
  if (data.type === "stop") {
    running = false;
    return;
  }
  if (data.type === "start" && data.header) {
    running = true;
    header = new Uint8Array(data.header);
    nonce = data.nonce ?? 0;
    loop();
  }
};
