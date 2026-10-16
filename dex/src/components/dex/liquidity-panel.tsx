import { useState } from "react";
import { baseToInput, formatBase, formatPercentFromBps, parseBase, sanitizeAmount } from "@/lib/dex/format";
import { maxDepositAeth, quoteDeposit, quoteWithdraw } from "@/lib/dex/math";
import { deposit, useDex, withdraw } from "@/lib/dex/store";
import { primaryBtn } from "./chrome";

const PORTIONS = [25, 50, 75, 100] as const;

export function LiquidityPanel() {
  const dex = useDex();
  const [amount, setAmount] = useState("");
  const [portion, setPortion] = useState(100);
  const [status, setStatus] = useState<string | null>(null);

  const reserveAeth = BigInt(dex.reserveAeth);
  const reserveUsdc = BigInt(dex.reserveUsdc);
  const supply = BigInt(dex.lpSupply);
  const walletAeth = BigInt(dex.walletAeth);
  const walletUsdc = BigInt(dex.walletUsdc);
  const walletLp = BigInt(dex.walletLp);
  const parsed = parseBase(amount);
  const active = parsed !== null && parsed > 0n;
  const quoted = active ? quoteDeposit(parsed, reserveAeth, reserveUsdc, supply) : { amountUsdc: 0n, lpOut: 0n };
  const cap = maxDepositAeth(walletAeth, walletUsdc, reserveAeth, reserveUsdc);

  let error: string | null = null;
  if (active) {
    if (parsed > walletAeth) error = "Not enough AETH";
    else if (quoted.amountUsdc > walletUsdc) error = "Not enough USDC for the pool ratio";
    else if (quoted.lpOut <= 0n) error = "Amount is too small";
  }

  const shareBps = supply > 0n ? (walletLp * 10_000n) / supply : 0n;
  const lpIn = (walletLp * BigInt(portion)) / 100n;
  const out = quoteWithdraw(lpIn, reserveAeth, reserveUsdc, supply);

  function onDeposit() {
    if (!active || error || parsed === null) return;
    const usdc = quoted.amountUsdc;
    const shares = quoted.lpOut;
    const result = deposit(parsed);
    if (!result.ok) {
      setStatus(result.error);
      return;
    }
    setStatus(`Deposited ${formatBase(parsed)} AETH and ${formatBase(usdc)} USDC. Minted ${formatBase(shares)} shares.`);
    setAmount("");
  }

  function onWithdraw() {
    const preview = quoteWithdraw(lpIn, reserveAeth, reserveUsdc, supply);
    const result = withdraw(lpIn);
    if (!result.ok) {
      setStatus(result.error);
      return;
    }
    setStatus(`Withdrew ${formatBase(preview.amountAeth)} AETH and ${formatBase(preview.amountUsdc)} USDC.`);
  }

  return (
    <div className="grid gap-6">
      <div>
        <h3 className="text-sm font-medium">Provide</h3>
        <p className="mt-1 text-sm text-muted">USDC follows the current ratio, so a deposit does not move the mid.</p>
        <form
          className="mt-3"
          onSubmit={(event) => {
            event.preventDefault();
            onDeposit();
          }}
        >
          <div className="rounded-sm bg-surface-2 p-3">
            <div className="flex items-center justify-between gap-3">
              <label htmlFor="deposit-aeth" className="text-xs text-muted">
                AETH
              </label>
              <button
                type="button"
                className="inline-flex h-11 items-center font-mono text-xs text-muted tabular-nums hover:text-fg"
                onClick={() => {
                  setAmount(baseToInput(cap));
                  setStatus(null);
                }}
              >
                Max {formatBase(cap)}
              </button>
            </div>
            <input
              id="deposit-aeth"
              inputMode="decimal"
              autoComplete="off"
              autoCorrect="off"
              spellCheck={false}
              placeholder="0"
              value={amount}
              onChange={(event) => {
                setAmount(sanitizeAmount(event.target.value));
                setStatus(null);
              }}
              className="mt-2 w-full bg-transparent font-mono text-2xl text-fg tabular-nums outline-none placeholder:text-muted"
            />
          </div>
          <dl className="mt-3 grid grid-cols-[1fr_auto] gap-y-2 text-sm">
            <dt className="text-muted">USDC required</dt>
            <dd className="font-mono tabular-nums">{active ? formatBase(quoted.amountUsdc) : "0"}</dd>
            <dt className="text-muted">Shares minted</dt>
            <dd className="font-mono tabular-nums">{active ? formatBase(quoted.lpOut) : "0"}</dd>
          </dl>
          {error ? <p className="mt-3 text-sm text-danger">{error}</p> : null}
          <button type="submit" className={`${primaryBtn} mt-4`} disabled={!active || !!error}>
            {!active ? "Enter an amount" : "Provide liquidity"}
          </button>
        </form>
      </div>

      <div className="border-t border-line pt-6">
        <h3 className="text-sm font-medium">Withdraw</h3>
        <p className="mt-1 text-sm text-muted">
          Your shares {formatBase(walletLp)} · {formatPercentFromBps(shareBps)} of the pool. Displayed ÷ 10^6; the
          accounting unit is the raw integer.
        </p>
        {walletLp === 0n ? (
          <p className="mt-3 text-sm text-muted">No shares yet. A deposit mints them against the current reserves.</p>
        ) : (
          <div className="mt-3">
            <div className="grid grid-cols-4 gap-2" role="group" aria-label="Share portion">
              {PORTIONS.map((pct) => (
                <button
                  key={pct}
                  type="button"
                  aria-pressed={portion === pct}
                  onClick={() => setPortion(pct)}
                  className={
                    portion === pct
                      ? "h-11 rounded-sm border border-fg text-sm text-fg"
                      : "h-11 rounded-sm border border-line text-sm text-muted hover:text-fg"
                  }
                >
                  {pct === 100 ? "All" : `${pct}%`}
                </button>
              ))}
            </div>
            <dl className="mt-3 grid grid-cols-[1fr_auto] gap-y-2 text-sm">
              <dt className="text-muted">AETH out</dt>
              <dd className="font-mono tabular-nums">{formatBase(out.amountAeth)}</dd>
              <dt className="text-muted">USDC out</dt>
              <dd className="font-mono tabular-nums">{formatBase(out.amountUsdc)}</dd>
            </dl>
            <button type="button" className={`${primaryBtn} mt-4`} onClick={onWithdraw}>
              Withdraw {portion === 100 ? "all" : `${portion}%`}
            </button>
          </div>
        )}
      </div>
      {status ? (
        <p className="text-sm text-fg" role="status">
          {status}
        </p>
      ) : null}
    </div>
  );
}
