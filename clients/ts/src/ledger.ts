import { closeSync, existsSync, fsyncSync, mkdirSync, openSync, readFileSync, renameSync, writeSync } from "node:fs";
import { dirname } from "node:path";

// Prepaid balances a seller holds for its buyers. The file format is the
// Go paywall's (paywall.FileLedger), so a seller can move between them.

export interface Withdrawal {
  id: string;
  account: string;
  requested: string; // "all" or uaeth, as asked
  amount: string; // uaeth
  status: "reserved" | "pending" | "confirmed";
  txHash?: string;
  /** The signed payout (base64), kept from before it's broadcast until it's in a block. */
  txBytes?: string;
  sequence?: number;
  at: string;
  /** When the payout's sequence was first seen used while the payout wasn't on chain. */
  sequenceSpentAt?: string;
}

/** One transfer of what an aether-pull buyer owes (the Go paywall's Collection). */
export interface Collection {
  id: string;
  account: string;
  amount: string; // uaeth
  status: "reserved" | "pending" | "confirmed" | "failed";
  txHash?: string;
  /** The signed transfer (base64), kept from before it's broadcast until it's in a block. */
  txBytes?: string;
  sequence?: number;
  at: string;
  sequenceSpentAt?: string;
  log?: string;
}

/** Where one aether-pull buyer stands, in uaeth. */
export interface PullAccount {
  accrued: bigint; // charged, not yet being collected
  inFlight: bigint; // in the open collection
  unpaid: bigint; // from failed collections
}

export const owedOf = (a: PullAccount) => a.accrued + a.inFlight + a.unpaid;

/** What aether-pull buyers owe. Every method must be atomic and durable. */
export interface PullLedger {
  /** Charges amount for a requestId not charged before; ok false (nothing changes) if what's accrued would exceed limit. */
  accrue(account: string, requestId: string, amount: bigint, limit: bigint, forgetAfterMs: number): { accrued: bigint; ok: boolean; fresh: boolean };
  /** Reverses an accrue not yet taken into a collection. */
  unaccrue(account: string, requestId: string, amount: bigint): void;
  pullAccount(account: string): PullAccount;
  /** Accounts with something accrued or a collection open. */
  collectable(): string[];
  /** The account's open collection, or a new one for everything accrued (undefined: nothing to collect). */
  openCollection(account: string, id: string, atMs: number): Collection | undefined;
  saveCollection(c: Collection): void;
  /** Ends the open collection: collected, or failed (its amount becomes unpaid). */
  closeCollection(account: string, collected: boolean, log?: string): void;
  /** Moves unpaid back to accrued, to collect again. */
  reinstate(account: string): void;
}

export class LedgerError extends Error {
  constructor(readonly code: "insufficient" | "below_minimum", message: string) {
    super(message);
  }
}

/** Every method must be atomic and durable: these are customers' funds. */
export interface Ledger {
  /** Adds a deposit to account, once per transaction hash. */
  credit(depositTx: string, account: string, amount: bigint): { balance: bigint; credited: boolean };
  /** Deducts amount for a requestId not charged before; ok false (nothing changes) if the balance is short. */
  charge(account: string, requestId: string, amount: bigint, forgetAfterMs: number): { balance: bigint; ok: boolean; fresh: boolean };
  /** Reverses a charge, forgetting the requestId. */
  refund(account: string, requestId: string, amount: bigint): void;
  balance(account: string): bigint;
  /** Deducts a withdrawal (undefined amount: the whole balance) and records it under id, once. Throws LedgerError. */
  reserveWithdrawal(account: string, id: string, amount: bigint | undefined, min: bigint, atMs: number): { withdrawal: Withdrawal; balance: bigint; fresh: boolean };
  saveWithdrawal(w: Withdrawal): void;
  /** Returns a reserved withdrawal to the balance and forgets it: only for a payout that can never land. */
  cancelWithdrawal(account: string, id: string): void;
}

interface State {
  balances: Record<string, string>;
  deposits: Record<string, { account: string; amount: string; at: string }>;
  requests: Record<string, { amount: string; forgetAfter: string }>;
  withdrawals?: Record<string, Withdrawal>;
  pull?: Record<string, { accrued?: string; unpaid?: string; open?: Collection }>;
  pullRequests?: Record<string, { amount: string; forgetAfter: string }>;
  collections?: Record<string, Collection>;
}

