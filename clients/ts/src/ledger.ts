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
}

const UINT = /^[0-9]+$/;
const num = (s: string | undefined) => (s && UINT.test(s) ? BigInt(s) : 0n);

/**
 * A Ledger in one JSON file, rewritten atomically (temp file, fsync,
 * rename) on every change. For one server process; several need a shared
 * Ledger (e.g. a database). path "" keeps it in memory (tests).
 */
export class FileLedger implements Ledger {
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
    for (const [k, r] of Object.entries(this.state.requests)) {
      if (!r.forgetAfter.startsWith("0001-") && Date.parse(r.forgetAfter) < now) delete this.state.requests[k];
    }
    if (!this.path) return;
    mkdirSync(dirname(this.path), { recursive: true, mode: 0o700 });
    const tmp = this.path + ".tmp";
    const fd = openSync(tmp, "w", 0o600);
    try {
      writeSync(fd, JSON.stringify(this.state, null, 2));
      fsyncSync(fd);
    } finally {
      closeSync(fd);
    }
    renameSync(tmp, this.path);
  }
}
