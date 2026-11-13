import { useEffect, useId, useRef, useState } from "react";
import type { Helix, HelixBlock, HelixPacket } from "../api";
import { AETHER_COLOR, IBC_COLOR, type ChainView } from "../chain";
import { aeth, amountIn, int } from "../format";

// The two chains drawn as the strands of a helix along a time axis:
// Aether's blocks on one strand, an IBC counterparty's on the other, and
// each packet between them as a rung. The tubes, base pairs and the
// Helicase / Ligase packet motion follow the latest explorer mockup.
// Heights, times and amounts come from /api/helix.

const MONO = "'JetBrains Mono', monospace";

/** Seconds since the epoch on the API's clock, ticking once a second. */
export function useChainClock(helix: Helix | null): number {
  const offset = useRef(0);
  useEffect(() => {
    if (helix) offset.current = new Date(helix.now).getTime() - Date.now();
  }, [helix]);
  const [now, setNow] = useState(() => Date.now() / 1000);
  useEffect(() => {
    const id = setInterval(() => setNow((Date.now() + offset.current) / 1000), 250);
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
  /** How many turns the strands make across the width. Overview uses 2, the blocks strip 1.6. */
  turns?: number;
}

/** One line under the helix: the packet Helicase is carrying, or Ligase just sealed, or idle. */
export function bridgeCaption(helix: Helix, now: number): { text: string; color: string } {
  const name = helix.ibc?.name || "IBC";
  let best: { t: number; kind: "send" | "seal"; p: HelixPacket } | null = null;
  for (const p of helix.packets) {
    const sent = p.sent ? ts(p.sent.time) : packetTs(p);
    if (sent && sent <= now && now - sent < 3.5 && (!best || sent > best.t)) best = { t: sent, kind: "send", p };
    const sealedAt = p.received ? ts(p.received.time) : p.acked ? ts(p.acked.time) : 0;
    if (sealedAt && sealedAt <= now && now - sealedAt < 3.5 && (!best || sealedAt > best.t)) best = { t: sealedAt, kind: "seal", p };
  }
  const inflight = helix.bridge?.inFlight ?? helix.packets.filter((p) => p.status === "in-flight").length;
  if (!best) return { text: `bridge idle · ${inflight} in flight`, color: "#6b645b" };
  const amt = packetAmount(best.p);
  const from = best.p.direction === "out" ? "Aether" : name;
  const to = best.p.direction === "out" ? name : "Aether";
  if (best.kind === "send") {
    return { text: `Helicase · packet #${int(best.p.sequence)} unwinding · ${amt} ${from} → ${to}`, color: "#e98a72" };
  }
  const height = best.p.received?.height ?? best.p.acked?.height;
  return { text: `Ligase · packet #${int(best.p.sequence)} sealed on ${to}${height ? ` at #${int(height)}` : ""}`, color: "#f3efe6" };
}

/** The horizontal helix: now at the right, time flowing left. */
export function HelixHero({ helix, now, view, width: W, height: H, windowSecs, turns = 2 }: HelixHeroProps) {
  const uid = useId().replace(/:/g, "");
  const id = (k: string) => `${uid}-${k}`;
  const cy = H / 2;
  const A = H * 0.31;
  const pad = 84;
  const x0 = 8;
  const L = (W - pad - x0) / turns;
  const pps = (W - pad - 10) / windowSecs;
  const nowX = W - pad;
  const X = (t: number) => nowX - (now - t) * pps;
  const th = (x: number) => (2 * Math.PI * (x - x0)) / L;
  const Y = (x: number, sign: number) => cy + sign * A * Math.sin(th(x));
  const Z = (x: number, sign: number) => sign * Math.cos(th(x));
  const xs: number[] = [];
  for (let x = x0; x <= W - 6; x += 1.5) xs.push(x);
  const [aF, aB] = strandPaths(xs, (x) => `${x.toFixed(1)} ${Y(x, 1).toFixed(2)}`, (x) => Z(x, 1) > 0);
  const [bF, bB] = strandPaths(xs, (x) => `${x.toFixed(1)} ${Y(x, -1).toFixed(2)}`, (x) => Z(x, -1) > 0);

  const hasIbc = !!helix.ibc && helix.ibc.height > 0;
  const dA = view === "ibc" ? 0.12 : 1;
  const dB = view === "aether" ? 0.12 : 1;
  const fade = { transition: "opacity .4s" };
  const move = "transform 1s linear, opacity .4s";
  const ibcName = helix.ibc?.name ?? "IBC";
  const relayFallback = helix.bridge?.avgRelaySecs && helix.bridge.avgRelaySecs > 0 ? helix.bridge.avgRelaySecs : 8;

  const pairs: JSX.Element[] = [];
  for (let x = x0 + 4; x < W - 8; x += 8) {
    const ya = Y(x, 1);
    const yb = Y(x, -1);
    const sp = Math.abs(ya - yb);
    if (sp < 5) continue;
    const mid = (ya + yb) / 2;
    const op = 0.1 + 0.32 * (sp / (2 * A));
    pairs.push(
      <g key={x} opacity={op}>
        <line x1={x} x2={x} y1={ya} y2={mid} stroke={AETHER_COLOR} strokeWidth={1.6} strokeLinecap="round" style={{ ...fade, opacity: dA }} />
        <line x1={x} x2={x} y1={mid} y2={yb} stroke={IBC_COLOR} strokeWidth={1.6} strokeLinecap="round" style={{ ...fade, opacity: hasIbc ? dB : 0.35 }} />
      </g>,
    );
  }

  const tube = (d: string, key: string, grad: string, glow: string | null, op: number, hi: number) => (
    <g key={key}>
      {glow && <path d={d} stroke={glow} strokeOpacity={0.55} strokeWidth={9} fill="none" strokeLinecap="round" filter={`url(#${id("glow")})`} style={{ ...fade, opacity: op }} />}
      <path d={d} stroke="#0a0908" strokeWidth={7} fill="none" strokeLinecap="round" strokeLinejoin="round" style={{ ...fade, opacity: op * 0.9 }} />
      <path d={d} stroke={`url(#${grad})`} strokeWidth={4.5} fill="none" strokeLinecap="round" strokeLinejoin="round" style={{ ...fade, opacity: op }} />
      <path d={d} stroke="#fffaf2" strokeOpacity={hi} strokeWidth={1.1} fill="none" strokeLinecap="round" transform="translate(0 -1.3)" style={{ ...fade, opacity: op }} />
    </g>
  );

  const nodes = (front: boolean) => {
    const out: JSX.Element[] = [];
    const strand = (chain: "aether" | "ibc") => {
      const s = chain === "aether" ? helix.aether : helix.ibc;
      if (!s) return;
      const sign = chain === "aether" ? 1 : -1;
      const isA = chain === "aether";
      const c = isA ? AETHER_COLOR : IBC_COLOR;
      s.blocks.forEach((b, i) => {
        const x = X(ts(b.time));
        if (x < -12 || x > W + 8) return;
        const z = Z(x, sign);
        if (z > 0 !== front) return;
        const head = i === 0;
        const r = (head ? 6.4 : 4.6) + (1.6 * (z + 1)) / 2;
        out.push(
          <g key={chain + b.height} style={{ transform: `translate(${x}px,${Y(x, sign)}px)`, transition: move, opacity: isA ? dA : dB }}>
            {head && <circle className="helix-halo" r={r + 4} fill={c} />}
            {front ? (
              <>
                <circle r={r + 2.2} fill={c} opacity={0.35} filter={`url(#${id("soft")})`} />
                <circle r={r} fill={`url(#${id(isA ? "nA" : "nB")})`} stroke="#0a0908" strokeWidth={1.2} />
              </>
            ) : (
              <circle r={r - 0.6} fill="#0e0c0b" stroke={c} strokeOpacity={0.6} strokeWidth={1.4} />
            )}
            <title>{`${isA ? "Aether" : ibcName} #${int(b.height)} · ${b.numTxs} tx${b.numTxs === 1 ? "" : "s"}`}</title>
          </g>,
        );
      });
    };
    strand("aether");
    if (hasIbc) strand("ibc");
    return out;
  };

  const packets = helix.packets
    .map((p) => ({ p, x: X(packetTs(p)) }))
    .filter(({ x }) => x > 0 && x <= nowX + 1)
    .map(({ p, x }) => {
      const ya = Y(x, 1) - cy;
      const yb = Y(x, -1) - cy;
      const outbound = p.direction === "out";
      const ys = outbound ? ya : yb;
      const yd = outbound ? yb : ya;
      const srcC = outbound ? AETHER_COLOR : IBC_COLOR;
      const dstC = outbound ? IBC_COLOR : AETHER_COLOR;
      const sentAt = p.sent ? ts(p.sent.time) : packetTs(p);
      const measured = relaySecs(p);
      const relay = measured && measured > 0.2 ? measured : relayFallback;
      const prog = Math.max(0, Math.min(1, (now - sentAt) / relay));
      const sealedAt = p.received ? ts(p.received.time) : p.acked ? ts(p.acked.time) : 0;
      const since = sealedAt ? now - sealedAt : relay;
      const inflight = p.status === "in-flight";
      const glow = `url(#${id("glow")})`;
      return (
        <g key={packetKey(p)} style={{ transform: `translate(${x}px,${cy}px)`, transition: move, opacity: view === "both" ? 1 : 0.3 }}>
          {inflight ? (
            <>
              <ellipse cx={0} cy={(ya + yb) / 2} rx={12} ry={Math.max(5, Math.abs(ya - yb) / 2 + 5)} fill={`url(#${id("bub")})`} />
              <line className="helix-flow" x1={0} x2={0} y1={ys} y2={yd} stroke="#fff7ee" strokeOpacity={0.55} strokeWidth={1.6} strokeDasharray="3 5" strokeLinecap="round" />
              <circle cx={0} cy={yd} r={7.5} fill="none" stroke={dstC} strokeOpacity={0.55} strokeWidth={1.2} strokeDasharray="2 3" />
              <g transform={`translate(0 ${ys.toFixed(2)})`}>
                <circle r={12} fill={srcC} opacity={0.3} filter={glow} />
                <ellipse rx={5.6} ry={8} fill={`url(#${id("hc")})`} stroke="#0a0908" strokeWidth={1} />
                <circle className="hc-spin" r={10.5} fill="none" stroke="#fff7ee" strokeWidth={1.4} strokeDasharray="4 3.5" strokeLinecap="round" />
              </g>
              <g style={{ transform: `translate(0px, ${(ys + (yd - ys) * prog).toFixed(2)}px)`, transition: "transform 0.25s linear" }}>
                <circle r={6.5} fill="#fff1e8" opacity={0.6} filter={glow} />
                <circle r={3.4} fill="#fff7ee" />
                <text x={10} y={3.6} fill="#f3efe6" fontSize={10.5} fontWeight={600} fontFamily={MONO} style={{ paintOrder: "stroke", stroke: "#0a0908", strokeWidth: 3 }}>
                  {packetAmount(p)}
                </text>
              </g>
            </>
          ) : (
            <>
              <line x1={0} x2={0} y1={ya} y2={yb} stroke="#ffe9df" strokeOpacity={0.7} strokeWidth={7} strokeLinecap="round" filter={glow} />
              <line x1={0} x2={0} y1={ya} y2={yb} stroke={Math.abs(ya - yb) > 4 ? `url(#${id("pk")})` : "#fff7ee"} strokeWidth={2.6} strokeLinecap="round" />
              <circle cx={0} cy={ys} r={2.4} fill="#fff7ee" />
              <rect x={-3.3} y={yd - 3.3} width={6.6} height={6.6} transform={`rotate(45 0 ${yd.toFixed(2)})`} fill="#fff7ee" stroke={dstC} strokeWidth={1.4} />
              {p.status !== "timed-out" && since < 2.6 && (
                <g transform={`translate(0 ${yd.toFixed(2)})`}>
                  <circle className="lg-seal" r={9} fill="none" stroke={dstC} strokeWidth={2.4} />
                  <circle className="lg-flash" r={8} fill="#fff7ee" />
                </g>
              )}
            </>
          )}
          <title>{`IBC packet #${p.sequence} · ${packetAmount(p)} · ${outbound ? `Aether → ${ibcName}` : `${ibcName} → Aether`} · ${inflight ? "helicase relaying" : p.status}`}</title>
        </g>
      );
    });

  const ticks: number[] = [];
  for (let s = 15; s < windowSecs; s += 15) ticks.push(s);

  return (
    <svg viewBox={`0 0 ${W} ${H}`} className="helix-svg" role="img" aria-label={hasIbc ? `Aether and ${ibcName} blocks over the last ${windowSecs} seconds` : `Aether blocks over the last ${windowSecs} seconds`}>
      <defs>
        <linearGradient id={id("fade")} x1="0" x2="1" y1="0" y2="0">
          <stop offset="0" stopColor="#fff" stopOpacity="0" />
          <stop offset="0.16" stopColor="#fff" stopOpacity="1" />
          <stop offset="1" stopColor="#fff" stopOpacity="1" />
        </linearGradient>
        <mask id={id("mask")} maskUnits="userSpaceOnUse" x="0" y="-20" width={W} height={H + 40}>
          <rect x="0" y="-20" width={W} height={H + 40} fill={`url(#${id("fade")})`} />
        </mask>
        <filter id={id("glow")} x="-20%" y="-60%" width="140%" height="220%">
          <feGaussianBlur stdDeviation="3.2" />
        </filter>
        <filter id={id("soft")} x="-50%" y="-50%" width="200%" height="200%">
          <feGaussianBlur stdDeviation="1.6" />
        </filter>
        <linearGradient id={id("tubeA")} x1="0" x2="0" y1="0" y2="1">
          <stop offset="0" stopColor="#e0735b" />
          <stop offset="1" stopColor="#9c3b28" />
        </linearGradient>
        <linearGradient id={id("tubeB")} x1="0" x2="0" y1="0" y2="1">
          <stop offset="0" stopColor="#fbf6ec" />
          <stop offset="1" stopColor="#b9ad99" />
        </linearGradient>
        <radialGradient id={id("nA")} cx="35%" cy="32%" r="70%">
          <stop offset="0" stopColor="#ffd9cc" />
          <stop offset="0.35" stopColor="#e2694f" />
          <stop offset="1" stopColor="#8f3423" />
        </radialGradient>
        <radialGradient id={id("nB")} cx="35%" cy="32%" r="70%">
          <stop offset="0" stopColor="#ffffff" />
          <stop offset="0.4" stopColor="#efe6d6" />
          <stop offset="1" stopColor="#a99d89" />
        </radialGradient>
        <linearGradient id={id("pk")} x1="0" x2="0" y1="0" y2="1">
          <stop offset="0" stopColor={AETHER_COLOR} />
          <stop offset="0.5" stopColor="#fff7ee" />
          <stop offset="1" stopColor={IBC_COLOR} />
        </linearGradient>
        <radialGradient id={id("bub")}>
          <stop offset="0" stopColor="#fff1e8" stopOpacity="0.28" />
          <stop offset="0.7" stopColor={AETHER_COLOR} stopOpacity="0.12" />
          <stop offset="1" stopColor={AETHER_COLOR} stopOpacity="0" />
        </radialGradient>
        <radialGradient id={id("hc")} cx="35%" cy="30%" r="75%">
          <stop offset="0" stopColor="#fff7ee" />
          <stop offset="0.45" stopColor="#e98a72" />
          <stop offset="1" stopColor="#7d2c1e" />
        </radialGradient>
      </defs>
      <line x1={x0} x2={nowX} y1={H - 14} y2={H - 14} stroke="#1f1b18" />
      {ticks.map((s) => (
        <g key={s}>
          <line x1={nowX - s * pps} x2={nowX - s * pps} y1={H - 16} y2={H - 12} stroke="#3a342e" />
          <text x={nowX - s * pps} y={H - 1} fill="#4a443d" fontSize={10.5} fontFamily={MONO} textAnchor="middle">
            −{s}s
          </text>
        </g>
      ))}
      <line x1={nowX} x2={nowX} y1={4} y2={H - 12} stroke="#4a443d" strokeDasharray="2 4" />
      <text x={nowX} y={H - 1} fill="#8f877c" fontSize={10.5} fontFamily={MONO} textAnchor="middle">
        now
      </text>
      <g mask={`url(#${id("mask")})`}>
        <path d={aB} stroke={AETHER_COLOR} strokeOpacity={0.32} strokeWidth={2.6} fill="none" strokeLinecap="round" style={{ ...fade, opacity: dA }} />
        <path
          d={bB}
          stroke={IBC_COLOR}
          strokeOpacity={hasIbc ? 0.22 : 0.16}
          strokeWidth={hasIbc ? 2.6 : 1.5}
          strokeDasharray={hasIbc ? undefined : "4 6"}
          fill="none"
          strokeLinecap="round"
          style={{ ...fade, opacity: hasIbc ? dB : 0.45 }}
        />
        {nodes(false)}
        {pairs}
        {tube(bF, "bf", id("tubeB"), null, hasIbc ? dB : 0.28, 0.6)}
        {tube(aF, "af", id("tubeA"), AETHER_COLOR, dA, 0.45)}
        {packets}
        {nodes(true)}
      </g>
      <text x={nowX + 14} y={Y(nowX, 1) + 4} fill={AETHER_COLOR} fontSize={12} fontWeight={600} fontFamily={MONO} style={{ ...fade, opacity: dA }}>
        #{int(helix.aether.height)}
      </text>
      {helix.ibc && helix.ibc.height > 0 ? (
        <text x={nowX + 14} y={Y(nowX, -1) + 4} fill={IBC_COLOR} fontSize={12} fontWeight={600} fontFamily={MONO} style={{ ...fade, opacity: helix.ibc.stale ? 0.55 : dB }}>
          #{int(helix.ibc.height)}
        </text>
      ) : helix.ibc ? (
        <text x={Math.min(nowX - 8, W - 12)} y={Y(nowX, -1) + 4} fill={IBC_COLOR} fontSize={11} fontFamily={MONO} textAnchor="end" style={{ ...fade, opacity: 0.55 }}>
          RPC unavailable
        </text>
      ) : (
        <text x={Math.min(nowX - 8, W - 12)} y={Y(nowX, -1) + 4} fill={IBC_COLOR} fontSize={11} fontFamily={MONO} textAnchor="end" style={{ ...fade, opacity: 0.45 }}>
          no IBC chain connected
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

/**
 * What to tell a person when the counterparty RPC missed. The raw 403
 * (and its "unexpected end of JSON") is a provider refusing this host,
 * not a broken chain, so it is not repeated.
 */
export function counterpartyNotice(h: Helix | null | undefined, name: string): string {
  if (!h) return "";
  if (h.degraded?.ibc) return h.degraded.ibc;
  if (!h.errors?.ibc) return "";
  if (/403|forbidden/i.test(h.errors.ibc)) {
    return `${name}'s public RPC refused this explorer (HTTP 403). Providers often block datacenter addresses, so this is a missing view of that chain, not a halt. Aether's channel and packets are still shown.`;
  }
  return `${name}'s RPC didn't answer. Aether's channel and packets are still shown.`;
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
