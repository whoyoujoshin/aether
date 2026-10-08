import { Link } from "react-router-dom";
import { api, type Helix, type Stats } from "../api";
import { useApi } from "../hooks";
import { AETHER_COLOR, IBC_COLOR, useChain, type ChainView } from "../chain";
import { TopBar } from "../components/TopBar";
import { TxHash } from "../components/Hash";
import { ErrorBanner, MsgTag } from "../components/ui";
import {
  HelixHero,
  STATUS_LABEL,
  blockTimeLabel,
  mergedBlocks,
  packetAmount,
  packetKey,
  packetTs,
  proposerLabel,
  shortBlockHash,
  useChainClock,
  useNarrow,
} from "../components/Helix";
import { aeth, compact, int, timeAgo } from "../format";

const WINDOW = 96;

interface StrandCard {
  key: string;
  title: string;
  sub: string;
  color: string;
  border: string;
  stats: [string, string][];
}

function cards(helix: Helix, stats: Stats | null, view: ChainView): StrandCard[] {
  const a = helix.aether;
  const epochLen = stats?.epochLength ?? 0;
  const aetherCard: StrandCard = {
    key: "aether",
    title: "Aether",
    sub: `proof-of-work · ${blockTimeLabel(a.blockTimeSecs)}`,
    color: AETHER_COLOR,
    border: "rgba(192,80,58,.35)",
    stats: [
      ["Height", int(a.height)],
      [stats ? `Epoch ${int(stats.currentEpoch)}` : "Epoch", stats && epochLen > 1 ? `${Math.round(((stats.latestHeight % epochLen) / epochLen) * 100)}%` : "—"],
      ["Difficulty", stats?.difficulty ? compact(stats.difficulty) : "—"],
      ["Reward", stats?.blockReward ? aeth(stats.blockReward) : "—"],
    ],
  };
  const br = helix.bridge;
  const bridgeCard: StrandCard = {
    key: "bridge",
    title: "IBC bridge",
    sub: br ? `${br.channelId} ⇄ ${br.counterpartyChannelId}` : "no open transfer channel",
    color: "#f3efe6",
    border: "var(--border)",
    stats: [
      ["Packets 24h", br ? int(br.packetsSent) : "—"],
      ["In flight", br ? int(br.inFlight) : "—"],
      ["Avg relay", br && br.avgRelaySecs ? `${br.avgRelaySecs.toFixed(1)}s` : "—"],
      ["AETH bridged", br ? compact(Number(br.escrowedUaeth) / 1e6) : "—"],
    ],
  };
  const out: StrandCard[] = [];
  if (view !== "ibc") out.push(aetherCard);
  out.push(bridgeCard);
  if (view !== "aether" && helix.ibc) {
    const b = helix.ibc;
    out.push({
      key: "ibc",
      title: b.name,
      sub: `BFT · ${blockTimeLabel(b.blockTimeSecs)}`,
      color: IBC_COLOR,
      border: "rgba(228,217,198,.28)",
      stats: [
        ["Height", int(b.height)],
        ["Validators", b.validators ? int(b.validators) : "—"],
        ["Block time", blockTimeLabel(b.blockTimeSecs)],
        ["Finality", "instant"],
      ],
    });
  }
  return out;
}

