import { useEffect, useRef, useState } from "react";
import type { Helix, HelixBlock, HelixPacket } from "../api";
import { AETHER_COLOR, IBC_COLOR, type ChainView } from "../chain";
import { aeth, amountIn, int } from "../format";

// The two chains drawn as the strands of a helix along a time axis:
// Aether's blocks on one strand, the IBC chain's on the other, and each
// packet relayed between them as a glowing rung at the time it was sent.
// Geometry follows the Claude Design mockup (turn 5).

const MONO = "'JetBrains Mono', monospace";

/** Seconds since the epoch on the API's clock, ticking once a second. */
export function useChainClock(helix: Helix | null): number {
  const offset = useRef(0);
  useEffect(() => {
    if (helix) offset.current = new Date(helix.now).getTime() - Date.now();
  }, [helix]);
  const [now, setNow] = useState(() => Date.now() / 1000);
  useEffect(() => {
    const id = setInterval(() => setNow((Date.now() + offset.current) / 1000), 1000);
    return () => clearInterval(id);
  }, []);
  return now;
}

/** True on phone-width screens, where the helix is drawn shorter so it stays tall enough to read. */
export function useNarrow(): boolean {
  const query = "(max-width: 700px)";
  const [narrow, setNarrow] = useState(() => typeof window !== "undefined" && window.matchMedia(query).matches);
  useEffect(() => {
    const m = window.matchMedia(query);
    const on = () => setNarrow(m.matches);
    m.addEventListener("change", on);
    return () => m.removeEventListener("change", on);
  }, []);
  return narrow;
}

export const ts = (iso: string) => new Date(iso).getTime() / 1000;

/** When a packet first shows up: sent if seen, else received. */
export function packetTs(p: HelixPacket): number {
  const s = p.sent ?? p.received ?? p.acked ?? p.timedOut;
  return s ? ts(s.time) : 0;
}

/**
 * A strand's front and back halves as SVG paths, sampled at steps:
 * point(t) is the "x y" there, front(t) whether the strand is in front.
 */
function strandPaths(steps: number[], point: (t: number) => string, front: (t: number) => boolean): [string, string] {
  let f = "";
  let b = "";
  let prev: boolean | null = null;
  for (const t of steps) {
    const p = point(t) + " ";
    const fr = front(t);
    if (prev === null) {
      if (fr) f += "M" + p;
      else b += "M" + p;
    } else if (prev === fr) {
      if (fr) f += "L" + p;
      else b += "L" + p;
    } else if (prev) {
      f += "L" + p;
      b += "M" + p;
    } else {
      b += "L" + p;
      f += "M" + p;
    }
    prev = fr;
  }
  return [f, b];
}

interface HelixHeroProps {
  helix: Helix;
  now: number;
  view: ChainView;
  width: number;
  height: number;
  windowSecs: number;
}

