import { api, IBCChannel } from "../api";
import { useApi } from "../hooks";
import { AddressLink } from "../components/Hash";
import { ErrorBanner, HeadStat } from "../components/ui";
import { coins, duration, enumShort } from "../format";

const stateColor: Record<string, [string, string]> = {
  OPEN: ["var(--green)", "rgba(22,199,132,.12)"],
  TRYOPEN: ["oklch(0.8 0.14 80)", "oklch(0.8 0.14 80 / 14%)"],
  INIT: ["var(--muted)", "var(--cell)"],
  CLOSED: ["var(--red)", "var(--red-14)"],
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

function footerNote(state: string): string {
  const short = enumShort(state, "STATE_");
  if (short === "OPEN") return "This explorer only reads Aether's own chain state: it can't confirm the counterparty chain or its relayer are still running.";
  if (short === "TRYOPEN" || short === "INIT") return "Handshake in progress — waiting for the next relayer step.";
  if (short === "CLOSED") return "Channel closed. No further packets can be sent on this path.";
  return "";
}

function ChannelCard({ c }: { c: IBCChannel }) {
  return (
    <div className="svc-card">
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
                <span key={b.denom} className="mono">{coins(`${b.amount}${b.denom}`)}</span>
              ))}
            </div>
          ) : (
            <span className="mono muted">0 uaeth</span>
          )}
          <div style={{ display: "flex", alignItems: "center", gap: 6, marginTop: 6 }}>
            <AddressLink address={c.escrowAddress} plain />
          </div>
        </div>
      )}

      <div className="muted" style={{ fontSize: 12, fontStyle: "italic", lineHeight: 1.5, borderTop: "1px solid var(--rule)", paddingTop: 10 }}>
        {footerNote(c.state)}
      </div>
    </div>
  );
}

export default function IBC() {
  const ibc = useApi(api.ibc, [], 15000);
  const channels = ibc.data?.channels ?? [];
  const totalEscrowUaeth = channels.reduce((sum, c) => sum + (c.escrowBalances ?? []).filter((b) => b.denom === "uaeth").reduce((s, b) => s + BigInt(b.amount || "0"), 0n), 0n);

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1 className="page-title">IBC</h1>
          <div className="page-sub">Clients, connections and channels this chain currently holds, read live on every request.</div>
        </div>
        <div className="head-stats">
          <HeadStat label="Clients">{ibc.data ? ibc.data.clients : "—"}</HeadStat>
          <HeadStat label="Connections">{ibc.data ? ibc.data.connections : "—"}</HeadStat>
          <HeadStat label="Channels">{ibc.data ? channels.length : "—"}</HeadStat>
          <HeadStat label="Escrowed">{coins(`${totalEscrowUaeth}uaeth`)}</HeadStat>
        </div>
      </div>

      <ErrorBanner error={ibc.error} />

      {ibc.loading && !ibc.data ? (
        <div className="loading">Loading…</div>
      ) : channels.length > 0 ? (
        <div className="svc-grid">
          {channels.map((c) => (
            <ChannelCard key={`${c.portId}/${c.channelId}`} c={c} />
          ))}
        </div>
      ) : (
        <div className="card gap-top-lg">
          <div className="empty">No IBC channels open yet. See docs/IBC.md and cmd/relayer.</div>
        </div>
      )}

      <div className="muted" style={{ padding: "12px 4px", fontSize: 12 }}>
        A channel's state reflects Aether's own chain, not a live health check of its counterparty. Packets sent counts this side's send-packet sequence; it doesn't confirm the counterparty received or acknowledged them.
      </div>
    </div>
  );
}
