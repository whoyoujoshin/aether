import { Link, useParams } from "react-router-dom";
import { api, AddressPage, Transaction } from "../api";
import { useApi } from "../hooks";
import { CopyButton, TxHash } from "../components/Hash";
import { GrantsPanel } from "../components/Grants";
import { ErrorBanner } from "../components/ui";
import { aeth, aethNumber, amountIn, assetLabel, assetOf, int, shortDenom, timeAgo, uaethOf, utc } from "../format";

function kindOf(tx: Transaction): { icon: string; label: string; k: "in" | "out" } {
  return tx.direction === "received" ? { icon: "↓", label: "Received", k: "in" } : { icon: "↑", label: "Sent", k: "out" };
}

/** The listed history as CSV, built in the browser from what's on screen. */
function downloadCSV(page: AddressPage) {
  const rows = [["hash", "height", "timestamp", "direction", "amount_uaeth", "code"]];
  for (const t of page.transactions) rows.push([t.hash, String(t.height), t.timestamp, t.direction, uaethOf(t.amount) || t.amount, String(t.code)]);
  const blob = new Blob([rows.map((r) => r.map((c) => `"${c.replace(/"/g, '""')}"`).join(",")).join("\n")], { type: "text/csv" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = `${page.address}.csv`;
  a.click();
  URL.revokeObjectURL(url);
}

const HIST_COLS = "36px minmax(0,1fr) 120px 170px 90px";

/** A non-AETH transfer amount ("5000000ibc/…") in its own asset, or as given. */
function otherAmount(amount: string, k: "in" | "out"): string {
  const m = /^(\d+)([^,]+)$/.exec(amount || "");
  if (!m) return amount || "—";
  return `${k === "in" ? "+" : "−"}${amountIn(m[1], m[2])}`;
}

export default function Address() {
  const { address = "" } = useParams();
  const page = useApi(() => api.address(address), [address]);
  const grants = useApi(() => api.grants(address), [address]);
  const p = page.data;

  const available = p?.balance || "0";
  const escrowed = p?.escrow?.balance || "0";
  const total = (BigInt(available) + BigInt(escrowed)).toString();
  const hasEscrow = BigInt(escrowed) > 0n;
  const share = (v: string) => (BigInt(total) > 0n ? Number((BigInt(v) * 10000n) / BigInt(total)) / 100 : 0);
  const latest = p?.transactions[0];
  // Tokens other than AETH (USDC, or anything else that arrived over IBC).
  const others = (p?.balances ?? []).filter((c) => c.denom !== "uaeth");

  return (
    <div className="page">
      <div className="crumbs">
        <span>Address</span>
      </div>
      <div style={{ display: "flex", alignItems: "center", gap: 14, marginTop: 12, flexWrap: "wrap" }}>
        <div className="mono break" style={{ fontSize: 22, fontWeight: 600, letterSpacing: "-0.01em" }}>
          {address}
        </div>
        <CopyButton value={address} />
        {p && (
          <span className="pill plain" style={{ letterSpacing: 0, textTransform: "none", fontWeight: 500, fontSize: 12, borderRadius: 7, padding: "4px 10px" }}>
            {p.isValidator ? "Validator" : "Account"}
          </span>
        )}
        {p?.banned && <span className="pill bad">Banned from mining</span>}
      </div>

      <ErrorBanner error={page.error} />

      {page.loading && !p ? (
        <div className="loading">Loading…</div>
      ) : p ? (
        <>
          <div className="split even gap-top-lg" style={{ marginTop: 24 }}>
            <div className="card-glow" style={{ padding: 24, borderRadius: 18 }}>
              <div className="eyebrow red">Total balance</div>
              <div className="big-balance">
                {aethNumber(total)} <small>AETH</small>
              </div>
              <div className="mono muted" style={{ fontSize: 13, marginTop: 8 }}>
                {int(total)} uaeth
              </div>
              <div className="stack-bar" style={{ marginTop: 22 }}>
                {BigInt(total) > 0n ? (
                  <>
                    <div style={{ width: `${share(available)}%`, background: "var(--red)" }} />
                    {hasEscrow && <div style={{ width: `${share(escrowed)}%`, background: "var(--text-2)" }} />}
                  </>
                ) : (
                  <div style={{ width: "100%", background: "var(--cell)" }} />
                )}
              </div>
              <div className="metric-grid" style={{ marginTop: 16 }}>
                <div>
                  <div className="muted" style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 12 }}>
                    <span className="swatch" style={{ background: "var(--red)" }} />
                    Available
                  </div>
                  <div className="v">{aethNumber(available)}</div>
                </div>
                <div title="Mining rewards x/pow holds back from an active validator until the bond cooldown passes">
                  <div className="muted" style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 12 }}>
                    <span className="swatch" style={{ background: "var(--text-2)" }} />
                    Escrowed
                  </div>
                  <div className="v">{aethNumber(escrowed)}</div>
                </div>
                <div>
                  <div className="muted" style={{ fontSize: 12 }}>{hasEscrow ? "Unlocks at" : "Escrow"}</div>
                  <div className="v" style={{ fontSize: 15 }}>
                    {hasEscrow && p.escrow.unlockHeight ? (
                      <Link to={`/blocks/${p.escrow.unlockHeight}`} style={{ color: "inherit" }}>
                        block {int(p.escrow.unlockHeight)}
                      </Link>
                    ) : (
                      <span className="muted">none</span>
                    )}
                  </div>
                </div>
              </div>
              {others.length > 0 && (
                <div style={{ marginTop: 18, borderTop: "1px solid var(--rule)", paddingTop: 14 }}>
                  <div className="eyebrow">Other tokens</div>
                  {others.map((c) => {
                    const known = c.symbol ? assetOf(c.denom) : undefined;
                    return (
                      <div key={c.denom} className="mono" style={{ display: "flex", justifyContent: "space-between", gap: 12, fontSize: 13, marginTop: 8 }}
                        title={known ? `${known.origin} · ${c.denom}` : `${c.denom} · not a token this explorer names: it may look like one but arrived another way`}>
                        <span className={known ? "" : "muted"}>{known ? assetLabel(known) : `${shortDenom(c.denom)} · not recognized`}</span>
                        <span>{known ? amountIn(c.amount, c.denom) : int(c.amount)}</span>
                      </div>
                    );
                  })}
                </div>
              )}
            </div>

            <div className="stack">
              <div className="card card-pad metric-grid">
                <div>
                  <div className="muted" style={{ fontSize: 12 }}>Txs signed</div>
                  <div className="v" style={{ fontSize: 20 }}>{int(p.txsSigned)}</div>
                </div>
                <div>
                  <div className="muted" style={{ fontSize: 12 }}>Last active</div>
                  <div className="v" style={{ fontSize: 15, marginTop: 6 }} title={latest ? utc(latest.timestamp) : undefined}>
                    {latest ? timeAgo(latest.timestamp) : "—"}
                  </div>
                </div>
                <div>
                  <div className="muted" style={{ fontSize: 12 }}>Latest block</div>
                  <div className="v" style={{ fontSize: 15, marginTop: 6 }}>
                    {latest ? (
                      <Link to={`/blocks/${latest.height}`} style={{ color: "inherit" }}>
                        {int(latest.height)}
                      </Link>
                    ) : (
                      "—"
                    )}
                  </div>
                </div>
              </div>
              {grants.data ? (
                <GrantsPanel grants={grants.data} />
              ) : (
                <div className="card card-pad" style={{ flex: 1 }}>
                  <div className="eyebrow">Spending permissions</div>
                  <div className="muted" style={{ marginTop: 14, fontSize: 13 }}>
                    {grants.error ? `Couldn't load: ${grants.error}` : "Loading…"}
                  </div>
                </div>
              )}
            </div>
          </div>

          <div className="card gap-top">
            <div className="card-head">
              <span className="card-title">History</span>
              <span className="mono muted" style={{ fontSize: 12 }}>
                latest {p.transactions.length}
              </span>
              {p.transactions.length > 0 && (
                <button className="chip" style={{ marginLeft: "auto", border: "none", color: "var(--red)", padding: 0 }} onClick={() => downloadCSV(p)}>
                  Export CSV
                </button>
              )}
            </div>
            {p.transactions.length > 0 ? (
              <div className="scroll-x">
                <div className="rows" style={{ minWidth: 600 }}>
                  {p.transactions.map((t) => {
                    const k = kindOf(t);
                    const u = uaethOf(t.amount);
                    const color = t.code !== 0 ? "var(--danger)" : k.k === "in" ? "var(--green)" : "var(--red)";
                    const bg = t.code !== 0 ? "var(--danger-12)" : k.k === "in" ? "rgba(22,199,132,.12)" : "var(--red-14)";
                    return (
                      <div key={t.hash} className="row hover" style={{ gridTemplateColumns: HIST_COLS, padding: "12px 22px" }}>
                        <div className="type-tile" style={{ color, background: bg, fontSize: 13 }}>
                          {t.code !== 0 ? "✕" : k.icon}
                        </div>
                        <div style={{ minWidth: 0 }}>
                          <div style={{ fontSize: 14, fontWeight: 500 }}>
                            {k.label}
                            {t.code !== 0 && <span className="danger status-text" style={{ marginLeft: 8 }}>FAILED ({t.code})</span>}
                          </div>
                          <div style={{ fontSize: 12 }}>
                            <TxHash hash={t.hash} copy={false} />
                          </div>
                        </div>
                        <span className="mono muted" style={{ fontSize: 12 }}>
                          block{" "}
                          <Link to={`/blocks/${t.height}`} style={{ color: "var(--text-2)" }}>
                            {int(t.height)}
                          </Link>
                        </span>
                        <span className="right mono" style={{ fontSize: 14, fontWeight: 600, color: k.k === "in" ? "var(--green)" : "var(--bone)" }}>
                          {u ? `${k.k === "in" ? "+" : "−"}${aeth(u)}` : otherAmount(t.amount, k.k)}
                        </span>
                        <span className="right muted" style={{ fontSize: 12 }} title={utc(t.timestamp)}>
                          {timeAgo(t.timestamp)}
                        </span>
                      </div>
                    );
                  })}
                </div>
              </div>
            ) : (
              <div className="empty">No transactions found for this address.</div>
            )}
          </div>
        </>
      ) : null}
    </div>
  );
}
