// The browser miner: hashing threads (worker.ts) on the work the explorer
// serves (/api/mine/work, one cached read for every miner), and each find
// signed here and sent through /api/mine/submit, which relays only mining
// submissions. The chain takes one native submission a block, so a find
// can still lose to another miner's in the same block.
import { Key, addressBytes, buildTx, headerBytes, headerFor, msgSubmitPoW, nonceOffset, type MiningHeader, type Work } from "./chain";
import type { FromWorker, ToWorker } from "./worker";

export type SubmissionStatus = "waiting" | "sending" | "pending" | "accepted" | "refused" | "dropped";

export interface Submission {
  hash?: string;
  height: number;
  nonce: string;
  at: number;
  status: SubmissionStatus;
  reason?: string;
}

export interface MinerState {
  running: boolean;
  threads: number;
  hashes: number; // since start
  rate: number; // hashes a second, last ~10 s
  work?: Work;
  finds: number;
  submissions: Submission[];
  error?: string;
}

export interface MineAccount {
  address: string;
  exists: boolean;
  accountNumber?: string;
  sequence?: string;
  balance: string;
}

const WORK_POLL_MS = 5000;
const GAS_LIMIT = 400_000;

export async function fetchWork(): Promise<Work> {
  const res = await fetch("/api/mine/work");
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body.error || `the explorer answered ${res.status}`);
  return body as Work;
}

export async function fetchAccount(address: string): Promise<MineAccount> {
  const res = await fetch(`/api/mine/account?addr=${encodeURIComponent(address)}`);
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body.error || `the explorer answered ${res.status}`);
  return body as MineAccount;
}

async function submitTx(txBytes: Uint8Array): Promise<{ hash: string; code: number; log?: string }> {
  let bin = "";
  for (const b of txBytes) bin += String.fromCharCode(b);
  const res = await fetch("/api/mine/submit", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ tx: btoa(bin) }),
  });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body.error || `the explorer answered ${res.status}`);
  return body;
}

/** The block's verdict on a sent submission, or undefined while it isn't in one. */
async function txResult(hash: string): Promise<{ code: number; rawLog: string } | undefined> {
  const res = await fetch(`/api/tx?hash=${encodeURIComponent(hash)}`);
  if (res.status === 404) return undefined;
  if (!res.ok) return undefined;
  return res.json();
}

/** Why the chain refused a submission, in a few words. */
export function refusalReason(log: string): string {
  if (/already been accepted at height/i.test(log)) return "another miner's find took this block";
  if (/more than \d+ blocks behind/i.test(log)) return "too late: the block it was mined on is too old";
  if (/exact mining header has already been accepted/i.test(log)) return "already submitted";
  if (/below required difficulty/i.test(log)) return "difficulty went up before it landed";
  if (/sequence/i.test(log)) return "account sequence out of step; it will retry";
  return log.length > 140 ? log.slice(0, 140) + "…" : log;
}

const stale = (cur: Work, next: Work): boolean =>
  next.height - cur.height >= Math.floor(next.recencyWindow / 2) || BigInt(next.difficulty) < BigInt(cur.difficulty);

export class Miner {
  private workers: Worker[] = [];
  private jobId = 0;
  private header?: MiningHeader;
  private jobWork?: Work;
  private poll?: ReturnType<typeof setInterval>;
  private samples: { t: number; n: number }[] = [];
  private nextSeq?: bigint;
  // The chain takes one native submission a block, so one is sent at a
  // time; a find made meanwhile waits, and a newer one replaces it (a find
  // stays good for half the recency window, so waiting loses nothing).
  private queued?: { header: MiningHeader; sub: Submission };
  private sending = false;
  private readonly miner: Uint8Array;
  readonly state: MinerState = { running: false, threads: 0, hashes: 0, rate: 0, finds: 0, submissions: [] };

  constructor(private readonly key: Key, private readonly onChange: (s: MinerState) => void) {
    this.miner = addressBytes(key.address);
  }

  get address(): string {
    return this.key.address;
  }

  private changed(): void {
    this.onChange({ ...this.state, submissions: [...this.state.submissions] });
  }

  async start(threads: number): Promise<void> {
    this.stop();
    this.state.running = true;
    this.state.threads = threads;
    this.state.hashes = 0;
    this.state.rate = 0;
    this.state.error = undefined;
    this.samples = [{ t: performance.now(), n: 0 }];
    for (let i = 0; i < threads; i++) {
      const w = new Worker(new URL("./worker.ts", import.meta.url), { type: "module" });
      w.onmessage = (e: MessageEvent<FromWorker>) => this.onWorker(e.data);
      this.workers.push(w);
    }
    this.changed();
    await this.refresh(true);
    this.poll = setInterval(() => void this.refresh(false), WORK_POLL_MS);
  }

  stop(): void {
    if (this.poll) clearInterval(this.poll);
    this.poll = undefined;
    for (const w of this.workers) {
      w.postMessage({ type: "stop" } satisfies ToWorker);
      w.terminate();
    }
    this.workers = [];
    this.jobWork = undefined;
    this.header = undefined;
    if (this.state.running) {
      this.state.running = false;
      this.state.rate = 0;
      this.changed();
    }
  }

