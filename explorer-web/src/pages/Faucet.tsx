import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { api, Drip, FaucetStats } from "../api";
import { useApi } from "../hooks";
import { AddressLink, TxHash } from "../components/Hash";
import { StackBar } from "../components/ui";
import { aeth, compact, duration, int, timeAgo } from "../format";
import { isAetherAddress, randomAetherAddress, solveChallenge } from "../faucetwork";

// Who asked: agents (signed agent key) red, people (browser proof of work)
// bone, unverified API calls dim.
const SOURCES = [
  { key: "agent", label: "Agents", how: "signed agent key", color: "var(--red)" },
  { key: "web", label: "People", how: "browser, proof-of-work", color: "#e4d9c6" },
  { key: "api", label: "Unverified API", how: "no key, tighter limits", color: "#4a443d" },
] as const;

/** 86400 as "24 hours", the way the faucet's rule reads. */
function cooldownText(secs: number): string {
  if (secs > 0 && secs % 3600 === 0 && secs <= 72 * 3600) return `${secs / 3600} hour${secs === 3600 ? "" : "s"}`;
  return duration(secs);
}

type Phase =
  | { kind: "idle" }
  | { kind: "pow"; hashes: number }
  | { kind: "bc" }
  | { kind: "done"; tx: string; newWallet: boolean }
  | { kind: "error"; message: string };

