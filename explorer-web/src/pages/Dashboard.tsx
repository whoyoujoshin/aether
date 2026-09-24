import { api } from "../api";
import { useApi } from "../hooks";
import { TxHash } from "../components/Hash";
import { TxStatusBadge } from "../components/StatusBadge";

function timeAgo(iso: string): string {
  if (!iso) return "—";
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return iso;
  const seconds = Math.max(0, Math.floor((Date.now() - then) / 1000));
  if (seconds < 60) return `${seconds}s ago`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86400)}d ago`;
}

export default function Dashboard() {
  const stats = useApi(api.stats, [], 6000);
  const recent = useApi(() => api.recentTransactions(15), [], 6000);

  return (
    <div className="page">
      <h1 className="page-title">Network Overview</h1>

      {stats.error && <div className="error-banner">Failed to load chain stats: {stats.error}</div>}

      <div className="stat-grid">
        <div className="stat-card">
          <div className="stat-label">Latest Height</div>
          <div className="stat-value">{stats.data?.latestHeight ?? "—"}</div>
        </div>
        <div className="stat-card">
          <div className="stat-label">Current Epoch</div>
          <div className="stat-value">{stats.data?.currentEpoch ?? "—"}</div>
        </div>
        <div className="stat-card">
          <div className="stat-label">Difficulty</div>
          <div className="stat-value">{stats.data?.difficulty ?? "—"}</div>
        </div>
        <div className="stat-card">
          <div className="stat-label">Block Reward</div>
          <div className="stat-value">{stats.data?.blockReward ?? "—"} uaeth</div>
        </div>
        <div className="stat-card">
          <div className="stat-label">Treasury Balance</div>
          <div className="stat-value">{stats.data?.treasuryBalance ?? "—"}</div>
        </div>
      </div>

      <div className="panel">
        <div className="panel-header">
          <div className="panel-title">Recent Transactions</div>
          <div className="panel-meta">auto-refreshes every 6s</div>
        </div>
        {recent.error && <div className="error-banner" style={{ margin: 16 }}>{recent.error}</div>}
        {recent.loading && !recent.data ? (
          <div className="loading">Loading…</div>
        ) : recent.data && recent.data.length > 0 ? (
          <table>
            <thead>
              <tr>
                <th>Hash</th>
                <th>Type</th>
                <th>Height</th>
                <th>Status</th>
                <th>Time</th>
              </tr>
            </thead>
            <tbody>
              {recent.data.map((tx) => (
                <tr key={tx.hash}>
                  <td>
                    <TxHash hash={tx.hash} />
                  </td>
                  <td>{tx.msgType || "—"}</td>
                  <td className="mono">{tx.height}</td>
                  <td>
                    <TxStatusBadge code={tx.code} />
                  </td>
                  <td className="mono">{timeAgo(tx.timestamp)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <div className="empty-state">No recent transactions.</div>
        )}
      </div>
    </div>
  );
}
