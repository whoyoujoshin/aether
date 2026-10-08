import { Link } from "react-router-dom";
import { api, type Helix, type HelixPacket } from "../api";
import { useApi } from "../hooks";
import { AETHER_COLOR, IBC_COLOR, useChain } from "../chain";
import { ErrorBanner } from "../components/ui";
import {
  HelixHero,
  STATUS_LABEL,
  blockTimeLabel,
  mergedBlocks,
  packetAmount,
  packetKey,
  packetTs,
  proposerLabel,
  relaySecs,
  shortBlockHash,
  ts,
  txCount,
  useChainClock,
  useNarrow,
  vSegment,
  type StrandBlock,
} from "../components/Helix";
import { int, timeAgo } from "../format";

const RH = 58; // row height, px
const HERO_WINDOW = 60;
const ROWS = 24;

type Row = { kind: "block"; b: StrandBlock; t: number } | { kind: "packet"; p: HelixPacket; t: number };

/** Both strands and their packets, newest first, as one timeline. */
function timeline(h: Helix): Row[] {
  const rows: Row[] = mergedBlocks(h, "both").map((b) => ({ kind: "block", b, t: ts(b.time) }));
  for (const p of h.packets) rows.push({ kind: "packet", p, t: packetTs(p) });
  // A packet sorts above the block it arrived in.
  return rows.sort((a, b) => b.t - a.t || (a.kind === "packet" ? -1 : 1)).slice(0, ROWS);
}

function StepLink({ step, children }: { step?: HelixPacket["sent"]; children: React.ReactNode }) {
  if (step?.chain === "aether" && step.txHash) return <Link to={`/tx/${step.txHash}`}>{children}</Link>;
  return <>{children}</>;
}

/** The two sides of a packet row: what left, and where it got to. */
function packetSides(p: HelixPacket, name: string) {
  const relay = relaySecs(p);
  const recv = p.received ? `recv #${int(p.received.height)}${relay !== null ? ` · ${Math.round(relay)}s` : ""}` : null;
  const what = `packet #${int(p.sequence)} · ${packetAmount(p)}`;
  if (p.direction === "out") {
    return {
      left: <StepLink step={p.sent}>{what} →</StepLink>,
      leftColor: AETHER_COLOR,
      right: recv ? `→ ${recv}` : p.status === "timed-out" ? "timed out" : `relaying to ${name} →`,
      rightColor: "var(--muted)",
    };
  }
  return {
    left: recv ? <StepLink step={p.received}>← {recv}</StepLink> : p.status === "timed-out" ? "timed out" : "← relaying to Aether",
    leftColor: "var(--muted)",
    right: `← ${what}`,
    rightColor: IBC_COLOR,
  };
}