const UINT = /^[0-9]+$/;
const num = (s: string | undefined) => (s && UINT.test(s) ? BigInt(s) : 0n);

/**
 * A Ledger in one JSON file, rewritten atomically (temp file, fsync,
 * rename) on every change. For one server process; several need a shared
 * Ledger (e.g. a database). path "" keeps it in memory (tests).
 */
export class FileLedger implements Ledger, PullLedger {
  private state: State = { balances: {}, deposits: {}, requests: {}, withdrawals: {} };

  constructor(private readonly path: string) {
    if (path && existsSync(path)) {
      try {
        this.state = JSON.parse(readFileSync(path, "utf8")) as State;
      } catch (e) {
        throw new Error(`prepaid ledger ${path} is unreadable: ${(e as Error).message}`);
      }
      this.state.balances ??= {};
      this.state.deposits ??= {};
      this.state.requests ??= {};
      this.state.withdrawals ??= {};
    }
    this.state.pull ??= {};
    this.state.pullRequests ??= {};
    this.state.collections ??= {};
  }

  // --- aether-pull ---

  pullAccount(account: string): PullAccount {
    const r = this.state.pull![account];
    return { accrued: num(r?.accrued), inFlight: num(r?.open?.amount), unpaid: num(r?.unpaid) };
  }

  private rec(account: string) {
    return (this.state.pull![account] ??= {});
  }

  private dropIfEmpty(account: string) {
    const r = this.state.pull![account];
    if (r && !r.accrued && !r.unpaid && !r.open) delete this.state.pull![account];
  }

  accrue(account: string, requestId: string, amount: bigint, limit: bigint, forgetAfterMs: number) {
    const key = `${account}/${requestId}`;
    const accrued = this.pullAccount(account).accrued;
    if (this.state.pullRequests![key]) return { accrued, ok: false, fresh: false };
    if (accrued + amount > limit) return { accrued, ok: false, fresh: true };
    return this.mutate(() => {
      this.rec(account).accrued = (accrued + amount).toString();
      this.state.pullRequests![key] = { amount: amount.toString(), forgetAfter: new Date(forgetAfterMs).toISOString() };
      return { accrued: accrued + amount, ok: true, fresh: true };
    });
  }

  unaccrue(account: string, requestId: string, amount: bigint) {
    const key = `${account}/${requestId}`;
    if (!this.state.pullRequests![key]) throw new Error(`request ${key} was not charged`);
    const accrued = this.pullAccount(account).accrued;
    if (accrued < amount) throw new Error(`request ${key} is already being collected`);
    this.mutate(() => {
      const r = this.rec(account);
      r.accrued = accrued - amount ? (accrued - amount).toString() : undefined;
      if (!r.accrued) delete r.accrued;
      delete this.state.pullRequests![key];
      this.dropIfEmpty(account);
    });
  }

  collectable(): string[] {
    return Object.entries(this.state.pull!).filter(([, r]) => r.accrued || r.open).map(([a]) => a).sort();
  }

  openCollection(account: string, id: string, atMs: number): Collection | undefined {
    const r = this.state.pull![account];
    if (!r) return undefined;
    if (r.open) return { ...r.open };
    if (!r.accrued) return undefined;
    return this.mutate(() => {
      const c: Collection = { id, account, amount: r.accrued!, status: "reserved", at: new Date(atMs).toISOString() };
      r.open = c;
      delete r.accrued;
      return { ...c };
    });
  }

  saveCollection(c: Collection) {
    const r = this.state.pull![c.account];
    if (!r?.open || r.open.id !== c.id) throw new Error(`collection ${c.id} is not open`);
    this.mutate(() => {
      r.open = { ...c };
    });
  }

  closeCollection(account: string, collected: boolean, log?: string) {
    const r = this.state.pull![account];
    if (!r?.open) throw new Error(`${account} has no open collection`);
    this.mutate(() => {
      const c: Collection = { ...r.open!, txBytes: undefined, status: collected ? "confirmed" : "failed", ...(log ? { log } : {}) };
      if (!collected) r.unpaid = (num(r.unpaid) + num(c.amount)).toString();
      this.state.collections![c.id] = c;
      delete r.open;
      this.dropIfEmpty(account);
    });
  }

