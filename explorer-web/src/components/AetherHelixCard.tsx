import { api } from "../api";
import { useApi } from "../hooks";
import { AETHER_COLOR, IBC_COLOR } from "../chain";
import { HelixHero, blockTimeLabel, useChainClock, useNarrow } from "./Helix";
import { int } from "../format";

const WINDOW = 96;

/**
 * The helix on the overview of an explorer with no IBC chain to draw (no
 * --ibc-rpc): Aether's live blocks on their strand, and the second strand
 * a faint ghost, labelled, where a connected chain will go.
 */
export function AetherHelixCard() {
  const helix = useApi(() => api.helix(WINDOW, 8), [], 4000);
  const now = useChainClock(helix.data);
  const narrow = useNarrow();
  const h = helix.data;
  // An older API without /api/helix, or one that failed: leave the overview as it was.
  if (!h) return null;

  return (
    <div className="helix-hero" style={{ marginTop: 0, marginBottom: 20 }}>
      <div className="helix-legend">
        <div className="strand">
          <span className="dot" style={{ background: AETHER_COLOR, boxShadow: `0 0 10px ${AETHER_COLOR}` }} />
          <span className="name">Aether</span>
          <span className="h" style={{ color: AETHER_COLOR }}>#{int(h.aether.height)}</span>
          <span className="meta">PoW · {blockTimeLabel(h.aether.blockTimeSecs)}</span>
        </div>
        <div className="strand" style={{ opacity: 0.55 }}>
          <span className="dot" style={{ border: `1.5px dashed ${IBC_COLOR}`, background: "transparent" }} />
          <span className="meta">no IBC chain connected</span>
        </div>
        <div className="window">last {WINDOW}s · time flows left</div>
      </div>
      <HelixHero helix={h} now={now} view="both" width={narrow ? 560 : 1168} height={narrow ? 250 : 200} windowSecs={WINDOW} />
    </div>
  );
}