function RequestCard({ stats, onSent }: { stats: FaucetStats | null; onSent: () => void }) {
  const [address, setAddress] = useState("");
  const [phase, setPhase] = useState<Phase>({ kind: "idle" });
  const abort = useRef<AbortController | null>(null);
  useEffect(() => () => abort.current?.abort(), []);

  const amount = stats ? aeth(String(stats.amount_uaeth)) : "AETH";
  const trimmed = address.trim();
  const valid = isAetherAddress(trimmed);
  const busy = phase.kind === "pow" || phase.kind === "bc";
  const sent = phase.kind === "done";
  const cooldown = stats ? cooldownText(stats.address_cooldown_secs) : "";

  async function request() {
    if (!valid || busy || sent) return;
    abort.current?.abort();
    const ctl = new AbortController();
    abort.current = ctl;
    try {
      setPhase({ kind: "pow", hashes: 0 });
      const ch = await api.faucetChallenge(trimmed);
      const nonce = await solveChallenge(ch.challenge, ch.bits, (h) => setPhase({ kind: "pow", hashes: h }), ctl.signal);
      setPhase({ kind: "bc" });
      const reply = await api.faucetRequest({ address: trimmed, strand: "aether", pow: { challenge: ch.challenge, nonce } });
      if (reply.success && reply.tx_hash) {
        setPhase({ kind: "done", tx: reply.tx_hash, newWallet: !!reply.new_wallet });
        onSent();
      } else if (reply.code === "pending" && reply.tx_hash) {
        setPhase({ kind: "done", tx: reply.tx_hash, newWallet: false });
        onSent();
      } else {
        const wait = reply.retry_after_seconds ? ` Try again in ${duration(reply.retry_after_seconds)}.` : "";
        setPhase({ kind: "error", message: `${reply.message}.${wait}` });
      }
    } catch (err) {
      if (!ctl.signal.aborted) setPhase({ kind: "error", message: err instanceof Error ? err.message : String(err) });
    }
  }

  const expected = stats ? 2 ** stats.pow_bits : 1;
  let button: React.ReactNode;
  switch (phase.kind) {
    case "pow":
      button = (
        <>
          <span>Proving you're a browser…</span>
          <span className="mono faint" style={{ fontSize: 12 }}>
            {int(phase.hashes)} hashes
          </span>
        </>
      );
      break;
    case "bc":
      button = "Sending…";
      break;
    case "done":
      button = `Sent. Request again in ${cooldown || "a while"}`;
      break;
    default:
      button = !trimmed ? "Enter an address to continue" : valid ? `Request ${amount}` : "Enter a valid aether1 address";
  }

  return (
    <div className="card-glow faucet-request">
      <div className="eyebrow red">Faucet</div>
      <h1 className="faucet-title">Get {amount}</h1>
      <div className="muted">
        Once per address every {cooldown || "cooldown"}. Free, no sign-up.
      </div>

      <label className="faucet-label" htmlFor="faucet-address">
        Your address
      </label>
      <div className={`faucet-input${!trimmed ? "" : valid ? " ok" : " bad"}`}>
        <input
          id="faucet-address"
          className="mono"
          placeholder="aether1…"
          value={address}
          spellCheck={false}
          autoComplete="off"
          disabled={busy}
          onChange={(e) => {
            setAddress(e.target.value);
            if (phase.kind !== "pow" && phase.kind !== "bc") setPhase({ kind: "idle" });
          }}
        />
        {trimmed && <span className={`faucet-check ${valid ? "green" : "danger"}`}>{valid ? "VALID" : "NOT AN AETHER ADDRESS"}</span>}
      </div>
      <div className="faucet-hint">
        <button
          type="button"
          className="linkish"
          disabled={busy}
          onClick={() => {
            setAddress(randomAetherAddress());
            setPhase({ kind: "idle" });
          }}
        >
          Use a demo address
        </button>
        <span className="faint">·</span>
        <span className="muted">No wallet yet? Any valid aether1 address works, even a brand-new one.</span>
      </div>

      <div className="faucet-label">Receive on</div>
      <div className="faucet-strands">
        <button type="button" className="on">
          <span className="dot" style={{ color: "var(--red)" }} /> Aether
        </button>
        <button
          type="button"
          disabled={!stats?.ibc_enabled}
          title={stats?.ibc_enabled ? undefined : "Opens with the Osmosis channel"}
        >
          <span className="dot" style={{ color: "#e4d9c6" }} /> IBC
        </button>
      </div>

      <button type="button" className={`faucet-go${valid && !busy && !sent ? " ready" : ""}`} disabled={!valid || busy || sent} onClick={request}>
        {button}
      </button>
      {phase.kind === "pow" && (
        <div className="faucet-progress">
          <span style={{ width: `${Math.min(100, (phase.hashes / expected) * 100)}%` }} />
        </div>
      )}
      {phase.kind === "done" && (
        <div className="faucet-done">
          <span className="green" style={{ fontWeight: 600 }}>
            {amount} sent
          </span>
          <TxHash hash={phase.tx} />
          {phase.newWallet && <span className="tag new-wallet">NEW WALLET</span>}
        </div>
      )}
      {phase.kind === "error" && <div className="error-banner" style={{ marginTop: 12 }}>{phase.message}</div>}

      <div className="faucet-foot">
        <span>
          Faucet balance{" "}
          <b className="mono">{stats?.balance_uaeth !== undefined ? `${compact(stats.balance_uaeth / 1e6)} AETH` : "—"}</b>
        </span>
        {stats?.balance_uaeth !== undefined && stats.amount_uaeth > 0 && (
          <span>~{compact(Math.floor(stats.balance_uaeth / stats.amount_uaeth))} drips left</span>
        )}
        <span>
          Building a bot? <Link to="/agents">Use an agent key →</Link>
        </span>
      </div>
    </div>
  );
}

function Kpi({ label, value, sub, tone }: { label: string; value: string; sub: string; tone?: "green" | "red" }) {
  return (
    <div className={`card kpi${tone === "red" ? " kpi-red" : ""}`}>
      <div className="kpi-label">{label}</div>
      <div className={`kpi-value mono${tone ? " " + tone : ""}`}>{value}</div>
      <div className="kpi-sub">{sub}</div>
    </div>
  );
}