  /** Reads the work and hands the threads a fresh header when it's due. */
  private async refresh(force: boolean): Promise<void> {
    let work: Work;
    try {
      work = await fetchWork();
    } catch (e) {
      this.state.error = `Can't read the chain: ${e instanceof Error ? e.message : String(e)}`;
      this.changed();
      return;
    }
    if (!this.state.running) return;
    this.state.work = work;
    this.state.error = undefined;
    if (force || !this.jobWork || stale(this.jobWork, work)) this.dispatch(work);
    this.changed();
  }

  private dispatch(work: Work): void {
    const header = headerFor(work, this.miner, Math.floor(Date.now() / 1000));
    const bytes = headerBytes(header);
    const target = hexBytes(work.target);
    this.jobId++;
    this.jobWork = work;
    this.header = header;
    this.workers.forEach((w, i) =>
      w.postMessage({
        type: "work", id: this.jobId, bytes: bytes.slice(), nonceAt: nonceOffset(header), target, start: i, step: this.workers.length,
      } satisfies ToWorker),
    );
  }

  private onWorker(m: FromWorker): void {
    this.count(m.n);
    if (m.type !== "found" || m.id !== this.jobId || !this.header) return;
    this.state.finds++;
    const found = { ...this.header, nonce: BigInt(m.nonce) };
    // Fresh header right away: this one is spent, and the chain takes one a block.
    this.jobWork = undefined;
    void this.refresh(true);
    this.enqueue(found);
  }

  private enqueue(h: MiningHeader): void {
    const sub: Submission = { height: Number(h.height), nonce: h.nonce.toString(), at: Date.now(), status: "waiting" };
    if (this.queued) {
      this.queued.sub.status = "dropped";
      this.queued.sub.reason = "a newer find replaced it while another was on its way";
    }
    this.queued = { header: h, sub };
    this.state.submissions.unshift(sub);
    this.state.submissions.splice(20);
    this.changed();
    void this.pump();
  }

  private async pump(): Promise<void> {
    if (this.sending) return;
    this.sending = true;
    try {
      while (this.queued) {
        const { header, sub } = this.queued;
        this.queued = undefined;
        if (await this.submit(header, sub)) await this.follow(sub);
      }
    } finally {
      this.sending = false;
    }
  }

  private count(n: number): void {
    this.state.hashes += n;
    const now = performance.now();
    const last = this.samples[this.samples.length - 1];
    this.samples.push({ t: now, n: (last?.n ?? 0) + n });
    while (this.samples.length > 2 && now - this.samples[0].t > 10_000) this.samples.shift();
    const first = this.samples[0];
    const secs = (now - first.t) / 1000;
    if (secs > 0.5) this.state.rate = (this.samples[this.samples.length - 1].n - first.n) / secs;
    this.changed();
  }

  /** Signs and sends a find; true once it's in the mempool. */
  private async submit(h: MiningHeader, sub: Submission): Promise<boolean> {
    sub.status = "sending";
    this.changed();
    try {
      for (let attempt = 0; attempt < 2; attempt++) {
        const acct = await fetchAccount(this.key.address);
        if (!acct.exists) throw new Error("the account doesn't exist yet: get the faucet's first drip");
        const chainSeq = BigInt(acct.sequence ?? "0");
        const seq = this.nextSeq !== undefined && this.nextSeq > chainSeq ? this.nextSeq : chainSeq;
        const signed = buildTx(this.key, [msgSubmitPoW(this.key.address, h)], {
          chainId: this.state.work?.chainId ?? "aether-testnet-1", accountNumber: BigInt(acct.accountNumber ?? "0"), sequence: seq, gasLimit: GAS_LIMIT,
        });
        const r = await submitTx(signed.txBytes);
        if (r.code === 0) {
          this.nextSeq = seq + 1n;
          sub.hash = r.hash;
          sub.status = "pending";
          this.changed();
          return true;
        }
        if (/sequence/i.test(r.log ?? "") && attempt === 0) {
          this.nextSeq = undefined;
          continue;
        }
        sub.status = "refused";
        sub.reason = refusalReason(r.log ?? `code ${r.code}`);
        this.changed();
        return false;
      }
    } catch (e) {
      sub.status = "refused";
      sub.reason = e instanceof Error ? e.message : String(e);
      this.changed();
    }
    return false;
  }

  /** Waits for the block that carries a sent submission, about a minute at most. */
  private async follow(sub: Submission): Promise<void> {
    for (let i = 0; i < 30 && sub.hash; i++) {
      await new Promise((r) => setTimeout(r, 3000));
      const res = await txResult(sub.hash).catch(() => undefined);
      if (!res) continue;
      sub.status = res.code === 0 ? "accepted" : "refused";
      if (res.code !== 0) sub.reason = refusalReason(res.rawLog);
      this.changed();
      return;
    }
    sub.status = "refused";
    sub.reason = "not in a block after a minute";
    this.changed();
  }
}

function hexBytes(hex: string): Uint8Array {
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i++) out[i] = parseInt(hex.slice(2 * i, 2 * i + 2), 16);
  return out;
}
