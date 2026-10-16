import { useState } from "react";
import {
  AETH_DENOM,
  CHAIN_ID,
  TEST_VECTOR,
  USDC_BASE_DENOM,
  USDC_COUNTERPARTY,
  USDC_IBC_DENOM,
  USDC_PATH,
} from "@/lib/dex/constants";
import { FORMULA } from "@/lib/dex/math";

const ROWS = [
  ["Chain", CHAIN_ID],
  ["AETH base", `${AETH_DENOM} · 6 decimals`],
  ["USDC trace", `${USDC_PATH}/${USDC_BASE_DENOM}`],
  ["Counterparty", USDC_COUNTERPARTY],
] as const;

export function SpecPanel() {
  const [copied, setCopied] = useState(false);

  async function copyDenom() {
    try {
      await navigator.clipboard.writeText(USDC_IBC_DENOM);
      setCopied(true);
    } catch {
      setCopied(false);
    }
  }

  return (
    <div className="grid gap-5 text-sm">
      <p className="text-muted">
        The prime caller is an agent. An instruction becomes one tool call in{" "}
        <span className="font-mono text-fg">src/lib/dex/agent.ts</span> — get_pool, quote_swap, swap, deposit, withdraw,
        set_slippage. Spends over the per-trade limit are refused, and only the owner sets that limit. A swap settles
        only if it still pays at least its min_receive. High impact returns needs_confirm and does
        not settle until the instruction includes confirm. The fill is still the integer AMM in{" "}
        <span className="font-mono text-fg">math.ts</span>. Nothing is signed or broadcast.
      </p>

      <div>
        <h3 className="font-medium">Quote</h3>
        <pre className="mt-2 overflow-x-auto rounded-sm bg-surface-2 p-3 font-mono text-xs leading-relaxed text-fg">
          {FORMULA}
        </pre>
        <p className="mt-2 text-muted">
          Fee is 30 / 10_000 of the input and stays in the reserve, so k increases. Truncation favors the pool. Swap
          either way by handing the offer reserve in and the ask reserve out.
        </p>
      </div>

      <div>
        <h3 className="font-medium">Vector</h3>
        <dl className="mt-2 grid grid-cols-2 gap-x-4 gap-y-1 font-mono text-xs">
          <dt className="text-muted">Reserves</dt>
          <dd>50,000 AETH / 20,000 USDC</dd>
          <dt className="text-muted">Exact in</dt>
          <dd>{TEST_VECTOR.amountIn.toString()} uaeth</dd>
          <dt className="text-muted">Exact out</dt>
          <dd>{TEST_VECTOR.amountOut.toString()} base USDC</dd>
        </dl>
        <p className="mt-2 text-muted">
          Pinned in <span className="font-mono text-fg">constants.ts</span> and checked by{" "}
          <span className="font-mono text-fg">math.test.ts</span>.
        </p>
      </div>

      <div>
        <h3 className="font-medium">Assets on {CHAIN_ID}</h3>
        <dl className="mt-2 grid gap-2">
          {ROWS.map(([label, value]) => (
            <div key={label}>
              <dt className="text-xs text-muted">{label}</dt>
              <dd className="mt-1 font-mono text-xs break-all text-fg">{value}</dd>
            </div>
          ))}
          <div>
            <dt className="text-xs text-muted">USDC denom</dt>
            <dd className="mt-1 font-mono text-xs break-all text-fg">{USDC_IBC_DENOM}</dd>
          </div>
        </dl>
        <button
          type="button"
          onClick={() => void copyDenom()}
          className="mt-3 h-11 rounded-sm border border-line px-3 text-sm text-fg hover:opacity-80"
        >
          {copied ? "Copied denom" : "Copy USDC denom"}
        </button>
        <p className="mt-2 text-xs text-muted">
          The hash matches bank metadata on the testnet: IBC USDC from Injective, not an Osmosis-routed trace.
        </p>
      </div>

      <div>
        <h3 className="font-medium">Module shape</h3>
        <pre className="mt-2 overflow-x-auto rounded-sm bg-surface-2 p-3 font-mono text-xs leading-relaxed text-fg">
          {`MsgSwap     sender, offer_denom, offer_amount, min_receive
MsgDeposit  sender, aeth, usdc, min_shares
MsgWithdraw sender, shares, min_aeth, min_usdc`}
        </pre>
        <p className="mt-2 text-muted">
          The module account holds both denoms. Shares can be a bank denom if a position should be transferable; this
          demo keeps them on the local account. A handler recomputes the quote and rejects when the output is under
          the caller’s minimum.
        </p>
      </div>
    </div>
  );
}
