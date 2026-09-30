import { api, ServiceListing } from "../api";
import { useApi } from "../hooks";
import { AddressLink, BlockLink, CopyButton, TxHash } from "../components/Hash";
import { ErrorBanner } from "../components/ui";
import { amountIn, assetOf, int, shortDenom } from "../format";

/** What a service was paid, in its own asset (the backend sends a decimal for a known asset, base units otherwise). */
function volumeText(s: ServiceListing): string {
  const a = s.activity;
  if (!a) return "";
  if (a.volumeAeth) return `${a.volumeAeth} AETH`;
  const denom = s.asset || "uaeth";
  const known = assetOf(denom);
  return known ? `${a.volume ?? "0"} ${known.symbol}` : `${int(a.volume ?? "0")} ${shortDenom(denom)}`;
}

const schemeLabel: Record<string, string> = {
  "aether-memo": "Pay per request",
  "aether-prepaid": "Prepaid (agents)",
  "aether-pull": "Capped allowance",
};

// Service URLs come from on-chain announcements: only ever link http(s).
function safeHref(url: string): string | undefined {
  return /^https?:\/\//i.test(url) ? url : undefined;
}

function initials(name: string): string {
  const words = name.replace(/[^A-Za-z0-9 ]/g, " ").trim().split(/\s+/).filter(Boolean);
  if (words.length === 0) return "API";
  return (words.length === 1 ? words[0].slice(0, 3) : words.slice(0, 3).map((w) => w[0]).join("")).toUpperCase();
}

function ServiceCard({ s }: { s: ServiceListing }) {
  const href = safeHref(s.url);
  const a = s.activity;
  return (
    <div className="svc-card">
      <div style={{ display: "flex", alignItems: "center", gap: 12, minWidth: 0 }}>
        <div className="abbr-tile">{initials(s.name)}</div>
        <div style={{ minWidth: 0 }}>
          <div style={{ fontSize: 17, fontWeight: 600 }} className="ellipsis">
            {s.name || "(unnamed)"}
          </div>
          {s.description && (
            <div className="muted" style={{ fontSize: 13, marginTop: 2 }}>
              {s.description}
            </div>
          )}
        </div>
        <div className="mono nowrap" style={{ marginLeft: "auto", textAlign: "right" }}>
          <div style={{ fontSize: 15, fontWeight: 600 }} title={assetOf(s.asset || "uaeth") ? undefined : `${s.asset}: a token this explorer doesn't name, whatever the service calls it`}>
            {amountIn(s.price, s.asset || "uaeth")}
          </div>
          <div className="muted" style={{ fontSize: 11 }}>
            per request
          </div>
        </div>
      </div>

      <div className="url-box">
        {href ? (
          <a href={href} target="_blank" rel="noopener noreferrer nofollow" style={{ color: "var(--text-2)" }}>
            {s.url}
          </a>
        ) : (
          <span>{s.url}</span>
        )}
        <CopyButton value={s.url} />
      </div>

      <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
        {s.schemes.map((x) => (
          <span key={x} className="tag" style={{ color: "var(--red)", background: "var(--red-14)" }} title={x}>
            {schemeLabel[x] ?? x}
          </span>
        ))}
        {s.minDeposit && (
          <span className="tag" style={{ color: "var(--muted)", background: "var(--cell)" }}>
            min deposit {amountIn(s.minDeposit, s.asset || "uaeth")}
          </span>
        )}
      </div>

      <div
        className="mono muted"
        style={{ display: "flex", justifyContent: "space-between", gap: "6px 16px", fontSize: 12, flexWrap: "wrap" }}
        title="Payments to the payee and ratings from paying accounts, over the last week or so. A seller can pay itself from other accounts, so treat these as hints."
      >
        {a ? (
          <>
            <span>
              payers <span style={{ color: "var(--bone)" }}>{a.payers}</span>
            </span>
            <span>
              paid <span style={{ color: "var(--bone)" }}>{a.payments}×</span> · <span style={{ color: "var(--bone)" }}>{volumeText(s)}</span>
            </span>
            <span>{a.ratings > 0 ? <>★ <span style={{ color: "var(--bone)" }}>{a.averageScore?.toFixed(1)}</span> from {a.ratings}</> : "no ratings yet"}</span>
          </>
        ) : (
          <span>no recent use</span>
        )}
      </div>

      <div style={{ display: "flex", gap: "6px 16px", fontSize: 12, flexWrap: "wrap", borderTop: "1px solid var(--rule)", paddingTop: 12 }} className="muted">
        <span style={{ display: "flex", gap: 6, alignItems: "center", minWidth: 0 }}>
          payee <AddressLink address={s.payTo} copy={false} />
        </span>
        <span style={{ marginLeft: "auto", display: "flex", gap: 6, alignItems: "center" }}>
          listed at <BlockLink height={s.listedAtHeight} /> · <TxHash hash={s.txHash} copy={false} />
        </span>
      </div>
    </div>
  );
}

