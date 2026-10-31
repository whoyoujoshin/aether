// One hashing thread. It hashes header bytes with nonces start, start+step,
// ... and reports its count about every 400 ms and any nonce whose scrypt
// hash is below the target. Hashing runs only here, on this computer.
import { belowTarget, powHash, setNonce } from "./chain";

export type ToWorker =
  | { type: "work"; id: number; bytes: Uint8Array; nonceAt: number; target: Uint8Array; start: number; step: number }
  | { type: "stop" };

export type FromWorker = { type: "hashes"; id: number; n: number } | { type: "found"; id: number; nonce: string; n: number };

let job: Extract<ToWorker, { type: "work" }> | null = null;
let nonce = 0n;
let timer: ReturnType<typeof setTimeout> | undefined;

function run(): void {
  timer = undefined;
  const j = job;
  if (!j) return;
  const until = performance.now() + 400;
  let n = 0;
  const step = BigInt(j.step);
  while (performance.now() < until) {
    setNonce(j.bytes, j.nonceAt, nonce);
    const hash = powHash(j.bytes);
    n++;
    if (belowTarget(hash, j.target)) {
      self.postMessage({ type: "found", id: j.id, nonce: nonce.toString(), n } satisfies FromWorker);
      n = 0; // and keep going: the page hands over fresh work
    }
    nonce += step;
  }
  if (n) self.postMessage({ type: "hashes", id: j.id, n } satisfies FromWorker);
  timer = setTimeout(run, 0); // let messages (new work, stop) in
}

self.onmessage = (e: MessageEvent<ToWorker>) => {
  const m = e.data;
  if (m.type === "stop") {
    job = null;
    if (timer) clearTimeout(timer);
    timer = undefined;
    return;
  }
  job = m;
  nonce = BigInt(m.start);
  if (!timer) timer = setTimeout(run, 0);
};
