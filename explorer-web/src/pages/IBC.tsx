import type { ReactNode } from "react";
import { api, IBCChannel, IBCClient, IBCDenom, IBCTransfer } from "../api";
import { useApi } from "../hooks";
import { AddressLink, TxHash } from "../components/Hash";
import { ErrorBanner, HeadStat, NoticeBanner } from "../components/ui";
import { amountIn, duration, enumShort, timeAgo, utc } from "../format";

const stateColor: Record<string, [string, string]> = {
  OPEN: ["var(--green)", "rgba(22,199,132,.12)"],
  TRYOPEN: ["oklch(0.8 0.14 80)", "oklch(0.8 0.14 80 / 14%)"],
  INIT: ["var(--muted)", "var(--cell)"],
  CLOSED: ["var(--red)", "var(--red-14)"],
};

const attentionStyle: Record<string, [string, string]> = {
  active: ["var(--green)", "var(--green-10)"],
  expiring: ["var(--warn)", "oklch(0.8 0.14 80 / 16%)"],
  expired: ["var(--danger)", "var(--danger-12)"],
  frozen: ["var(--danger)", "var(--danger-12)"],
  unknown: ["var(--muted)", "var(--cell)"],
};

function ChannelStateBadge({ state }: { state: string }) {
  const short = enumShort(state, "STATE_");
  const [color, bg] = stateColor[short] ?? ["var(--muted)", "var(--cell)"];
  return (
    <span className="tag" style={{ color, background: bg }}>
      {short}
    </span>
  );
}

function clientKind(c: IBCClient): string {
  if (c.attention === "frozen" || c.status === "Frozen") return "frozen";
  if (c.attention === "expired" || c.status === "Expired") return "expired";
  if (c.attention === "expiring") return "expiring";
  if (c.status === "Active") return "active";
  return "unknown";
}

function ClientBadge({ c }: { c: IBCClient }) {
  const kind = clientKind(c);
  const [color, bg] = attentionStyle[kind];
  const label = kind === "unknown" ? (c.status || "UNKNOWN").toUpperCase() : kind.toUpperCase();
  return (
    <span className="tag" style={{ color, background: bg }}>
      {label}
    </span>
  );
}

function remaining(c: IBCClient): string {
  if (!c.expiresAt) return "";
  const secs = Math.round((new Date(c.expiresAt).getTime() - Date.now()) / 1000);
  if (Number.isNaN(secs)) return "";
  if (secs >= 0) return `${duration(secs)} left`;
  return `ended ${duration(-secs)} ago`;
}

function footerNote(state: string): string {
  const short = enumShort(state, "STATE_");
  if (short === "OPEN") return "Channel state is Aether's own. A counterparty that has stopped signing still looks open here until a packet times out.";
  if (short === "TRYOPEN" || short === "INIT") return "Handshake in progress — waiting for the next relayer step.";
  if (short === "CLOSED") return "Channel closed. No further packets can be sent on this path.";
  return "";
}

function delayLabel(ns: number): string {
  if (!ns) return "none";
  const secs = ns / 1e9;
  return secs >= 1 ? duration(secs) : `${ns} ns`;
}

function denomAmount(d: IBCDenom): string {
  return amountIn(d.amount, d.denom);
}

function Section({ title, meta, children }: { title: string; meta?: string; children: ReactNode }) {
  return (
    <div className="card gap-top-lg">
      <div className="card-head">
        <span className="card-title">{title}</span>
        {meta && <span className="card-meta">{meta}</span>}
      </div>
      {children}
    </div>
  );
}

