import type { Grants, Permission } from "../api";
import { aeth } from "../format";
import { AddressLink } from "./Hash";

function until(expiration: string): string {
  if (!expiration) return "no expiry";
  const d = new Date(expiration);
  const when = d.toLocaleDateString("en-US", { month: "short", day: "numeric", year: "numeric" });
  return (d.getTime() < Date.now() ? "expired " : "expires ") + when;
}

function sendText(p: Permission): string {
  if (!p.send) return p.other.length ? p.other.join(", ") : "";
  const amount = p.send.unlimited ? "any amount" : `up to ${aeth(p.send.spendLimit)} more`;
  const to = p.send.allowList.length ? ` to ${p.send.allowList.length} allowed address${p.send.allowList.length === 1 ? "" : "es"}` : "";
  return `${amount}${to} · ${until(p.send.expiration)}`;
}

function feesText(p: Permission): string {
  if (!p.fees) return "";
  return `fees ${p.fees.spendLimit ? `up to ${aeth(p.fees.spendLimit)} more` : "with no limit"} · ${until(p.fees.expiration)}`;
}

function Row({ p }: { p: Permission }) {
  return (
    <div className="grant-row">
      <AddressLink address={p.account} />
      <span className="muted">may</span>
      {p.send && (
        <span className="tag" style={{ color: "var(--red)", background: "var(--red-14)" }}>
          MsgSend
        </span>
      )}
      {p.fees && (
        <span className="tag" style={{ color: "oklch(0.76 0.13 235)", background: "color-mix(in oklch, oklch(0.76 0.13 235) 14%, transparent)" }}>
          pay fees
        </span>
      )}
      {!p.send && !p.fees && p.other.map((o) => <span key={o} className="tag" style={{ color: "var(--muted)", background: "var(--cell)" }}>{o.split(".").pop()}</span>)}
      <span className="muted" style={{ marginLeft: "auto" }}>
        {[sendText(p), feesText(p)].filter(Boolean).join(" · ")}
      </span>
      {p.send && p.send.allowList.length > 0 && (
        <div style={{ width: "100%", display: "flex", gap: 10, flexWrap: "wrap", fontSize: 12 }}>
          <span className="faint">only to</span>
          {p.send.allowList.map((a) => (
            <AddressLink key={a} address={a} plain />
          ))}
        </div>
      )}
    </div>
  );
}

/** Chain-enforced spending permissions (x/authz) and fee allowances (x/feegrant). */
export function GrantsPanel({ grants }: { grants: Grants }) {
  return (
    <div className="card card-pad" style={{ flex: 1 }}>
      <div className="eyebrow">Spending permissions</div>
      {!grants.active ? (
        <div className="muted" style={{ marginTop: 14, fontSize: 13 }}>
          x/authz and x/feegrant aren't active on this chain yet.
        </div>
      ) : grants.given.length === 0 && grants.received.length === 0 ? (
        <div className="muted" style={{ marginTop: 14, fontSize: 13 }}>
          This address hasn't given or received any. Grants are enforced by the chain: a limit and an expiry, revocable at any time.
        </div>
      ) : (
        <>
          {grants.given.length > 0 && (
            <div style={{ marginTop: 14 }}>
              <div className="faint" style={{ fontSize: 12 }}>May spend from this address</div>
              {grants.given.map((p) => (
                <Row key={p.account} p={p} />
              ))}
            </div>
          )}
          {grants.received.length > 0 && (
            <div style={{ marginTop: 14 }}>
              <div className="faint" style={{ fontSize: 12 }}>This address may spend from</div>
              {grants.received.map((p) => (
                <Row key={p.account} p={p} />
              ))}
            </div>
          )}
        </>
      )}
    </div>
  );
}
