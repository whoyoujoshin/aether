import type { Grants, Permission } from "../api";
import { AddressLink } from "./Hash";

// uaeth (a decimal string) as AETH, exactly: no floating point.
export function aeth(uaeth: string): string {
  const s = uaeth.replace(/^0+(?=\d)/, "").padStart(7, "0");
  const whole = s.slice(0, -6);
  const frac = s.slice(-6).replace(/0+$/, "");
  return `${Number(whole).toLocaleString("en-US")}${frac ? "." + frac : ""} AETH`;
}

function until(expiration: string): string {
  if (!expiration) return "no expiry";
  const d = new Date(expiration);
  return (d.getTime() < Date.now() ? "expired " : "until ") + d.toLocaleString();
}

function sendText(p: Permission): string {
  if (!p.send) return p.other.length ? p.other.join(", ") : "—";
  const amount = p.send.unlimited ? "any amount" : `${aeth(p.send.spendLimit)} more`;
  return `${amount}, ${until(p.send.expiration)}`;
}

function feesText(p: Permission): string {
  if (!p.fees) return "—";
  return `${p.fees.spendLimit ? `up to ${aeth(p.fees.spendLimit)} more` : "no limit"}, ${until(p.fees.expiration)}`;
}

function Table({ rows, who }: { rows: Permission[]; who: string }) {
  return (
    <table>
      <thead>
        <tr>
          <th>{who}</th>
          <th>May send</th>
          <th>To</th>
          <th>Fees paid</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((p) => (
          <tr key={p.account}>
            <td>
              <AddressLink address={p.account} />
            </td>
            <td className="mono">{sendText(p)}</td>
            <td className="mono">
              {!p.send ? "—" : p.send.allowList.length ? p.send.allowList.map((a) => <AddressLink key={a} address={a} />) : "anyone"}
            </td>
            <td className="mono">{feesText(p)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

/** Chain-enforced spending permissions (x/authz) and fee allowances (x/feegrant). */
export function GrantsPanel({ grants }: { grants: Grants }) {
  if (!grants.active) return null;
  const none = grants.given.length === 0 && grants.received.length === 0;
  return (
    <div className="panel">
      <div className="panel-header">
        <div className="panel-title">Spending permissions</div>
        <div className="panel-meta">x/authz grants and x/feegrant allowances, enforced by the chain</div>
      </div>
      {none && <div className="empty-state">This address hasn't given or received any.</div>}
      {grants.given.length > 0 && (
        <>
          <div className="panel-subtitle">Accounts that may spend from this address</div>
          <Table rows={grants.given} who="Grantee" />
        </>
      )}
      {grants.received.length > 0 && (
        <>
          <div className="panel-subtitle">Accounts this address may spend from</div>
          <Table rows={grants.received} who="Granter" />
        </>
      )}
    </div>
  );
}
