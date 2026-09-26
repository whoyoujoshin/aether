import { api } from "../api";
import { useApi } from "../hooks";
import { AddressLink } from "../components/Hash";

function formatTenure(ratio: string): string {
  const n = Number(ratio);
  if (Number.isNaN(n)) return ratio;
  return `${(n * 100).toFixed(1)}%`;
}

function formatEnteredAt(unix: number): string {
  if (!unix) return "—";
  return new Date(unix * 1000).toLocaleString();
}

export default function Validators() {
  const validators = useApi(api.validators, [], 10000);
  const leaderboard = useApi(() => api.leaderboard(), [], 10000);

  // ?? []: an explorer API from before the leaderboard DTO leaves
  // "entries" out entirely when nobody has mined this epoch yet.
  const entries = leaderboard.data?.entries ?? [];
  const maxWork = Math.max(1, ...entries.map((e) => e.work));

  return (
    <div className="page">
      <h1 className="page-title">Validators &amp; Mining</h1>

      <div className="panel">
        <div className="panel-header">
          <div className="panel-title">Active Validator Set</div>
          <div className="panel-meta">tenure ratio = time spent bonded / total eligible time</div>
        </div>
        {validators.error && <div className="error-banner" style={{ margin: 16 }}>{validators.error}</div>}
        {validators.loading && !validators.data ? (
          <div className="loading">Loading…</div>
        ) : validators.data && validators.data.length > 0 ? (
          <table>
            <thead>
              <tr>
                <th>Address</th>
                <th>Tenure Ratio</th>
                <th>Entered At</th>
              </tr>
            </thead>
            <tbody>
              {validators.data.map((v) => (
                <tr key={v.address}>
                  <td>
                    <AddressLink address={v.address} />
                  </td>
                  <td className="mono">{formatTenure(v.tenureRatio)}</td>
                  <td className="mono">{formatEnteredAt(v.enteredAtUnix)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <div className="empty-state">No active validators.</div>
        )}
      </div>

      <div className="panel">
        <div className="panel-header">
          <div className="panel-title">Miner Leaderboard</div>
          <div className="panel-meta">
            {leaderboard.data ? `epoch ${leaderboard.data.epoch}` : "current epoch"}
          </div>
        </div>
        {leaderboard.error && <div className="error-banner" style={{ margin: 16 }}>{leaderboard.error}</div>}
        {leaderboard.loading && !leaderboard.data ? (
          <div className="loading">Loading…</div>
        ) : entries.length > 0 ? (
          <table>
            <thead>
              <tr>
                <th>Rank</th>
                <th>Miner</th>
                <th>Work</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {entries.map((e, i) => (
                <tr key={e.address}>
                  <td className="mono">{i + 1}</td>
                  <td>
                    <AddressLink address={e.address} />
                  </td>
                  <td className="mono">{e.work.toLocaleString()}</td>
                  <td style={{ width: 160 }}>
                    <div className="progress-track">
                      <div className="progress-fill" style={{ width: `${(e.work / maxWork) * 100}%` }} />
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <div className="empty-state">No work submitted this epoch yet.</div>
        )}
      </div>
    </div>
  );
}
