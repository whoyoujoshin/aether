import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api, BlockSummary } from "../api";
import { useApi } from "../hooks";
import { TopBar, useSearch } from "../components/TopBar";
import { TxHash } from "../components/Hash";
import { TxStatusText } from "../components/StatusBadge";
import { ErrorBanner, Legend, StackBar, TypeTile } from "../components/ui";
import { aeth, compact, int, timeAgo, typeColor, uaethOf, duration } from "../format";

const STRIP = 7; // blocks drawn in the chain strip
const CELLS = 16; // tx cells per block tile

/** Mean seconds between the given blocks (newest first); 0 if unknown. */
function blockInterval(blocks: BlockSummary[]): number {
  if (blocks.length < 2) return 0;
  const newest = new Date(blocks[0].time).getTime();
  const oldest = new Date(blocks[blocks.length - 1].time).getTime();
  const s = (newest - oldest) / 1000 / (blocks.length - 1);
  return s > 0 ? s : 0;
}

/** Ticks once a second, for the countdown and block ages. */
function useNow(): number {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, []);
  return now;
}

export default function Dashboard() {
  const stats = useApi(api.stats, [], 6000);
  const blocks = useApi(api.blocks, [], 6000);
  const recent = useApi(() => api.recentTransactions(100), [], 6000);
  const search = useSearch();
  const now = useNow();

  const s = stats.data;
  const bl = blocks.data ?? [];
  const interval = blockInterval(bl);
  const head = bl[0];
  const sinceHead = head ? (now - new Date(head.time).getTime()) / 1000 : 0;
  const nextIn = interval ? Math.max(0, Math.round(interval - sinceHead)) : null;

  // Activity mix: message types across the recent transactions.
  const txs = recent.data ?? [];
  const counts = new Map<string, number>();
  for (const t of txs) counts.set(t.msgType || "other", (counts.get(t.msgType || "other") ?? 0) + 1);
  const sorted = [...counts.entries()].sort((a, b) => b[1] - a[1]);
  const top = sorted.slice(0, 4).map(([label, value]) => ({ label, value, color: typeColor(label) }));
  const rest = sorted.slice(4).reduce((a, [, v]) => a + v, 0);
  const mix = rest > 0 ? [...top, { label: "others", value: rest, color: "#6b645b" }] : top;

  // Epoch progress, when epochs are longer than a block.
  const epochLen = s?.epochLength ?? 0;
  const epochDone = s && epochLen > 1 ? s.latestHeight % epochLen : 0;
  const epochLeft = epochLen - epochDone;
  const cellSize = epochLen / 48;

  return (
    <>
      <div className="hero-bg">
        <TopBar home />
        <div className="hero">
          <h1>
            Explore <span>Aether Chain</span>
          </h1>
          <form className="hero-search" onSubmit={search.submit} role="search">
            <input
              placeholder="Search by address, transaction hash or block height"
              aria-label="Search by address, transaction hash or block height"
              value={search.query}
              onChange={(e) => search.setQuery(e.target.value)}
            />
            <button className="btn-red" type="submit">
              Search
            </button>
            {search.error && <div className="error-banner search-error">{search.error}</div>}
          </form>
          <div className="hero-stats">
            <span>
              height <b>{s ? int(s.latestHeight) : "—"}</b>
            </span>
            <span>
              epoch <b>{s ? int(s.currentEpoch) : "—"}</b>
            </span>
            <span>
              difficulty <b>{s?.difficulty ? compact(s.difficulty) : "—"}</b>
            </span>
            <span>
              reward <b>{s?.blockReward ? aeth(s.blockReward) : "—"}</b>
            </span>
            <span>
              treasury <b>{s?.treasuryBalance && uaethOf(s.treasuryBalance) ? aeth(uaethOf(s.treasuryBalance)) : s?.treasuryBalance || "—"}</b>
            </span>
          </div>
        </div>
      </div>

      <div className="overview">
        {(stats.error || blocks.error) && (
          <div style={{ padding: "0 32px" }}>
            <ErrorBanner error={stats.error || blocks.error} prefix="Failed to load chain data" />
          </div>
        )}

        <div className="overview-strip">
          <div style={{ display: "flex", justifyContent: "space-between", alignItems: "baseline", marginBottom: 14, gap: 12 }}>
            <Link to="/blocks" className="card-title" style={{ color: "var(--bone)" }}>
              Latest blocks
            </Link>
            <div className="mono muted" style={{ fontSize: 12, fontWeight: 500 }}>
              next <span className="red">#{head ? int(head.height + 1) : "—"}</span>
              {nextIn !== null && ` · ~${nextIn}s`}
            </div>
          </div>
          <div className="chain-strip">
            <div className="next-block">
              <div className="diamond" />
              <span>#{head ? int(head.height + 1) : "—"}</span>
            </div>
            {bl.slice(0, STRIP).map((b, i) => (
              <div key={b.height} style={{ display: "flex", alignItems: "center" }}>
                <div className={`chain-link${i === 0 ? " hot" : ""}`} />
                <Link to={`/blocks/${b.height}`} className={`block-tile${i === 0 ? " hot" : ""}`}>
                  <span className="h">{int(b.height)}</span>
                  <span className="mono muted" style={{ fontSize: 11 }}>
                    {b.hash.slice(0, 4).toLowerCase()}…{b.hash.slice(-4).toLowerCase()}
                  </span>
                  <div className="cells">
                    {Array.from({ length: CELLS }, (_, k) => (
                      <div key={k} className={k < b.numTxs ? "on" : ""} />
                    ))}
                  </div>
                  <span style={{ display: "flex", justifyContent: "space-between", fontSize: 11, color: "var(--muted)" }}>
                    <span>
                      {b.numTxs} tx{b.numTxs === 1 ? "" : "s"}
                    </span>
                    <span>{timeAgo(b.time)}</span>
                  </span>
                </Link>
              </div>
            ))}
            {blocks.loading && !blocks.data && <div className="loading" style={{ padding: "0 24px" }}>Loading…</div>}
          </div>
        </div>

        <div className="overview-grid split w320">
          <div className="stack">
            <div className="card card-pad">
              <div className="eyebrow">Activity mix · last {txs.length} txs</div>
              <div style={{ margin: "16px 0" }}>
                <StackBar segments={mix} glow />
              </div>
              {mix.length > 0 ? <Legend segments={mix} /> : <div className="muted" style={{ fontSize: 13 }}>No transactions yet.</div>}
            </div>

            <div className="card card-pad">
              <div style={{ display: "flex", justifyContent: "space-between" }}>
                <div className="eyebrow">Epoch {s ? int(s.currentEpoch) : "—"}</div>
                {epochLen > 1 && <div className="mono red" style={{ fontSize: 12, fontWeight: 500 }}>{Math.round((epochDone / epochLen) * 100)}%</div>}
              </div>
              {epochLen > 1 ? (
                <>
                  <div className="epoch-cells">
                    {Array.from({ length: 48 }, (_, i) => {
                      const cur = Math.floor(epochDone / cellSize);
                      return <div key={i} className={i === cur ? "now" : i < cur ? "done" : ""} />;
                    })}
                  </div>
                  <div className="muted" style={{ fontSize: 12, marginTop: 12 }}>
                    {int(epochLeft)} block{epochLeft === 1 ? "" : "s"} until epoch {int((s?.currentEpoch ?? 0) + 1)}
                    {interval > 0 && ` · ~${duration(epochLeft * interval)}`}
                  </div>
                </>
              ) : (
                <div className="muted" style={{ fontSize: 12, marginTop: 12 }}>
                  {s ? "Epochs are one block long on this network." : "—"}
                </div>
              )}
            </div>
          </div>

          <div className="card">
            <div className="card-head">
              <span className="card-title">Transactions</span>
              <span className="card-meta">auto-refresh 6s</span>
            </div>
            <ErrorBanner error={recent.error} />
            {recent.loading && !recent.data ? (
              <div className="loading">Loading…</div>
            ) : txs.length > 0 ? (
              txs.slice(0, 8).map((t) => (
                <div key={t.hash} className="row hover" style={{ gridTemplateColumns: "36px minmax(0,1fr) 120px 80px", gap: 14, padding: "12px 20px" }}>
                  <TypeTile type={t.msgType} />
                  <div style={{ minWidth: 0 }}>
                    <div style={{ fontSize: 14, fontWeight: 500 }} className="ellipsis">
                      {t.msgType || "Transaction"}
                    </div>
                    <div style={{ fontSize: 12 }}>
                      <TxHash hash={t.hash} copy={false} />
                    </div>
                  </div>
                  <div className="mono muted" style={{ fontSize: 12 }}>
                    block <Link to={`/blocks/${t.height}`} style={{ color: "var(--text-2)" }}>{int(t.height)}</Link>
                  </div>
                  <div className="right">
                    <TxStatusText code={t.code} />
                    <div className="muted" style={{ fontSize: 12, marginTop: 3 }}>
                      {timeAgo(t.timestamp)}
                    </div>
                  </div>
                </div>
              ))
            ) : (
              <div className="empty">No transactions yet.</div>
            )}
          </div>
        </div>
      </div>
    </>
  );
}
