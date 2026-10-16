import { useEffect, useState } from "react";
import { CHAIN_ID } from "@/lib/dex/constants";
import { assetSymbol, formatBase } from "@/lib/dex/format";
import { hydrateDex, resetDemo, useDex } from "@/lib/dex/store";
import { ghostBtn, panel } from "./chrome";
import { LiquidityPanel } from "./liquidity-panel";
import { MarketPanel } from "./market-panel";
import { SpecPanel } from "./spec-panel";
import { AgentDesk } from "./agent-desk";
import { TradesPanel } from "./trades-panel";

const TABS = [
  { id: "liquidity", label: "Liquidity" },
  { id: "trades", label: "Trades" },
  { id: "spec", label: "Spec" },
] as const;

type TabId = (typeof TABS)[number]["id"];

export function DexApp() {
  const dex = useDex();
  const [tab, setTab] = useState<TabId>("liquidity");
  const [armReset, setArmReset] = useState(false);

  useEffect(() => {
    hydrateDex();
  }, []);

  return (
    <div className="min-h-dvh bg-bg text-fg">
      <header className="border-b border-line">
        <div className="mx-auto flex max-w-6xl flex-col gap-4 px-4 py-4 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex items-center gap-3">
            <img src="/favicon.svg" alt="" width={32} height={32} className="size-8" />
            <div>
              <h1 className="text-lg font-medium tracking-tight">
                Aether <span className="font-mono text-xs tracking-widest text-muted">DEX</span>
              </h1>
              <p className="text-sm text-muted">Agent desk · AETH / USDC</p>
            </div>
          </div>
          <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
            <p className="font-mono text-xs text-muted">{CHAIN_ID}</p>
            <Balance label={assetSymbol("aeth")} amount={dex.walletAeth} />
            <Balance label={assetSymbol("usdc")} amount={dex.walletUsdc} />
            {armReset ? (
              <span className="flex items-center">
                <button type="button" className={ghostBtn} onClick={() => setArmReset(false)}>
                  Cancel
                </button>
                <button
                  type="button"
                  className={ghostBtn}
                  onClick={() => {
                    resetDemo();
                    setArmReset(false);
                  }}
                >
                  Confirm reset
                </button>
              </span>
            ) : (
              <button type="button" className={ghostBtn} onClick={() => setArmReset(true)}>
                Reset pool
              </button>
            )}
          </div>
        </div>
      </header>
      <p className="border-b border-line">
        <span className="mx-auto block max-w-6xl px-4 py-2 text-xs text-muted">
          For review before the chain repo. An agent calls the pool. Settlement stays in this browser. Nothing is broadcast
          to {CHAIN_ID}.
        </span>
      </p>

      <main className="mx-auto grid max-w-6xl gap-4 px-4 py-4 lg:grid-cols-3 lg:items-start">
        <div className="min-w-0 lg:sticky lg:top-4 lg:self-start">
          <AgentDesk />
        </div>
        <div className="min-w-0 lg:col-span-2">
          <MarketPanel />
          <section className={`${panel} mt-4`}>
            <div role="tablist" aria-label="Pool details" className="grid grid-cols-3 gap-1 rounded-sm bg-surface-2 p-1">
              {TABS.map((item) => (
                <button
                  key={item.id}
                  type="button"
                  role="tab"
                  aria-selected={tab === item.id}
                  onClick={() => setTab(item.id)}
                  className={
                    tab === item.id
                      ? "h-11 rounded-xs bg-surface text-sm text-fg"
                      : "h-11 rounded-xs text-sm text-muted hover:text-fg"
                  }
                >
                  {item.label}
                </button>
              ))}
            </div>
            <div className="mt-4" role="tabpanel">
              {tab === "liquidity" ? <LiquidityPanel /> : null}
              {tab === "trades" ? <TradesPanel /> : null}
              {tab === "spec" ? <SpecPanel /> : null}
            </div>
          </section>
        </div>
      </main>
    </div>
  );
}

function Balance({ label, amount }: { label: string; amount: string }) {
  return (
    <p className="font-mono text-sm tabular-nums">
      <span className="mr-2 text-xs text-muted">{label}</span>
      {formatBase(BigInt(amount))}
    </p>
  );
}