export default function Services() {
  const dir = useApi(api.services, [], 60000);
  const services = dir.data?.services ?? [];

  return (
    <div className="page">
      <div className="page-head" style={{ alignItems: "center" }}>
        <div>
          <h1 className="page-title">Services</h1>
          <div className="page-sub">Paid APIs listed on chain. Each one is checked: its manifest names the account that listed it as payee.</div>
        </div>
        {dir.data && (
          <div
            className="pill"
            style={{ marginLeft: "auto", padding: "10px 16px", borderRadius: 12, fontSize: 13, letterSpacing: 0, textTransform: "none", background: "var(--red-12)", border: "1px solid var(--red-35)", color: "var(--red)" }}
          >
            <span className="dot glow pulse" />
            {services.length} listed
          </div>
        )}
      </div>

      <ErrorBanner error={dir.error} />

      {dir.loading && !dir.data ? (
        <div className="loading">Loading…</div>
      ) : services.length > 0 ? (
        <div className="svc-grid">
          {services.map((s) => (
            <ServiceCard key={`${s.payTo}|${s.url}`} s={s} />
          ))}
        </div>
      ) : (
        <div className="card gap-top-lg">
          <div className="empty">No services listed yet.</div>
        </div>
      )}

      {dir.data && (
        <div className="card card-pad gap-top">
          <div style={{ display: "flex", alignItems: "center", gap: 12, flexWrap: "wrap" }}>
            <span className="card-title">List a service</span>
            <span className="muted" style={{ fontSize: 13 }}>
              run your API behind <span className="mono">cmd/paywall</span> or the Node/Python seller kits (they serve the manifest)
            </span>
          </div>
          <div className="prose" style={{ marginTop: 12 }}>
            <p>
              Send 1 uaeth from the payee account to the directory with memo <span className="mono">x402-service:&lt;your URL&gt;</span>. Memo{" "}
              <span className="mono">x402-delist:&lt;your URL&gt;</span> removes it. Names and descriptions are set by each service, not verified.
            </p>
            <p className="muted" style={{ fontSize: 13 }}>
              Buyers rate a service with 1 uaeth to the same address, memo <span className="mono">x402-rate:&lt;1-5&gt;:&lt;URL&gt;</span>; a rating counts only from
              an account that paid the service first. Fees are zero, so a seller could pay itself from accounts it controls: agents give weight to ratings
              from accounts they trust, and to their own experience.
            </p>
          </div>
        </div>
      )}

      {dir.data && (
        <div className="info-bar">
          <span style={{ fontWeight: 600 }}>Directory</span>
          <span className="mono dim break" style={{ fontSize: 13 }}>
            {dir.data.directoryAddress}
          </span>
          <CopyButton value={dir.data.directoryAddress} />
          <span className="sep" />
          <span style={{ fontWeight: 600 }}>List</span>
          <span className="mono dim">x402-service:&lt;URL&gt;</span>
          <span className="sep" />
          <span style={{ fontWeight: 600 }}>Rate</span>
          <span className="mono dim">x402-rate:&lt;1-5&gt;:&lt;URL&gt;</span>
        </div>
      )}
    </div>
  );
}
