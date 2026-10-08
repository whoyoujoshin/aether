import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { api, type HelixPeer } from "./api";

/** Which strands the helix pages show: both chains, Aether alone or the IBC chain alone. */
export type ChainView = "both" | "aether" | "ibc";

export const AETHER_COLOR = "#c0503a";
export const IBC_COLOR = "#e4d9c6";

interface ChainState {
  /** Whether the API has said yet if there's a second strand. */
  ready: boolean;
  /** The IBC chain drawn opposite Aether, when the explorer is run with --ibc-rpc. */
  ibcName: string | null;
  /** Every chain the explorer can draw opposite Aether (several with a comma-separated --ibc-rpc). */
  peers: HelixPeer[];
  /** The chain ID of the one drawn; null: the API's default. */
  peer: string | null;
  setPeer: (chainId: string) => void;
  view: ChainView;
  setView: (v: ChainView) => void;
}

const Ctx = createContext<ChainState>({
  ready: false,
  ibcName: null,
  peers: [],
  peer: null,
  setPeer: () => {},
  view: "both",
  setView: () => {},
});

const STORAGE_KEY = "aether-explorer-chain-view";
const PEER_KEY = "aether-explorer-peer";

function stored(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function store(key: string, v: string) {
  try {
    localStorage.setItem(key, v);
  } catch {
    // Not remembered, still applied.
  }
}

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
  const [firstName, setFirstName] = useState<string | null>(null);
  const [peers, setPeers] = useState<HelixPeer[]>([]);
  const [peer, setPeerState] = useState<string | null>(() => stored(PEER_KEY));
  const [ready, setReady] = useState(false);
  const [view, setViewState] = useState<ChainView>(storedView);
  useEffect(() => {
    api
      .helix(10, 0, stored(PEER_KEY))
      .then((h) => {
        setFirstName(h.ibc ? h.ibc.name || h.ibc.chainId || "IBC" : null);
        setPeers((h.peers ?? []).filter((p) => p.chainId));
        // A remembered chain the explorer no longer draws: use the one it did.
        setPeerState(h.ibc?.chainId || null);
      })
      .catch(() => setFirstName(null))
      .finally(() => setReady(true));
  }, []);
  function setView(v: ChainView) {
    setViewState(v);
    store(STORAGE_KEY, v);
  }
  function setPeer(chainId: string) {
    setPeerState(chainId);
    store(PEER_KEY, chainId);
  }
  const ibcName = peers.find((p) => p.chainId === peer)?.name || firstName;
  // Without --ibc-rpc still draw both strands: Aether live + faint IBC ghost (README).
  return (
    <Ctx.Provider value={{ ready, ibcName, peers, peer, setPeer, view: ibcName ? view : "both", setView }}>{children}</Ctx.Provider>
  );
}

export function useChain(): ChainState {
  return useContext(Ctx);
}

/** The Both / Aether / IBC switch the helix pages share, with a picker for the IBC chain when there are several. */
export function ChainSelector() {
  const { ibcName, peers, peer, setPeer, view, setView } = useChain();
  if (!ibcName) return null;
  const opts: [ChainView, string, string][] = [
    ["both", "Both", `linear-gradient(90deg, ${AETHER_COLOR} 50%, ${IBC_COLOR} 50%)`],
    ["aether", "Aether", AETHER_COLOR],
    ["ibc", ibcName, IBC_COLOR],
  ];
  return (
    <div className="chain-switch" role="radiogroup" aria-label="Chains to show">
      {peers.length > 1 && (
        <select className="peer-select" aria-label="IBC chain to draw" value={peer ?? ""} onChange={(e) => setPeer(e.target.value)}>
          {peers.map((p) => (
            <option key={p.chainId} value={p.chainId}>
              {p.name}
            </option>
          ))}
        </select>
      )}
      {opts.map(([v, label, dot]) => (
        <button key={v} role="radio" aria-checked={view === v} className={view === v ? "on" : ""} onClick={() => setView(v)}>
          <span className="dot" style={{ background: dot }} />
          {label}
        </button>
      ))}
    </div>
  );
}