/** The horizontal helix: now at the right, time flowing left. */
export function HelixHero({ helix, now, view, width: W, height: H, windowSecs }: HelixHeroProps) {
  const cy = H / 2;
  const A = H * 0.3;
  const L = W / 3.6;
  const pad = 84;
  const pps = (W - pad - 10) / windowSecs;
  const nowX = W - pad;
  const X = (t: number) => nowX - (now - t) * pps;
  const th = (x: number) => (2 * Math.PI * x) / L;
  const sn = (x: number) => Math.sin(th(x));
  const xs: number[] = [];
  for (let x = 0; x <= W; x += 4) xs.push(x);
  const [aF, aB] = strandPaths(xs, (x) => `${x} ${(cy + A * sn(x)).toFixed(1)}`, (x) => Math.cos(th(x)) > 0);
  const [bF, bB] = strandPaths(xs, (x) => `${x} ${(cy - A * sn(x)).toFixed(1)}`, (x) => -Math.cos(th(x)) > 0);
  let rungs = "";
  for (let x = 6; x < W; x += 14) rungs += `M${x} ${(cy + A * sn(x)).toFixed(1)} L${x} ${(cy - A * sn(x)).toFixed(1)} `;

  const hasIbc = !!helix.ibc;
  const dA = view === "ibc" ? 0.12 : 1;
  const dB = view === "aether" ? 0.12 : 1;
  const fade = { transition: "opacity .4s" };
  const move = "transform 1s linear, opacity .4s";
  const ibcName = helix.ibc?.name ?? "IBC";

  const dots: JSX.Element[] = [];
  const strand = (chain: "aether" | "ibc") => {
    const s = chain === "aether" ? helix.aether : helix.ibc;
    if (!s) return;
    const sign = chain === "aether" ? 1 : -1;
    const c = chain === "aether" ? AETHER_COLOR : IBC_COLOR;
    s.blocks.forEach((b, i) => {
      const x = X(ts(b.time));
      if (x < -12 || x > W) return;
      const z = sign * Math.cos(th(x));
      const r = 3.6 + (2.8 * (z + 1)) / 2;
      dots.push(
        <g key={chain + b.height} style={{ transform: `translate(${x}px,${cy + sign * A * sn(x)}px)`, transition: move, opacity: chain === "aether" ? dA : dB }}>
          {i === 0 && <circle r={r + 7} fill={c} opacity={0.2} />}
          <circle r={r} fill={z > 0 ? c : "#0e0c0b"} stroke={c} strokeWidth={z > 0 ? 0 : 1.5} />
          <title>{`${chain === "aether" ? "Aether" : ibcName} #${int(b.height)} · ${b.numTxs} tx${b.numTxs === 1 ? "" : "s"}`}</title>
        </g>,
      );
    });
  };
  strand("aether");
  strand("ibc");

  const rungEls = helix.packets
    .map((p) => ({ p, x: X(packetTs(p)) }))
    .filter(({ x }) => x > -10 && x <= nowX + 1)
    .map(({ p, x }) => {
      const s = A * sn(x) || 0.01;
      return (
        <g key={packetKey(p)} style={{ transform: `translate(${x}px,${cy}px)`, transition: move, opacity: view === "both" ? 1 : 0.3 }}>
          <line x1={0} x2={0} y1={-s} y2={s} stroke="#f3efe6" strokeWidth={2.5} style={{ filter: `drop-shadow(0 0 4px ${AETHER_COLOR})` }} />
          <title>{`IBC packet #${p.sequence} · ${p.direction === "out" ? `Aether → ${ibcName}` : `${ibcName} → Aether`} · ${p.status}`}</title>
        </g>
      );
    });

  return (
    <svg viewBox={`0 0 ${W} ${H}`} className="helix-svg" role="img" aria-label={hasIbc ? `Aether and ${ibcName} blocks over the last ${windowSecs} seconds` : `Aether blocks over the last ${windowSecs} seconds`}>
      {hasIbc && <path d={rungs} stroke="#2a2521" strokeWidth={1} fill="none" style={{ ...fade, opacity: view === "both" ? 1 : 0.45 }} />}
      <path d={aB} stroke={AETHER_COLOR} strokeOpacity={0.3} strokeWidth={2} fill="none" style={{ ...fade, opacity: dA }} />
      {hasIbc && <path d={bB} stroke={IBC_COLOR} strokeOpacity={0.25} strokeWidth={2} fill="none" style={{ ...fade, opacity: dB }} />}
      <path d={aF} stroke={AETHER_COLOR} strokeWidth={3} fill="none" style={{ ...fade, opacity: dA, filter: "drop-shadow(0 0 5px rgba(192,80,58,.55))" }} />
      {hasIbc && <path d={bF} stroke={IBC_COLOR} strokeWidth={3} fill="none" style={{ ...fade, opacity: dB }} />}
      <path d={aF} className="helix-flow" stroke="#f3efe6" strokeOpacity={0.4} strokeWidth={1} strokeDasharray="2 10" fill="none" style={{ opacity: dA }} />
      {hasIbc && <path d={bF} className="helix-flow" stroke="#0a0908" strokeOpacity={0.5} strokeWidth={1} strokeDasharray="2 10" fill="none" style={{ opacity: dB }} />}
      <line x1={nowX} x2={nowX} y1={6} y2={H - 18} stroke="#4a443d" strokeDasharray="3 4" />
      <text x={nowX} y={H - 3} fill="#8f877c" fontSize={11} fontFamily={MONO} textAnchor="middle">
        now
      </text>
      {[30, 60, 90, 120, 180, 240]
        .filter((s) => s < windowSecs)
        .map((s) => (
          <text key={s} x={nowX - s * pps} y={H - 3} fill="#4a443d" fontSize={11} fontFamily={MONO} textAnchor="middle">
            −{s}s
          </text>
        ))}
      {rungEls}
      {dots}
      <text x={nowX + 14} y={cy + A * sn(nowX) + 4} fill={AETHER_COLOR} fontSize={12} fontWeight={600} fontFamily={MONO} style={{ ...fade, opacity: dA }}>
        #{int(helix.aether.height)}
      </text>
      {helix.ibc && (
        <text x={nowX + 14} y={cy - A * sn(nowX) + 4} fill={IBC_COLOR} fontSize={12} fontWeight={600} fontFamily={MONO} style={{ ...fade, opacity: dB }}>
          #{int(helix.ibc.height)}
        </text>
      )}
    </svg>
  );
}

