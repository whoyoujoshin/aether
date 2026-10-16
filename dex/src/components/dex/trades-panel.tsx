import { assetSymbol, formatBase, formatPrice, formatUtc } from "@/lib/dex/format";
import { useDex } from "@/lib/dex/store";
import type { Asset } from "@/lib/dex/types";

export function TradesPanel() {
  const dex = useDex();
  if (dex.trades.length === 0) {
    return (
      <p className="text-sm text-muted">
        No fills yet. A swap prints here and moves both reserves. The chart’s opening path is illustrative and is not
        part of this book.
      </p>
    );
  }

  return (
    <ol>
      {dex.trades.map((trade) => {
        const tokenIn = trade.tokenIn;
        const tokenOut: Asset = tokenIn === "aeth" ? "usdc" : "aeth";
        return (
          <li key={trade.id} className="border-t border-line py-3 first:border-t-0 first:pt-0">
            <div className="flex items-baseline justify-between gap-3">
              <p className="text-sm text-fg">
                {formatBase(BigInt(trade.amountIn))} {assetSymbol(tokenIn)}
                <span className="text-muted"> to </span>
                {formatBase(BigInt(trade.amountOut))} {assetSymbol(tokenOut)}
              </p>
            </div>
            <div className="mt-1 flex items-baseline justify-between gap-3 text-xs text-muted">
              <p>{formatUtc(trade.ts)}</p>
              <p className="font-mono tabular-nums">
                {trade.id} · 1 AETH = {formatPrice(BigInt(trade.priceE6))}
              </p>
            </div>
          </li>
        );
      })}
    </ol>
  );
}