function ChannelCard({ c }: { c: IBCChannel }) {
  const attention = c.clientAttention || (c.clientStatus === "Expired" ? "expired" : c.clientStatus === "Frozen" ? "frozen" : "");
  return (
    <div className={`svc-card${attention ? ` attention-${attention}` : ""}`}>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", gap: 12 }}>
        <span className="mono" style={{ fontSize: 15, fontWeight: 600 }}>
          {c.portId}/{c.channelId}
        </span>
        <ChannelStateBadge state={c.state} />
      </div>

      <div className="mono muted" style={{ fontSize: 12 }}>
        <span style={{ color: "var(--text-2)" }}>{c.connectionId || "—"}</span>
        {" · "}
        <span style={{ color: "var(--text-2)" }}>{c.clientId || "—"}</span>
        {" → "}
        <span style={{ color: "var(--text-2)" }}>{c.counterpartyChainId || "unknown counterparty"}</span>
        {c.counterpartyChannelId ? ` · ${c.counterpartyPortId}/${c.counterpartyChannelId}` : ""}
      </div>

      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "10px 20px", fontSize: 13 }}>
        <div>
          <div className="muted" style={{ fontSize: 10, letterSpacing: "0.06em" }}>ORDERING</div>
          <div className="mono" style={{ fontWeight: 600, marginTop: 2 }}>{enumShort(c.ordering, "ORDER_")}</div>
        </div>
        <div>
          <div className="muted" style={{ fontSize: 10, letterSpacing: "0.06em" }}>VERSION</div>
          <div className="mono" style={{ fontWeight: 600, marginTop: 2 }}>{c.version || "—"}</div>
        </div>
        <div>
          <div className="muted" style={{ fontSize: 10, letterSpacing: "0.06em" }}>PACKETS SENT</div>
          <div className="mono" style={{ fontWeight: 600, marginTop: 2 }}>{c.packetsSent}{c.pendingPackets > 0 ? ` (${c.pendingPackets} pending)` : ""}</div>
        </div>
        <div>
          <div className="muted" style={{ fontSize: 10, letterSpacing: "0.06em" }}>UNBONDING PERIOD</div>
          <div className="mono" style={{ fontWeight: 600, marginTop: 2 }}>{c.unbondingPeriodSecs ? duration(c.unbondingPeriodSecs) : "—"}</div>
        </div>
      </div>

      {c.portId === "transfer" && (
        <div style={{ borderTop: "1px solid var(--rule)", paddingTop: 10, fontSize: 13 }}>
          <div className="muted" style={{ fontSize: 10, letterSpacing: "0.06em", marginBottom: 4 }}>ESCROW</div>
          {(c.escrowBalances ?? []).length > 0 ? (
            <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
              {(c.escrowBalances ?? []).map((b) => (
                <span key={b.denom} className="mono">{amountIn(b.amount, b.denom)}</span>
              ))}
            </div>
          ) : (
            <span className="mono muted">0</span>
          )}
          {c.escrowAddress && (
            <div style={{ display: "flex", alignItems: "center", gap: 6, marginTop: 6 }}>
              <AddressLink address={c.escrowAddress} plain />
            </div>
          )}
        </div>
      )}

      {attention && (
        <div className="muted" style={{ fontSize: 12, borderTop: "1px solid var(--rule)", paddingTop: 10 }}>
          Client {c.clientId} is {attention}
          {c.clientExpiresAt ? ` · trusting period ${c.clientAttention === "expired" || c.clientStatus === "Expired" ? "ended" : "ends"} ${utc(c.clientExpiresAt)}` : ""}.
        </div>
      )}

      <div className="muted" style={{ fontSize: 12, fontStyle: "italic", lineHeight: 1.5, borderTop: "1px solid var(--rule)", paddingTop: 10 }}>
        {footerNote(c.state)}
      </div>
    </div>
  );
}

function transferParty(t: IBCTransfer): string {
  if (t.direction === "out") return `Aether → ${t.channel}`;
  return `${t.channel} → Aether`;
}