function WhoRequests({ stats }: { stats: FaucetStats | null }) {
  const c = stats?.sources_30d ?? { agent: 0, web: 0, api: 0 };
  const total = c.agent + c.web + c.api;
  return (
    <div className="card card-pad">
      <div style={{ display: "flex", justifyContent: "space-between", marginBottom: 14 }}>
        <span className="eyebrow">Who requests · 30d</span>
        <span className="mono faint" style={{ fontSize: 11 }}>
          by drips
        </span>
      </div>
      <StackBar segments={SOURCES.map((s) => ({ label: s.label, value: c[s.key], color: s.color }))} height={10} />
      <div style={{ marginTop: 14, display: "grid", gap: 10 }}>
        {SOURCES.map((s) => (
          <div key={s.key} className="who-row">
            <span className="swatch" style={{ background: s.color }} />
            <span style={{ fontWeight: 500 }}>{s.label}</span>
            <span className="muted">{s.how}</span>
            <span className="mono" style={{ marginLeft: "auto" }}>
              {total ? `${Math.round((c[s.key] / total) * 100)}%` : "—"}
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}

function NewWalletsChart({ stats }: { stats: FaucetStats | null }) {
  const days = stats?.new_wallets_daily ?? [];
  const max = Math.max(1, ...days.map((d) => d.agent + d.web + d.api));
  const label = (date: string) => new Date(date + "T00:00:00Z").toLocaleDateString("en-US", { month: "short", day: "numeric", timeZone: "UTC" });
  return (
    <div className="card card-pad">
      <div style={{ display: "flex", alignItems: "center", gap: 14, flexWrap: "wrap", marginBottom: 18 }}>
        <span className="card-title">New wallets created via the faucet</span>
        <span style={{ marginLeft: "auto", display: "flex", gap: 14 }} className="muted">
          {SOURCES.map((s) => (
            <span key={s.key} style={{ display: "inline-flex", alignItems: "center", gap: 6, fontSize: 12 }}>
              <span className="swatch" style={{ background: s.color }} />
              {s.key === "web" ? "People" : s.key === "api" ? "Unverified" : "Agents"}
            </span>
          ))}
        </span>
      </div>
      <div className="wallet-bars">
        {days.map((d, i) => {
          const total = d.agent + d.web + d.api;
          return (
            <div key={d.date} className="wallet-bar" title={`${d.date}: ${total} new (${d.agent} agents, ${d.web} people, ${d.api} unverified)`}>
              <div className="wallet-stack" style={{ height: `${(total / max) * 100}%`, boxShadow: i === days.length - 1 && total ? "0 0 18px rgba(192,80,58,.35)" : undefined }}>
                <span style={{ flex: d.api, background: "#4a443d" }} />
                <span style={{ flex: d.web, background: "#e4d9c6" }} />
                <span style={{ flex: d.agent, background: "var(--red)" }} />
              </div>
            </div>
          );
        })}
      </div>
      {days.length > 0 && (
        <div className="wallet-axis mono faint">
          <span>{label(days[0].date)}</span>
          <span>{label(days[Math.floor(days.length / 2)].date)}</span>
          <span>today</span>
        </div>
      )}
    </div>
  );
}

function TopAgents({ stats }: { stats: FaucetStats | null }) {
  const list = stats?.top_agents_30d ?? [];
  const max = Math.max(1, ...list.map((a) => a.new_wallets));
  return (
    <div className="card">
      <div className="card-head">
        <span className="card-title">Top agents · 30d</span>
        <span className="card-meta">NEW WALLETS</span>
      </div>
      {list.length === 0 ? (
        <div className="empty">No agent-key requests in the last 30 days.</div>
      ) : (
        <div className="rows">
          {list.map((a) => (
            <div key={a.agent_id} className="row agent-row">
              <div className="initials red-tile">{a.name.slice(0, 2).toUpperCase()}</div>
              <div style={{ minWidth: 0, flex: 1 }}>
                <div style={{ fontWeight: 500 }}>{a.name}</div>
                <div className="agent-bar-line">
                  <div className="power-bar" style={{ flex: 1 }}>
                    <div style={{ width: `${(a.new_wallets / max) * 100}%` }} />
                  </div>
                  <span className="mono faint" style={{ fontSize: 11 }}>
                    {a.agent_id.slice(0, 7)}…
                  </span>
                </div>
              </div>
              <span className="mono" style={{ fontWeight: 600 }}>
                {int(a.new_wallets)}
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function SourcePill({ d }: { d: Drip }) {
  if (d.source === "agent") return <span className="tag src-agent">AGENT · {d.agent_name || d.agent_id}</span>;
  if (d.source === "web") return <span className="tag src-web">WEB</span>;
  return <span className="tag src-api">API</span>;
}

const DRIP_COLS = "minmax(0,1.6fr) 110px minmax(0,1.3fr) minmax(0,1fr) 110px 80px";

function RecentDrips({ drips, error }: { drips: Drip[]; error: string | null }) {
  return (
    <div className="card">
      <div className="card-head">
        <span className="card-title">Recent drips</span>
        <span className="card-meta">live</span>
      </div>
      {error && <div className="error-banner">{error}</div>}
      {drips.length === 0 ? (
        <div className="empty">No drips yet.</div>
      ) : (
        <div className="scroll-x">
          <div className="rows">
            <div className="row th" style={{ gridTemplateColumns: DRIP_COLS }}>
              <span>Recipient</span>
              <span>Strand</span>
              <span>Source</span>
              <span>Tx</span>
              <span className="right">Amount</span>
              <span className="right">Age</span>
            </div>
            {drips.map((d) => (
              <div key={d.tx_hash + d.address} className="row hover" style={{ gridTemplateColumns: DRIP_COLS }}>
                <span style={{ display: "flex", gap: 10, alignItems: "center", minWidth: 0 }}>
                  <AddressLink address={d.address} copy={false} />
                  {d.new_wallet && <span className="tag new-wallet">NEW WALLET</span>}
                </span>
                <span className="muted" style={{ display: "inline-flex", alignItems: "center", gap: 7 }}>
                  <span className="dot" style={{ color: d.strand === "ibc" ? "#e4d9c6" : "var(--red)" }} />
                  {d.strand === "ibc" ? "IBC" : "Aether"}
                </span>
                <span>
                  <SourcePill d={d} />
                </span>
                <span>
                  <TxHash hash={d.tx_hash} copy={false} />
                </span>
                <span className="right mono">{aeth(String(d.amount_uaeth))}</span>
                <span className="right muted">{timeAgo(d.time)}</span>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

export default function Faucet() {
  const [tick, setTick] = useState(0);
  const stats = useApi(api.faucetStats, [tick], 15000);
  const drips = useApi(() => api.faucetDrips(20), [tick], 10000);
  const s = stats.data;

  if (stats.error && !s) {
    return (
      <div className="page">
        <div className="card card-pad">
          <div className="eyebrow red">Faucet</div>
          <h1 className="faucet-title">The faucet isn't reachable right now</h1>
          <div className="muted">{stats.error}</div>
        </div>
      </div>
    );
  }

  const agentShare = s && s.new_wallets ? Math.round((s.new_wallets_by_agents / s.new_wallets) * 100) : 0;
  const since = s?.since ? new Date(s.since).toLocaleDateString("en-US", { month: "short", day: "numeric", year: "numeric" }) : "";

  return (
    <div className="page">
      <div className="faucet-top">
        <RequestCard stats={s} onSent={() => setTick((t) => t + 1)} />
        <div className="faucet-side">
          <div className="kpi-grid">
            <Kpi label="Drips · all time" value={s ? int(s.drips) : "—"} sub={s ? `${compact(s.sent_uaeth / 1e6)} AETH sent` : ""} />
            <Kpi label="Unique wallets funded" value={s ? int(s.unique_wallets) : "—"} sub={since ? `since ${since}` : "since the drip log began"} />
            <Kpi label="New wallets created" value={s ? int(s.new_wallets) : "—"} sub="first tx was a faucet drip" tone="green" />
            <Kpi label="Created by agents" value={s ? `${agentShare}%` : "—"} sub={s ? `${int(s.new_wallets_by_agents)} wallets` : ""} tone="red" />
          </div>
          <WhoRequests stats={s} />
        </div>
      </div>
      <div className="faucet-mid">
        <NewWalletsChart stats={s} />
        <TopAgents stats={s} />
      </div>
      <RecentDrips drips={drips.data?.drips ?? []} error={drips.error} />
    </div>
  );
}
