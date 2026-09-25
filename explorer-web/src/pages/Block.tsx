import { useParams, Link } from "react-router-dom";
import { api } from "../api";
import { useApi } from "../hooks";
import { TxHash } from "../components/Hash";

export default function Block() {
  const { height: heightParam } = useParams();
  const height = Number(heightParam);
  const block = useApi(() => api.block(height), [height]);

  return (
    <div className="page">
      <h1 className="page-title">Block #{heightParam}</h1>

      {block.error && <div className="error-banner">{block.error}</div>}

      {block.loading && !block.data ? (
        <div className="loading">Loading…</div>
      ) : block.data ? (
        <>
          <div className="panel">
            <div className="panel-header">
              <div className="panel-title">Overview</div>
              <div className="panel-meta">
                <Link to={`/blocks/${block.data.height - 1}`}>&larr; prev</Link>
                {"  "}
                <Link to={`/blocks/${block.data.height + 1}`}>next &rarr;</Link>
              </div>
            </div>
            <table className="kv-table">
              <tbody>
                <tr>
                  <td>Height</td>
                  <td className="mono">{block.data.height}</td>
                </tr>
                <tr>
                  <td>Block Hash</td>
                  <td className="mono" style={{ wordBreak: "break-all" }}>
                    {block.data.hash}
                  </td>
                </tr>
                <tr>
                  <td>Time</td>
                  <td className="mono">{block.data.time}</td>
                </tr>
                <tr>
                  <td>Proposer</td>
                  <td className="mono" style={{ wordBreak: "break-all" }}>
                    {block.data.proposerAddress}
                  </td>
                </tr>
                <tr>
                  <td>App Hash</td>
                  <td className="mono" style={{ wordBreak: "break-all" }}>
                    {block.data.appHash}
                  </td>
                </tr>
                <tr>
                  <td>Last Commit Hash</td>
                  <td className="mono" style={{ wordBreak: "break-all" }}>
                    {block.data.lastCommitHash || "—"}
                  </td>
                </tr>
                <tr>
                  <td>Data Hash</td>
                  <td className="mono" style={{ wordBreak: "break-all" }}>
                    {block.data.dataHash || "—"}
                  </td>
                </tr>
                <tr>
                  <td>Transactions</td>
                  <td className="mono">{block.data.numTxs}</td>
                </tr>
              </tbody>
            </table>
          </div>

          <div className="panel">
            <div className="panel-header">
              <div className="panel-title">Transactions ({block.data.txHashes.length})</div>
            </div>
            {block.data.txHashes.length > 0 ? (
              <table>
                <thead>
                  <tr>
                    <th>Hash</th>
                  </tr>
                </thead>
                <tbody>
                  {block.data.txHashes.map((h) => (
                    <tr key={h}>
                      <td>
                        <TxHash hash={h} full />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            ) : (
              <div className="empty-state">No transactions in this block.</div>
            )}
          </div>
        </>
      ) : null}
    </div>
  );
}
