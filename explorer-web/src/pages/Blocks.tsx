import { api } from "../api";
import { useApi } from "../hooks";
import { BlockLink, truncate } from "../components/Hash";

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

export default function Blocks() {
  const blocks = useApi(api.blocks, [], 6000);

  return (
    <div className="page">
      <h1 className="page-title">Blocks</h1>

      <div className="panel">
        <div className="panel-header">
          <div className="panel-title">Recent Blocks</div>
          <div className="panel-meta">auto-refreshes every 6s</div>
        </div>
        {blocks.error && <div className="error-banner" style={{ margin: 16 }}>{blocks.error}</div>}
        {blocks.loading && !blocks.data ? (
          <div className="loading">Loading…</div>
        ) : blocks.data && blocks.data.length > 0 ? (
          <table>
            <thead>
              <tr>
                <th>Height</th>
                <th>Hash</th>
                <th>Proposer</th>
                <th>Txs</th>
                <th>Time</th>
              </tr>
            </thead>
            <tbody>
              {blocks.data.map((b) => (
                <tr key={b.height}>
                  <td>
                    <BlockLink height={b.height} />
                  </td>
                  <td className="mono">{truncate(b.hash)}</td>
                  <td className="mono">{truncate(b.proposerAddress, 8, 4)}</td>
                  <td className="mono">{b.numTxs}</td>
                  <td className="mono">{timeAgo(b.time)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <div className="empty-state">No blocks found.</div>
        )}
      </div>
    </div>
  );
}
