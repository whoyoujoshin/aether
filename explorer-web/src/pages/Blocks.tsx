import { Link } from "react-router-dom";
import { api } from "../api";
import { useApi } from "../hooks";
import { ErrorBanner } from "../components/ui";
import { int, timeAgo, truncate, utc } from "../format";

const COLS = "120px minmax(0,1.4fr) minmax(0,1fr) 70px 110px";

export default function Blocks() {
  const blocks = useApi(api.blocks, [], 6000);
  const list = blocks.data ?? [];
  const maxTxs = Math.max(1, ...list.map((b) => b.numTxs));

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1 className="page-title">Blocks</h1>
          <div className="page-sub">The latest blocks, signed by Aether's validators. Miners' proof-of-work arrives inside them as transactions.</div>
        </div>
      </div>

      <div className="card gap-top-lg">
        <div className="card-head">
          <span className="card-title">Recent blocks</span>
          <span className="card-meta">auto-refresh 6s</span>
        </div>
        <ErrorBanner error={blocks.error} />
        {blocks.loading && !blocks.data ? (
          <div className="loading">Loading…</div>
        ) : list.length > 0 ? (
          <div className="scroll-x">
            <div className="rows">
              <div className="row th" style={{ gridTemplateColumns: COLS }}>
                <span>Height</span>
                <span>Hash</span>
                <span>Proposer</span>
                <span className="right">Txs</span>
                <span className="right">Age</span>
              </div>
              {list.map((b, i) => (
                <div key={b.height} className="row hover" style={{ gridTemplateColumns: COLS }}>
                  <Link to={`/blocks/${b.height}`} className="mono" style={{ fontWeight: 600, color: i === 0 ? "var(--red)" : "var(--text-2)" }}>
                    {int(b.height)}
                  </Link>
                  <span className="mono muted ellipsis" style={{ fontSize: 13 }}>
                    {truncate(b.hash, 14, 8)}
                  </span>
                  <span className="mono muted ellipsis" style={{ fontSize: 12 }}>
                    {truncate(b.proposerAddress.toUpperCase(), 8, 4)}
                  </span>
                  <span className="right mono" style={{ display: "flex", alignItems: "center", gap: 8, justifyContent: "flex-end", fontSize: 13 }}>
                    <span className="mini-bar" style={{ width: 28 }}>
                      <span style={{ width: `${(b.numTxs / maxTxs) * 100}%` }} />
                    </span>
                    {b.numTxs}
                  </span>
                  <span className="right muted" style={{ fontSize: 13 }} title={utc(b.time)}>
                    {timeAgo(b.time)}
                  </span>
                </div>
              ))}
            </div>
          </div>
        ) : (
          <div className="empty">No blocks found.</div>
        )}
      </div>
    </div>
  );
}
