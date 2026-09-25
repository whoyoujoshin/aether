import { api } from "../api";
import { useApi } from "../hooks";
import { AddressLink } from "../components/Hash";

const schemeLabel: Record<string, string> = {
  "aether-memo": "Pay per request",
  "aether-prepaid": "Prepaid (agents)",
};

// Service URLs come from on-chain announcements: only ever link http(s).
function safeHref(url: string): string | undefined {
  return /^https?:\/\//i.test(url) ? url : undefined;
}

export default function Services() {
  const dir = useApi(api.services, [], 60000);
  const services = dir.data?.services ?? [];

  return (
    <div className="page">
      <h1 className="page-title">Service Directory</h1>

      <div className="panel">
        <div className="panel-header">
          <div className="panel-title">Paid APIs</div>
          <div className="panel-meta">
            listed on chain · each verified: its manifest names the account that listed it as payee
          </div>
        </div>
        {dir.error && <div className="error-banner" style={{ margin: 16 }}>{dir.error}</div>}
        {dir.loading && !dir.data ? (
          <div className="loading">Loading…</div>
        ) : services.length > 0 ? (
          <table>
            <thead>
              <tr>
                <th>Service</th>
                <th>Price / request</th>
                <th>Payment</th>
                <th title="Payments to the payee and ratings from paying accounts, over the last week or so. A seller can pay itself from other accounts, so treat these as hints.">
                  Recent use*
                </th>
                <th>Payee</th>
                <th>Listed at</th>
              </tr>
            </thead>
            <tbody>
              {services.map((s) => (
                <tr key={`${s.payTo}|${s.url}`}>
                  <td>
                    <div>{s.name || "(unnamed)"}</div>
                    {s.description && <div className="panel-meta">{s.description}</div>}
                    {safeHref(s.url) ? (
                      <a className="mono" href={safeHref(s.url)} target="_blank" rel="noopener noreferrer nofollow">
                        {s.url}
                      </a>
                    ) : (
                      <span className="mono">{s.url}</span>
                    )}
                  </td>
                  <td className="mono">{s.priceAeth} AETH</td>
                  <td>{s.schemes.map((x) => schemeLabel[x] ?? x).join(", ")}</td>
                  <td>
                    {s.activity ? (
                      <>
                        <div>
                          {s.activity.payers} payer{s.activity.payers === 1 ? "" : "s"} · {s.activity.payments} paid · {s.activity.volumeAeth} AETH
                        </div>
                        <div className="panel-meta">
                          {s.activity.ratings > 0
                            ? `★ ${s.activity.averageScore?.toFixed(1)} from ${s.activity.ratings} paying rater${s.activity.ratings === 1 ? "" : "s"}`
                            : "no ratings yet"}
                        </div>
                      </>
                    ) : (
                      <span className="panel-meta">—</span>
                    )}
                  </td>
                  <td>
                    <AddressLink address={s.payTo} />
                  </td>
                  <td className="mono">{s.listedAtHeight}</td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <div className="empty-state">No services listed yet.</div>
        )}
      </div>

      {dir.data && (
        <div className="panel">
          <div className="panel-header">
            <div className="panel-title">List a service</div>
          </div>
          <div style={{ padding: 16 }}>
            Run your API behind <span className="mono">cmd/paywall</span> or the Node/Python seller kits (they serve the manifest), then send 1 uaeth
            from the payee account to <span className="mono">{dir.data.directoryAddress}</span> with memo{" "}
            <span className="mono">x402-service:&lt;your URL&gt;</span>. Memo{" "}
            <span className="mono">x402-delist:&lt;your URL&gt;</span> removes it. Names and descriptions are set by
            each service, not verified.
            <p className="panel-meta">
              * Buyers rate a service with 1 uaeth to the same address, memo{" "}
              <span className="mono">x402-rate:&lt;1-5&gt;:&lt;URL&gt;</span>; a rating counts only from an account
              that paid the service first. Fees are zero, so a seller could pay itself from accounts it controls:
              agents give weight to ratings from accounts they trust, and to their own experience.
            </p>
          </div>
        </div>
      )}
    </div>
  );
}