  reinstate(account: string) {
    const r = this.state.pull![account];
    if (!r?.unpaid) return;
    this.mutate(() => {
      r.accrued = (num(r.accrued) + num(r.unpaid)).toString();
      delete r.unpaid;
    });
  }

  balance(account: string): bigint {
    return num(this.state.balances[account]);
  }

  credit(depositTx: string, account: string, amount: bigint) {
    if (this.state.deposits[depositTx]) return { balance: this.balance(account), credited: false };
    return this.mutate(() => {
      const balance = this.balance(account) + amount;
      this.state.balances[account] = balance.toString();
      this.state.deposits[depositTx] = { account, amount: amount.toString(), at: new Date().toISOString() };
      return { balance, credited: true };
    });
  }

  charge(account: string, requestId: string, amount: bigint, forgetAfterMs: number) {
    const key = `${account}/${requestId}`;
    const bal = this.balance(account);
    if (this.state.requests[key]) return { balance: bal, ok: false, fresh: false };
    if (bal < amount) return { balance: bal, ok: false, fresh: true };
    return this.mutate(() => {
      this.state.balances[account] = (bal - amount).toString();
      this.state.requests[key] = { amount: amount.toString(), forgetAfter: new Date(forgetAfterMs).toISOString() };
      return { balance: bal - amount, ok: true, fresh: true };
    });
  }

  refund(account: string, requestId: string, amount: bigint) {
    const key = `${account}/${requestId}`;
    if (!this.state.requests[key]) return;
    this.mutate(() => {
      this.state.balances[account] = (this.balance(account) + amount).toString();
      delete this.state.requests[key];
    });
  }

  reserveWithdrawal(account: string, id: string, amount: bigint | undefined, min: bigint, atMs: number) {
    const key = `${account}/${id}`;
    const bal = this.balance(account);
    const seen = this.state.withdrawals![key];
    if (seen) return { withdrawal: seen, balance: bal, fresh: false };
    const take = amount ?? bal;
    if (bal <= 0n || take <= 0n || take > bal) throw new LedgerError("insufficient", "insufficient balance");
    if (take < min && take !== bal) throw new LedgerError("below_minimum", "below the minimum withdrawal");
    return this.mutate(() => {
      const withdrawal: Withdrawal = {
        id, account, requested: amount === undefined ? "all" : amount.toString(), amount: take.toString(), status: "reserved", at: new Date(atMs).toISOString(),
      };
      this.state.balances[account] = (bal - take).toString();
      this.state.withdrawals![key] = withdrawal;
      return { withdrawal, balance: bal - take, fresh: true };
    });
  }

  saveWithdrawal(w: Withdrawal) {
    const key = `${w.account}/${w.id}`;
    if (!this.state.withdrawals![key]) throw new Error("withdrawal not found");
    this.mutate(() => {
      this.state.withdrawals![key] = { ...w };
    });
  }

  cancelWithdrawal(account: string, id: string) {
    const key = `${account}/${id}`;
    const w = this.state.withdrawals![key];
    if (!w) throw new Error("withdrawal not found");
    this.mutate(() => {
      this.state.balances[account] = (this.balance(account) + num(w.amount)).toString();
      delete this.state.withdrawals![key];
    });
  }

  /** Applies fn and saves; on a failed save, the change is undone. */
  private mutate<T>(fn: () => T): T {
    const snapshot = structuredClone(this.state);
    try {
      const r = fn();
      this.save();
      return r;
    } catch (e) {
      this.state = snapshot;
      throw e;
    }
  }

  private save() {
    const now = Date.now();
    for (const m of [this.state.requests, this.state.pullRequests!]) {
      for (const [k, r] of Object.entries(m)) {
        if (!r.forgetAfter.startsWith("0001-") && Date.parse(r.forgetAfter) < now) delete m[k];
      }
    }
    if (!this.path) return;
    mkdirSync(dirname(this.path), { recursive: true, mode: 0o700 });
    const tmp = this.path + ".tmp";
    const fd = openSync(tmp, "w", 0o600);
    try {
      // Like the Go ledger: empty aether-pull maps are left out.
      const out: Partial<State> = { ...this.state };
      for (const k of ["pull", "pullRequests", "collections"] as const) if (!Object.keys(out[k] ?? {}).length) delete out[k];
      writeSync(fd, JSON.stringify(out, null, 2));
      fsyncSync(fd);
    } finally {
      closeSync(fd);
    }
    renameSync(tmp, this.path);
  }
}