export default function IBC() {
  const ibc = useApi(api.ibc, [], 15000);
  const data = ibc.data;
  const channels = data?.channels ?? [];
  const clients = data?.clientStates ?? [];
  const connections = data?.connectionStates ?? [];
  const relayers = data?.relayers ?? [];
  const transfers = data?.transfers ?? [];
  const denoms = data?.denoms ?? [];
  const alerts = clients.filter((c) => clientKind(c) === "expiring" || clientKind(c) === "expired" || clientKind(c) === "frozen");
  const anyHard = alerts.some((c) => clientKind(c) !== "expiring");

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1 className="page-title">IBC</h1>
          <div className="page-sub">Clients, connections and channels on Aether, with trusting-period expiry, relayers, recent transfers and the IBC tokens held here.</div>
        </div>
        <div className="head-stats">
          <HeadStat label="Clients">{data ? data.clients : "—"}</HeadStat>
          <HeadStat label="Connections">{data ? data.connections : "—"}</HeadStat>
          <HeadStat label="Channels">{data ? channels.length : "—"}</HeadStat>
          <HeadStat label="IBC tokens">{data ? denoms.length : "—"}</HeadStat>
        </div>
      </div>

      <ErrorBanner error={ibc.error} />
      {data?.activityNote && <NoticeBanner>{data.activityNote}</NoticeBanner>}

      {alerts.length > 0 && (
        <div className={anyHard ? "error-banner" : "warning-banner"} role="status">
          {alerts.map((c) => (
            <div key={c.clientId}>
              <b className="mono">{c.clientId}</b>
              {c.chainId ? ` (${c.chainId})` : ""} is {clientKind(c)}.
              {c.expiresAt ? ` Trusting period ${remaining(c)} (${utc(c.expiresAt)}).` : " The chain reported this status."} Packets that depend on it will fail while it stays that way.
            </div>
          ))}
        </div>
      )}

      {ibc.loading && !data ? (
        <div className="loading">Loading…</div>
      ) : (
        <>
          <Section title="Clients" meta="trusting period">
            {clients.length === 0 ? (
              <div className="empty">No IBC clients yet.</div>
            ) : (
              <div className="svc-grid" style={{ marginTop: 0, padding: 18 }}>
                {clients.map((c) => (
                  <div key={c.clientId} className={`svc-card attention-${clientKind(c)}`} style={{ margin: 0 }}>
                    <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", gap: 12 }}>
                      <span className="mono" style={{ fontSize: 15, fontWeight: 600 }}>{c.clientId}</span>
                      <ClientBadge c={c} />
                    </div>
                    <div className="mono" style={{ fontSize: 13 }}>{c.chainId || "chain id unknown"}</div>
                    <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "10px 20px", fontSize: 13 }}>
                      <div>
                        <div className="muted" style={{ fontSize: 10, letterSpacing: "0.06em" }}>LATEST HEIGHT</div>
                        <div className="mono" style={{ fontWeight: 600, marginTop: 2 }}>{c.latestHeight ? `${c.latestRevision}-${c.latestHeight}` : "—"}</div>
                      </div>
                      <div>
                        <div className="muted" style={{ fontSize: 10, letterSpacing: "0.06em" }}>TRUSTING PERIOD</div>
                        <div className="mono" style={{ fontWeight: 600, marginTop: 2 }}>{c.trustingPeriodSecs ? duration(c.trustingPeriodSecs) : "—"}</div>
                      </div>
                      <div>
                        <div className="muted" style={{ fontSize: 10, letterSpacing: "0.06em" }}>CONSENSUS TIME</div>
                        <div className="mono" style={{ fontWeight: 600, marginTop: 2 }}>{c.consensusTime ? utc(c.consensusTime) : "—"}</div>
                      </div>
                      <div>
                        <div className="muted" style={{ fontSize: 10, letterSpacing: "0.06em" }}>EXPIRES</div>
                        <div className="mono" style={{ fontWeight: 600, marginTop: 2 }}>{c.expiresAt ? remaining(c) : "—"}</div>
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </Section>

          <Section title="Connections" meta={connections.length ? String(connections.length) : undefined}>
            {connections.length === 0 ? (
              <div className="empty">No IBC connections yet.</div>
            ) : (
              connections.map((c) => (
                <div key={c.id} className="row" style={{ gridTemplateColumns: "140px 110px minmax(0,1fr) 120px" }}>
                  <span className="mono">{c.id}</span>
                  <ChannelStateBadge state={c.state} />
                  <span className="mono muted ellipsis">
                    {c.clientId || "—"} → {c.counterpartyClientId || "—"}
                    {c.counterpartyConnectionId ? ` · ${c.counterpartyConnectionId}` : ""}
                  </span>
                  <span className="mono muted right">delay {delayLabel(c.delayPeriodNs)}</span>
                </div>
              ))
            )}
          </Section>

          <div className="gap-top-lg">
            <div className="card-title" style={{ marginBottom: 4 }}>Channels</div>
            {channels.length > 0 ? (
              <div className="svc-grid">
                {channels.map((c) => (
                  <ChannelCard key={`${c.portId}/${c.channelId}`} c={c} />
                ))}
              </div>
            ) : (
              <div className="card">
                <div className="empty">No IBC channels open yet. See docs/IBC.md and cmd/relayer.</div>
              </div>
            )}
          </div>

          <Section title="Relayers" meta="newest indexed packet relays">
            {relayers.length === 0 ? (
              <div className="empty">No recv, ack or timeout transactions in the indexed window.</div>
            ) : (
              relayers.map((r) => (
                <div key={r.address} className="row" style={{ gridTemplateColumns: "minmax(0,1.4fr) 90px minmax(0,1fr) 140px" }}>
                  <AddressLink address={r.address} />
                  <span className="mono">{r.packets} pkts</span>
                  <span className="mono muted ellipsis">{r.channels.join(", ") || "—"} · {r.lastAction.replace(/_/g, " ")}</span>
                  <span className="muted right">{r.lastTime ? timeAgo(r.lastTime) : `block ${r.lastHeight}`}</span>
                </div>
              ))
            )}
          </Section>

          <Section title="Recent transfers" meta="ICS-20">
            {transfers.length === 0 ? (
              <div className="empty">No recent MsgTransfer sends or transfer receives.</div>
            ) : (
              transfers.map((t) => (
                <div key={`${t.hash}-${t.sequence}-${t.direction}`} className="row" style={{ gridTemplateColumns: "minmax(0,1.1fr) 150px minmax(0,1fr) 120px" }}>
                  <span style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0 }}>
                    <TxHash hash={t.hash} />
                    {t.status === "failed" && <span className="tag" style={{ color: "var(--danger)", background: "var(--danger-12)" }}>FAILED</span>}
                  </span>
                  <span className="mono ellipsis">{amountIn(t.amount, t.denom)}</span>
                  <span className="mono muted ellipsis">{transferParty(t)} · {t.direction === "out" ? "out" : "in"}</span>
                  <span className="muted right">{t.time ? timeAgo(t.time) : `block ${t.height}`}</span>
                </div>
              ))
            )}
          </Section>

          <Section title="IBC tokens on Aether" meta="denom traces with a balance">
            {denoms.length === 0 ? (
              <div className="empty">No IBC denom with a non-zero supply.</div>
            ) : (
              denoms.map((d) => (
                <div key={d.denom} className="row" style={{ gridTemplateColumns: "minmax(0,1.2fr) 160px minmax(0,1fr)" }}>
                  <div style={{ minWidth: 0 }}>
                    <div className="mono ellipsis" title={d.denom}>{d.symbol ? `${d.symbol}` : d.denom}</div>
                    {d.symbol && <div className="mono muted ellipsis" style={{ fontSize: 12 }} title={d.denom}>{d.denom}</div>}
                  </div>
                  <span className="mono">{denomAmount(d)}</span>
                  <span className="mono muted ellipsis">{d.path ? `${d.path}/${d.baseDenom}` : d.baseDenom || "trace unknown"}</span>
                </div>
              ))
            )}
          </Section>
        </>
      )}

      <div className="muted" style={{ padding: "12px 4px", fontSize: 12 }}>
        Client status and expiry come from Aether's light clients. A client is marked expiring in the last 24 hours of its trusting period. Relayers are the signers of recv, acknowledgement and timeout messages.
      </div>
    </div>
  );
}
