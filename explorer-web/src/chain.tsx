import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { api } from "./api";

/** Which strands the helix pages show: both chains, Aether alone or the IBC chain alone. */
export type ChainView = "both" | "aether" | "ibc";

export const AETHER_COLOR = "#c0503a";
export const IBC_COLOR = "#e4d9c6";

interface ChainState {
  /** Whether the API has said yet if there's a second strand. */
  ready: boolean;
  /** The IBC chain on the other end of Aether's channel, when the explorer is run with --ibc-rpc. */
  ibcName: string | null;
  view: ChainView;
  setView: (v: ChainView) => void;
}

const Ctx = createContext<ChainState>({ ready: false, ibcName: null, view: "both", setView: () => {} });

const STORAGE_KEY = "aether-explorer-chain-view";

function storedView(): ChainView {
  try {
    const v = localStorage.getItem(STORAGE_KEY);
    if (v === "both" || v === "aether" || v === "ibc") return v;
  } catch {
    // Storage can be unavailable (private windows); the default is fine.
  }
  return "both";
}

/** Asks the API once whether there's a second strand to draw, and keeps the chosen view. */
export function ChainProvider({ children }: { children: ReactNode }) {
  const [ibcName, setIbcName] = useState<string | null>(null);
  const [ready, setReady] = useState(false);
  const [view, setViewState] = useState<ChainView>(storedView);
  useEffect(() => {
    api
      .helix(10)
      .then((h) => setIbcName(h.ibc ? h.ibc.name || h.ibc.chainId || "IBC" : null))
      .catch(() => setIbcName(null))
      .finally(() => setReady(true));
  }, []);
  function setView(v: ChainView) {
    setViewState(v);
    try {
      localStorage.setItem(STORAGE_KEY, v);
    } catch {
      // Not remembered, still applied.
    }
  }
  // Without --ibc-rpc still draw both strands: Aether live + faint IBC ghost (README).
  return <Ctx.Provider value={{ ready, ibcName, view: ibcName ? view : "both", setView }}>{children}</Ctx.Provider>;
}

export function useChain(): ChainState {
  return useContext(Ctx);
}

/** The Both / Aether / IBC switch the helix pages share. */
export function ChainSelector() {
  const { ibcName, view, setView } = useChain();
  if (!ibcName) return null;
  const opts: [ChainView, string, string][] = [
    ["both", "Both", `linear-gradient(90deg, ${AETHER_COLOR} 50%, ${IBC_COLOR} 50%)`],
    ["aether", "Aether", AETHER_COLOR],
    ["ibc", ibcName, IBC_COLOR],
  ];
  return (
    <div className="chain-switch" role="radiogroup" aria-label="Chains to show">
      {opts.map(([v, label, dot]) => (
        <button key={v} role="radio" aria-checked={view === v} className={view === v ? "on" : ""} onClick={() => setView(v)}>
          <span className="dot" style={{ background: dot }} />
          {label}
        </button>
      ))}
    </div>
  );
}