/** The overview with both chains drawn as a helix (mockup 5a). */
export default function HelixOverview() {
  const { view, ibcName, peer } = useChain();
  const helix = useApi(() => api.helix(WINDOW, 8, peer), [peer], 4000);
  const stats = useApi(api.stats, [], 6000);
  const recent = useApi(() => api.recentTransactions(20), [], 6000);
  const now = useChainClock(helix.data);
  const narrow = useNarrow();
  const h = helix.data;
  const name = h?.ibc?.name ?? ibcName ?? "IBC";

  const title = !h?.ibc || view === "both" ? "Aether helix" : view === "aether" ? "Aether overview" : `${name} overview`;
  const blocks = h ? mergedBlocks(h, view).slice(0, 7) : [];
  const maxTxs = Math.max(1, ...blocks.map((b) => b.numTxs));

  return (
    <>
      <TopBar />
      <div className="page">
        <div className="helix-title">
          {title}
          <span className="live-pill">
            <i />
            LIVE
          </span>
        </div>
        <ErrorBanner error={helix.error} prefix="Failed to load the chains" />
        {h?.errors?.ibc && <ErrorBanner error={h.errors.ibc} prefix={`Couldn't read ${name}`} />}

        <div className="helix-hero">
          <div className="helix-legend">
            <div className="strand" style={{ opacity: view === "ibc" ? 0.3 : 1 }}>
              <span className="dot" style={{ background: AETHER_COLOR, boxShadow: `0 0 10px ${AETHER_COLOR}` }} />
              <span className="name">Aether</span>
              <span className="h" style={{ color: AETHER_COLOR }}>#{h ? int(h.aether.height) : "—"}</span>
              <span className="meta">PoW · {h ? blockTimeLabel(h.aether.blockTimeSecs) : "—"}</span>
            </div>
            {h?.ibc ? (
              <div className="strand" style={{ opacity: view === "aether" ? 0.3 : 1 }}>
                <span className="dot" style={{ background: IBC_COLOR, boxShadow: `0 0 10px ${IBC_COLOR}` }} />
                <span className="name">{name}</span>
                <span className="h" style={{ color: IBC_COLOR }}>#{int(h.ibc.height)}</span>
                <span className="meta">BFT · {blockTimeLabel(h.ibc.blockTimeSecs)}</span>
              </div>
            ) : (
              <div className="strand" style={{ opacity: 0.55 }}>
                <span className="dot" style={{ border: `1.5px dashed ${IBC_COLOR}`, background: "transparent" }} />
                <span className="meta">no IBC chain connected</span>
              </div>
            )}
            <div className="rung">
              <i />
              IBC packet
            </div>
            <div className="window">last {WINDOW}s · time flows left</div>
          </div>
          {h ? <HelixHero helix={h} now={now} view={view} width={narrow ? 560 : 1168} height={250} windowSecs={WINDOW} /> : <div className="loading">Loading…</div>}
        </div>

        {h && (
          <div className="strand-cards">
            {cards(h, stats.data, view).map((c) => (
              <div key={c.key} className="strand-card" style={{ borderColor: c.border }}>
                <div className="head">
                  <span className="dot" style={{ background: c.color, boxShadow: `0 0 8px ${c.color}` }} />
                  <b>{c.title}</b>
                  <span>{c.sub}</span>
                </div>
                <div className="stats">
                  {c.stats.map(([k, v]) => (
                    <div key={k} style={{ minWidth: 0 }}>
                      <div className="k">{k}</div>
                      <div className="v">{v}</div>
                    </div>
                  ))}
                </div>
              </div>
            ))}
          </div>
        )}

        <div className="helix-feeds">
          <div className="card">
            <div className="card-head">
              <span className="card-title">Latest blocks</span>
              <Link to="/blocks" style={{ fontSize: 12, marginLeft: "auto" }}>
                All blocks →
              </Link>
            </div>
            {blocks.length === 0 && <div className="empty">{helix.loading ? "Loading…" : "No blocks yet."}</div>}
            {blocks.map((b) => {
              const c = b.chain === "aether" ? AETHER_COLOR : IBC_COLOR;
              return (
                <div key={b.chain + b.height} className="strand-block">
                  <div className="tile" style={b.head ? { borderColor: b.chain === "aether" ? "rgba(192,80,58,.6)" : "rgba(228,217,198,.5)" } : undefined}>
                    <i style={{ height: `${Math.max(10, (b.numTxs / maxTxs) * 100)}%`, background: b.chain === "aether" ? "rgba(192,80,58,.75)" : "rgba(228,217,198,.55)" }} />
                  </div>
                  <div style={{ minWidth: 0 }}>
                    <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                      {b.chain === "aether" ? (
                        <Link to={`/blocks/${b.height}`} className="mono" style={{ fontWeight: 600, color: c }}>
                          {int(b.height)}
                        </Link>
                      ) : (
                        <span className="mono" style={{ fontWeight: 600, color: c }}>
                          {int(b.height)}
                        </span>
                      )}
                      <span className="chain-tag">
                        <i style={{ background: c }} />
                        {b.chain === "aether" ? "Aether" : name}
                      </span>
                    </div>
                    <div className="mono muted ellipsis" style={{ fontSize: 12, marginTop: 3 }}>
                      {shortBlockHash(b.hash)} · {proposerLabel(b)}
                    </div>
                  </div>
                  <div className="right">
                    <div style={{ fontSize: 13 }}>
                      {b.numTxs} tx{b.numTxs === 1 ? "" : "s"}
                    </div>
                    <div className="muted" style={{ fontSize: 12, marginTop: 3 }}>
                      {timeAgo(b.time)}
                    </div>
                  </div>
                </div>
              );
            })}
          </div>

          {view === "ibc" ? (
            <div className="card">
              <div className="card-head">
                <span className="card-title">Packets</span>
                <span className="card-meta">last {WINDOW}s</span>
              </div>
              {h && h.packets.length === 0 && <div className="empty">No packets between the chains in the last {WINDOW}s.</div>}
              {h?.packets.slice(0, 8).map((p) => (
                <div key={packetKey(p)} className="row" style={{ gridTemplateColumns: "minmax(0,1fr) 160px 110px 64px", gap: 14, padding: "14px 20px", fontSize: 13 }}>
                  <span className="mono dim ellipsis">
                    #{int(p.sequence)} · {p.direction === "out" ? `Aether → ${name}` : `${name} → Aether`}
                  </span>
                  <span className="mono dim ellipsis" style={{ fontSize: 12 }}>
                    {packetAmount(p)}
                  </span>
                  <span>
                    <span className={`status-tag ${p.status}`}>{STATUS_LABEL[p.status]}</span>
                  </span>
                  <span className="right muted">{timeAgo(packetTs(p))}</span>
                </div>
              ))}
            </div>
          ) : (
            <div className="card">
              <div className="card-head">
                <span className="card-title">Recent transactions</span>
                <span className="card-meta">Aether</span>
              </div>
              <ErrorBanner error={recent.error} />
              {recent.data && recent.data.length === 0 && <div className="empty">No transactions yet.</div>}
              {recent.data?.slice(0, 7).map((t) => (
                <div key={t.hash} className="row hover" style={{ gridTemplateColumns: "minmax(0,1fr) 170px 110px 64px", gap: 14, padding: "14px 20px", fontSize: 13 }}>
                  <div style={{ display: "flex", alignItems: "center", gap: 10, minWidth: 0 }}>
                    <span style={{ flex: "none", width: 8, height: 8, borderRadius: "50%", background: AETHER_COLOR }} />
                    <TxHash hash={t.hash} copy={false} />
                  </div>
                  <div className="ellipsis">
                    <MsgTag type={t.msgType} />
                  </div>
                  <span className="mono muted" style={{ fontSize: 12 }}>
                    block <Link to={`/blocks/${t.height}`} style={{ color: "var(--text-2)" }}>{int(t.height)}</Link>
                  </span>
                  <span className="right muted">{timeAgo(t.timestamp)}</span>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </>
  );
}
