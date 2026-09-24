import { useParams } from "react-router-dom";
import { api } from "../api";
import { useApi } from "../hooks";
import { TxHash, AddressLink } from "../components/Hash";
import { TxStatusBadge } from "../components/StatusBadge";

export default function Address() {
  const { address = "" } = useParams();
  const page = useApi(() => api.address(address), [address]);

  return (
    <div className="page">
      <h1 className="page-title">Address</h1>

      {page.error && <div className="error-banner">{page.error}</div>}

      {page.loading && !page.data ? (
        <div className="loading">Loading…</div>
      ) : page.data ? (
        <>
          <div className="panel">
            <div className="panel-header">
              <div className="panel-title">Overview</div>
            </div>
            <table className="kv-table">
              <tbody>
                <tr>
                  <td>Address</td>
                  <td>
                    <AddressLink address={page.data.address} full />
                  </td>
                </tr>
                <tr>
                  <td>Balance</td>
                  <td className="mono">{page.data.balance}</td>
                </tr>
              </tbody>
            </table>
          </div>

          <div className="panel">
            <div className="panel-header">
              <div className="panel-title">Transactions</div>
            </div>
            {page.data.transactions.length > 0 ? (
              <table>
                <thead>
                  <tr>
                    <th>Hash</th>
                    <th>Height</th>
                    <th>Direction</th>
                    <th>Amount</th>
                    <th>Status</th>
                    <th>Time</th>
                  </tr>
                </thead>
                <tbody>
                  {page.data.transactions.map((tx) => (
                    <tr key={tx.hash}>
                      <td>
                        <TxHash hash={tx.hash} />
                      </td>
                      <td className="mono">{tx.height}</td>
                      <td>{tx.direction}</td>
                      <td className="mono">{tx.amount}</td>
                      <td>
                        <TxStatusBadge code={tx.code} />
                      </td>
                      <td className="mono">{tx.timestamp}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            ) : (
              <div className="empty-state">No transactions found for this address.</div>
            )}
          </div>
        </>
      ) : null}
    </div>
  );
}
