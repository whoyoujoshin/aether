import type { ReactNode } from "react";
import { splitTypeUrl, typeBg, typeColor, typeAbbr } from "../format";

/** A message type, colored by kind: MsgSend, MsgExec, MsgSubmitPoW… */
export function MsgTag({ type }: { type: string }) {
  if (!type) return <span className="faint">—</span>;
  return (
    <span className="tag" style={{ color: typeColor(type), background: typeBg(type) }}>
      {type}
    </span>
  );
}

/** The square tile with a message type's initials. */
export function TypeTile({ type }: { type: string }) {
  return (
    <div className="type-tile" style={{ color: typeColor(type), background: typeBg(type) }}>
      {typeAbbr(type)}
    </div>
  );
}

/** A tag plus the proto package, from a type URL. */
export function TypeUrl({ url }: { url: string }) {
  const [name, pkg] = splitTypeUrl(url);
  return (
    <>
      <MsgTag type={name} />
      {pkg && <span className="muted" style={{ fontSize: 13 }}>{pkg}</span>}
    </>
  );
}

export function Tabs<T extends string>({ tabs, value, onChange }: { tabs: readonly T[]; value: T; onChange: (t: T) => void }) {
  return (
    <div className="tabs" role="tablist">
      {tabs.map((t) => (
        <button key={t} role="tab" aria-selected={t === value} className={`tab${t === value ? " on" : ""}`} onClick={() => onChange(t)}>
          {t}
        </button>
      ))}
    </div>
  );
}

/** One label / value row of a detail card. */
export function KV({ label, children }: { label: ReactNode; children: ReactNode }) {
  return (
    <div className="kv">
      <span>{label}</span>
      <div className="kv-inline">{children}</div>
    </div>
  );
}

/** A thin progress bar, 0–100. */
export function MiniBar({ pct, bone = false }: { pct: number; bone?: boolean }) {
  return (
    <span className={`mini-bar${bone ? " bone" : ""}`}>
      <span style={{ width: `${Math.max(0, Math.min(100, pct))}%` }} />
    </span>
  );
}

export interface Segment {
  label: string;
  value: number;
  color: string;
}

/** Proportional segments with a legend underneath (activity mix, tallies, miner share). */
export function StackBar({ segments, height = 10, glow = false }: { segments: Segment[]; height?: number; glow?: boolean }) {
  const total = segments.reduce((a, s) => a + s.value, 0);
  if (total <= 0) return <div className="stack-bar" style={{ height, background: "var(--cell)" }} />;
  return (
    <div className="stack-bar" style={{ height, borderRadius: height / 2 }}>
      {segments
        .filter((s) => s.value > 0)
        .map((s) => (
          <div key={s.label} style={{ width: `${(s.value / total) * 100}%`, background: s.color, boxShadow: glow ? `0 0 10px ${s.color}` : undefined }} />
        ))}
    </div>
  );
}

export function pct(value: number, total: number, digits = 1): string {
  if (total <= 0) return "0%";
  return `${((value / total) * 100).toFixed(digits)}%`;
}

export function Legend({ segments }: { segments: Segment[] }) {
  const total = segments.reduce((a, s) => a + s.value, 0);
  return (
    <>
      {segments.map((s) => (
        <div className="legend-row" key={s.label}>
          <span className="swatch" style={{ background: s.color }} />
          <span className="mono ellipsis">{s.label}</span>
          <span className="mono" style={{ marginLeft: "auto", color: "var(--muted)" }}>
            {pct(s.value, total)}
          </span>
        </div>
      ))}
    </>
  );
}

export function ErrorBanner({ error, prefix }: { error: string | null; prefix?: string }) {
  if (!error) return null;
  return (
    <div className="error-banner">
      {prefix ? `${prefix}: ` : ""}
      {error}
    </div>
  );
}

/** A stat in a page header: small label over a mono value. */
export function HeadStat({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div>
      <div className="head-stat-label">{label}</div>
      <div className="head-stat-value">{children}</div>
    </div>
  );
}
