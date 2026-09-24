import { useParams } from "react-router-dom";
import { api } from "../api";
import { useApi } from "../hooks";
import { AddressLink } from "../components/Hash";
import { TxStatusBadge } from "../components/StatusBadge";

export default function Transaction() {
  const { hash = "" } = useParams();
  const tx = useApi(() => api.tx(hash), [hash]);

  return (
    <div className="page">
      <h1 className="page-title">Transaction</h1>

      {tx.error && <div className="error-banner">{tx.error}</div>}

      {tx.loading && !tx.data ? (
        <div className="loading">Loading…</div>
      ) : tx.data ? (
        <>
          <div className="panel">
            <div className="panel-header">
              <div className="panel-title">Overview</div>
              <TxStatusBadge code={tx.data.code} />
            </div>
            <table className="kv-table">
              <tbody>
                <tr>
                  <td>Hash</td>
                  <td className="mono" style={{ wordBreak: "break-all" }}>
                    {tx.data.hash}
                  </td>
                </tr>
                <tr>
                  <td>Height</td>
                  <td className="mono">{tx.data.height}</td>
                </tr>
                <tr>
                  <td>Timestamp</td>
                  <td className="mono">{tx.data.timestamp}</td>
                </tr>
                <tr>
                  <td>From</td>
                  <td>
                    <AddressLink address={tx.data.from} full />
                  </td>
                </tr>
                <tr>
                  <td>To</td>
                  <td>
                    <AddressLink address={tx.data.to} full />
                  </td>
                </tr>
                <tr>
                  <td>Amount</td>
                  <td className="mono">{tx.data.amount}</td>
                </tr>
                <tr>
                  <td>Gas Used / Wanted</td>
                  <td className="mono">
                    {tx.data.gasUsed} / {tx.data.gasWanted}
                  </td>
                </tr>
                {tx.data.rawLog && (
                  <tr>
                    <td>Raw Log</td>
                    <td className="mono" style={{ wordBreak: "break-all" }}>
                      {tx.data.rawLog}
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>

          {tx.data.transfers.length > 0 && (
            <div className="panel">
              <div className="panel-header">
                <div className="panel-title">Transfers</div>
              </div>
              <table>
                <thead>
                  <tr>
                    <th>From</th>
                    <th>To</th>
                    <th>Amount</th>
                  </tr>
                </thead>
                <tbody>
                  {tx.data.transfers.map((t, i) => (
                    <tr key={i}>
                      <td>
                        <AddressLink address={t.from} />
                      </td>
                      <td>
                        <AddressLink address={t.to} />
                      </td>
                      <td className="mono">{t.amount}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          {tx.data.auxPow && (
            <div className="panel">
              <div className="panel-header">
                <div className="panel-title">Merged Mining (AuxPoW)</div>
              </div>
              <table className="kv-table">
                <tbody>
                  <tr>
                    <td>Parent Header</td>
                    <td className="mono" style={{ wordBreak: "break-all" }}>
                      {tx.data.auxPow.parentHeaderBase64}
                    </td>
                  </tr>
                  <tr>
                    <td>Coinbase Tx</td>
                    <td className="mono" style={{ wordBreak: "break-all" }}>
                      {tx.data.auxPow.coinbaseTxBase64}
                    </td>
                  </tr>
                  <tr>
                    <td>Aux Block Hash</td>
                    <td className="mono" style={{ wordBreak: "break-all" }}>
                      {tx.data.auxPow.auxBlockHashBase64}
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          )}
        </>
      ) : null}
    </div>
  );
}
