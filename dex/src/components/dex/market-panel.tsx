import { SNAPSHOT_PRICE_E6 } from "@/lib/dex/constants";
import { formatBase, formatK, formatPercentFromBps, formatPrice } from "@/lib/dex/format";
import { spotPriceE6 } from "@/lib/dex/math";
import { useDex } from "@/lib/dex/store";
import { panel } from "./chrome";
import { PriceChart } from "./price-chart";

export function MarketPanel() {
  const dex = useDex();
  const reserveAeth = BigInt(dex.reserveAeth);
  const reserveUsdc = BigInt(dex.reserveUsdc);
  const spot = spotPriceE6(reserveAeth, reserveUsdc);
  const delta = ((spot - SNAPSHOT_PRICE_E6) * 10_000n) / SNAPSHOT_PRICE_E6;
  const tvl = reserveUsdc * 2n;
  const k = reserveAeth * reserveUsdc;
  const moved = delta !== 0n;
  const sign = delta > 0n ? "+" : "−";

  const cells = [
    ["AETH reserve", formatBase(reserveAeth)],
    ["USDC reserve", formatBase(reserveUsdc)],
    ["TVL at mid", `${formatBase(tvl)} USDC`],
    ["Invariant k", formatK(k)],
  ] as const;

  return (
    <section className={panel}>
      <p className="text-xs font-medium tracking-wide text-muted">AETH / USDC</p>
      <p className="mt-1 font-mono text-3xl font-medium tracking-tight text-fg tabular-nums sm:text-4xl">
        {formatPrice(spot)}
      </p>
      <p className="mt-1 text-sm text-muted">
        {moved
          ? `USDC per AETH · ${sign}${formatPercentFromBps(delta)} from snapshot`
          : "USDC per AETH · snapshot price"}
      </p>

      <div className="mt-4">
        <PriceChart points={dex.prices} label={`Pool mid ${formatPrice(spot)} USDC per AETH`} />
        <p className="text-xs text-muted">
          {dex.trades.length === 0
            ? "Illustrative path into the 0.4000 snapshot. Not chain history."
            : "Illustrative path, then mids from this session."}
        </p>
      </div>

      <div className="mt-4 grid grid-cols-2 gap-px overflow-hidden rounded-sm border border-line bg-line">
        {cells.map(([label, value]) => (
          <div key={label} className="bg-surface px-3 py-3">
            <p className="text-xs text-muted">{label}</p>
            <p className="mt-1 font-mono text-sm break-all text-fg tabular-nums">{value}</p>
          </div>
        ))}
      </div>
      <p className="mt-3 text-xs text-muted">Snapshot depth is 50,000 AETH against 20,000 USDC. Size moves the mid.</p>
    </section>
  );
}