function Timeline({ h }: { h: Helix }) {
  const name = h.ibc?.name ?? "IBC";
  const rows = timeline(h);
  return (
    <div className="card gap-top scroll-x" style={{ marginTop: 18 }}>
      <div className="timeline">
        <div className="tl-head">
          <span style={{ textAlign: "right", padding: "0 20px", color: AETHER_COLOR }}>AETHER · POW · {blockTimeLabel(h.aether.blockTimeSecs).toUpperCase()}</span>
          <span style={{ textAlign: "center", color: "var(--faint)" }}>PACKETS</span>
          <span style={{ padding: "0 20px", color: IBC_COLOR, opacity: h.ibc ? 1 : 0.45 }}>
            {h.ibc ? `${name.toUpperCase()} · BFT · ${blockTimeLabel(h.ibc.blockTimeSecs).toUpperCase()}` : "NO IBC CHAIN CONNECTED"}
          </span>
        </div>
        {rows.length === 0 && <div className="empty">No blocks yet.</div>}
        {rows.map((r, i) => {
          const g = vSegment(i, RH, 120, 34, false);
          if (r.kind === "packet") {
            const p = r.p;
            const s = packetSides(p, name);
            const a = g.at(1).x;
            const b = g.at(-1).x;
            return (
              <div key={packetKey(p)} className="tl-row packet">
                <div className="side left">
                  <span className="mono" style={{ fontSize: 12, fontWeight: 500, color: s.leftColor }}>
                    {s.left}
                  </span>
                </div>
                <Spine g={g} rung={`M${a} 29 L${b} 29`} />
                <div className="side">
                  <span className="mono" style={{ fontSize: 12, fontWeight: 500, color: s.rightColor }}>
                    {s.right}
                  </span>
                  <span className={`status-tag ${p.status}`}>{STATUS_LABEL[p.status]}</span>
                </div>
              </div>
            );
          }
          const blk = r.b;
          const isA = blk.chain === "aether";
          const n = g.at(isA ? 1 : -1);
          const c = isA ? AETHER_COLOR : IBC_COLOR;
          const pk = blk.packets ? `${blk.packets} pkt` : "";
          return (
            <div key={blk.chain + blk.height} className="tl-row">
              <div className="side left">
                {isA && (
                  <>
                    <span className="muted">{timeAgo(blk.time)}</span>
                    {pk && <span className="mono" style={{ color: "var(--muted)" }}>{pk}</span>}
                    <span className="mono" style={{ color: "var(--muted)" }}>{txCount(blk.numTxs)}</span>
                    <span className="mono muted" style={{ fontSize: 12 }}>{proposerLabel(blk)}</span>
                    <span className="mono faint" style={{ fontSize: 12 }}>{shortBlockHash(blk.hash)}</span>
                    <Link to={`/blocks/${blk.height}`} className="hgt" style={{ color: AETHER_COLOR }}>
                      {int(blk.height)}
                    </Link>
                  </>
                )}
              </div>
              <Spine g={g} node={{ x: n.x, r: (blk.head ? 7.5 : 5) + (n.z > 0 ? 1 : 0), fill: n.z > 0 ? c : "#12100e", stroke: c }} />
              <div className="side">
                {!isA && (
                  <>
                    <span className="hgt" style={{ color: IBC_COLOR }}>{int(blk.height)}</span>
                    <span className="mono faint" style={{ fontSize: 12 }}>{shortBlockHash(blk.hash)}</span>
                    <span className="mono muted" style={{ fontSize: 12 }}>{proposerLabel(blk)}</span>
                    <span className="mono" style={{ color: "var(--muted)" }}>{txCount(blk.numTxs)}</span>
                    {pk && <span className="mono" style={{ color: "var(--muted)" }}>{pk}</span>}
                    <span className="muted">{timeAgo(blk.time)}</span>
                  </>
                )}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

function Spine({ g, rung, node }: { g: ReturnType<typeof vSegment>; rung?: string; node?: { x: number; r: number; fill: string; stroke: string } }) {
  return (
    <svg viewBox={`0 0 120 ${RH}`} className="spine" aria-hidden="true">
      <path d={g.rungs} stroke="#2a2521" strokeWidth={1} fill="none" />
      <path d={g.aB} stroke={AETHER_COLOR} strokeOpacity={0.3} strokeWidth={2} fill="none" />
      <path d={g.bB} stroke={IBC_COLOR} strokeOpacity={0.25} strokeWidth={2} fill="none" />
      <path d={g.aF} stroke={AETHER_COLOR} strokeWidth={3} fill="none" />
      <path d={g.bF} stroke={IBC_COLOR} strokeWidth={3} fill="none" />
      {rung && <path d={rung} stroke="#f3efe6" strokeWidth={2.5} fill="none" style={{ filter: `drop-shadow(0 0 4px ${AETHER_COLOR})` }} />}
      {node && <circle cx={node.x} cy={29} r={node.r} fill={node.fill} stroke={node.stroke} strokeWidth={2} />}
    </svg>
  );
}

/** One chain's own table, its strand running down the side. */
function StrandTable({ h, chain }: { h: Helix; chain: "aether" | "ibc" }) {
  const s = chain === "aether" ? h.aether : h.ibc;
  const color = chain === "aether" ? AETHER_COLOR : IBC_COLOR;
  const blocks = (s?.blocks ?? []).slice(0, 20);
  return (
    <div className="card scroll-x" style={{ marginTop: 18 }}>
      <div className="strand-table">
        <div className="st-row th">
          <span />
          <span>Height</span>
          <span>Hash</span>
          <span>{chain === "aether" ? "Miner" : "Proposer"}</span>
          <span className="right">Txs</span>
          <span className="right">IBC packets</span>
          <span className="right">Age</span>
        </div>
        {blocks.length === 0 && <div className="empty">No blocks yet.</div>}
        {blocks.map((b, i) => {
          const g = vSegment(i, RH, 44, 12, true);
          const n = g.at(1);
          return (
            <div key={b.height} className="st-row body">
              <svg viewBox={`0 0 44 ${RH}`} style={{ width: 44, height: RH, display: "block", overflow: "visible" }} aria-hidden="true">
                <path d={g.aB} stroke={color} strokeOpacity={0.3} strokeWidth={2} fill="none" />
                <path d={g.aF} stroke={color} strokeWidth={3} fill="none" />
                <circle cx={n.x} cy={29} r={i === 0 ? 7 : 5} fill={n.z > 0 ? color : "#12100e"} stroke={color} strokeWidth={2} />
              </svg>
              {chain === "aether" ? (
                <Link to={`/blocks/${b.height}`} className="hgt mono" style={{ color, fontWeight: 600, fontSize: 15 }}>
                  {int(b.height)}
                </Link>
              ) : (
                <span className="mono" style={{ color, fontWeight: 600, fontSize: 15 }}>
                  {int(b.height)}
                </span>
              )}
              <span className="mono muted ellipsis" style={{ fontSize: 12 }} title={b.hash}>
                {b.hash.slice(0, 10)}…{b.hash.slice(-6)}
              </span>
              <span className="mono dim ellipsis" style={{ fontSize: 12 }} title={b.proposer}>
                {chain === "aether" && b.proposer ? <Link to={`/address/${b.proposer}`} style={{ color: "var(--text-2)" }}>{proposerLabel(b)}</Link> : proposerLabel(b)}
              </span>
              <span className="right mono">{b.numTxs}</span>
              <span className="right mono" style={{ color: b.packets ? "var(--bone)" : "var(--icon)" }}>
                {b.packets || "—"}
              </span>
              <span className="right muted">{timeAgo(b.time)}</span>
            </div>
          );
        })}
      </div>
    </div>
  );
}

/** Blocks with both chains (mockup 5b): one timeline, or a chain's own table. */
export default function HelixBlocks() {
  const { view, ibcName, peer } = useChain();
  const helix = useApi(() => api.helix(150, 20, peer), [peer], 4000);
  const now = useChainClock(helix.data);
  const narrow = useNarrow();
  const h = helix.data;
  const name = h?.ibc?.name ?? ibcName ?? "IBC";
  const sub =
    view === "both"
      ? "Both strands on one timeline, newest first. Rungs are IBC packets between the chains."
      : view === "aether"
        ? `Aether blocks, signed by its validators, one every ~${h ? blockTimeLabel(h.aether.blockTimeSecs) : "6s"}. Miners' proof-of-work arrives inside them as transactions.`
        : `${name} blocks, BFT, one every ~${h?.ibc ? blockTimeLabel(h.ibc.blockTimeSecs) : "—"}.`;

  return (
    <>
      <div className="page">
        <div className="page-head" style={{ display: "flex", alignItems: "flex-end", gap: 18, flexWrap: "wrap" }}>
          <div>
            <h1 className="page-title">Blocks</h1>
            <div className="page-sub">{sub}</div>
          </div>
          <div className="helix-heads">
            <div style={{ opacity: view === "ibc" ? 0.3 : 1 }}>
              <div className="k">
                <i style={{ background: AETHER_COLOR }} />
                Aether head
              </div>
              <div className="v" style={{ color: AETHER_COLOR }}>{h ? int(h.aether.height) : "—"}</div>
            </div>
            {h?.ibc && (
              <div style={{ opacity: view === "aether" ? 0.3 : 1 }}>
                <div className="k">
                  <i style={{ background: IBC_COLOR }} />
                  {name} head
                </div>
                <div className="v" style={{ color: IBC_COLOR }}>{int(h.ibc.height)}</div>
              </div>
            )}
          </div>
        </div>
        <ErrorBanner error={helix.error} prefix="Failed to load the chains" />
        {h?.errors?.ibc && <ErrorBanner error={h.errors.ibc} prefix={`Couldn't read ${name}`} />}

        <div className="helix-hero slim">
          {h ? <HelixHero helix={h} now={now} view={view} width={narrow ? 560 : 1176} height={120} windowSecs={HERO_WINDOW} /> : <div className="loading">Loading…</div>}
        </div>

        {h && (view === "both" ? <Timeline h={h} /> : <StrandTable h={h} chain={view} />)}
      </div>
    </>
  );
}