/**
 * One row's slice of a vertical helix: rows stack into one continuous
 * pair of strands, a full turn every five rows.
 */
export function vSegment(row: number, rowH: number, width: number, amp: number, single: boolean) {
  const cx = width / 2;
  const Lv = rowH * 5;
  const th = (y: number) => (2 * Math.PI * (row * rowH + y)) / Lv;
  const ys: number[] = [];
  for (let y = -2; y <= rowH + 2; y += 3) ys.push(y);
  const seg = (sign: number) =>
    strandPaths(ys, (y) => `${(cx + sign * amp * Math.sin(th(y))).toFixed(1)} ${y}`, (y) => sign * Math.cos(th(y)) > 0);
  const [aF, aB] = seg(1);
  const [bF, bB] = single ? ["", ""] : seg(-1);
  let rungs = "";
  if (!single) {
    for (let gy = Math.ceil((row * rowH) / 12) * 12; gy < (row + 1) * rowH; gy += 12) {
      const y = gy - row * rowH;
      const s = amp * Math.sin(th(y));
      rungs += `M${(cx + s).toFixed(1)} ${y} L${(cx - s).toFixed(1)} ${y} `;
    }
  }
  const at = (sign: number) => ({ x: +(cx + sign * amp * Math.sin(th(rowH / 2))).toFixed(1), z: sign * Math.cos(th(rowH / 2)) });
  return { aF, aB, bF, bB, rungs, at };
}

/** A block on either strand, for the feeds that mix both. */
export interface StrandBlock extends HelixBlock {
  chain: "aether" | "ibc";
  head: boolean;
}

/** Both strands' blocks, newest first, limited to the chains in view. */
export function mergedBlocks(helix: Helix, view: ChainView): StrandBlock[] {
  const out: StrandBlock[] = [];
  if (view !== "ibc") helix.aether.blocks.forEach((b, i) => out.push({ ...b, chain: "aether", head: i === 0 }));
  if (view !== "aether" && helix.ibc) helix.ibc.blocks.forEach((b, i) => out.push({ ...b, chain: "ibc", head: i === 0 }));
  return out.sort((a, b) => ts(b.time) - ts(a.time) || (a.chain < b.chain ? -1 : 1));
}

/**
 * What a packet carries: AETH (native, or back on its way home as a
 * transfer/channel-N/uaeth voucher) in AETH, anything else by its denom.
 */
export function packetAmount(p: HelixPacket): string {
  if (!p.amount || !p.denom) return p.srcPort === "transfer" ? "transfer" : p.srcPort;
  if (p.denom === "uaeth" || p.denom.endsWith("/uaeth")) return aeth(p.amount);
  return amountIn(p.amount, p.denom);
}

/** A packet's identity: both ends of a channel are often channel-0, so the direction is part of it. */
export function packetKey(p: HelixPacket): string {
  return `${p.direction}/${p.srcPort}/${p.srcChannel}/${p.sequence}`;
}

/** "1 tx", "3 txs". */
export function txCount(n: number): string {
  return `${n} tx${n === 1 ? "" : "s"}`;
}

/** Seconds from send to receipt, when both are in view. */
export function relaySecs(p: HelixPacket): number | null {
  if (!p.sent || !p.received) return null;
  const s = ts(p.received.time) - ts(p.sent.time);
  return s >= 0 ? s : null;
}

export const STATUS_LABEL: Record<HelixPacket["status"], string> = {
  "in-flight": "IN FLIGHT",
  received: "RECEIVED",
  acked: "ACKED",
  "timed-out": "TIMED OUT",
};

/** "6s" for a measured block time, "—" before there are two blocks. */
export function blockTimeLabel(secs: number): string {
  if (!secs) return "—";
  return secs >= 10 ? `${Math.round(secs)}s` : `${secs.toFixed(1).replace(/\.0$/, "")}s`;
}

/** The short form of a block's proposer: a miner account on Aether, a consensus address elsewhere. */
export function proposerLabel(b: HelixBlock): string {
  const p = b.proposer;
  if (!p) return "—";
  return p.length > 16 ? `${p.slice(0, p.startsWith("aether1") ? 11 : 6)}…${p.slice(-5)}` : p;
}

export function shortBlockHash(hash: string): string {
  return `${hash.slice(0, 4)}…${hash.slice(-4)}`;
}
