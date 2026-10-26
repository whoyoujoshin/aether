import { FormEvent, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, AAAuthenticator, AATransaction } from "../api";
import { useApi } from "../hooks";
import { AddressLink, TxHash } from "../components/Hash";
import { ErrorBanner, HeadStat, MiniBar, MsgTag, NoticeBanner } from "../components/ui";
import { aeth, timeAgo, utc } from "../format";

function kindLabel(kind: string): string {
  if (kind === "session_key") return "Session key";
  if (kind === "guardian_threshold") return "Guardian threshold";
  return "Authenticator";
}

function spendPct(a: AAAuthenticator): number {
  try {
    const limit = BigInt(a.spendLimitUaeth || "0");
    const spent = BigInt(a.spentUaeth || "0");
    if (limit <= 0n) return 0;
    return Number((spent * 10000n) / limit) / 100;
  } catch {
    return 0;
  }
}

function AuthenticatorCard({ a }: { a: AAAuthenticator }) {
  const pct = spendPct(a);
  return (
    <div className={`svc-card${a.expired ? " attention-expired" : ""}`}>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", gap: 12 }}>
        <span style={{ fontWeight: 600 }}>
          {kindLabel(a.kind)} <span className="mono muted">#{a.id}</span>
        </span>
        {a.expired ? (
          <span className="tag" style={{ color: "var(--danger)", background: "var(--danger-12)" }}>EXPIRED</span>
        ) : a.kind === "session_key" ? (
          <span className="tag" style={{ color: "var(--green)", background: "var(--green-10)" }}>ACTIVE</span>
        ) : (
          <span className="tag" style={{ color: "var(--bone)", background: "var(--cell)" }}>
            {a.threshold} of {a.guardians}
          </span>
        )}
      </div>

      {a.kind === "session_key" && (
        <>
          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "10px 20px", fontSize: 13 }}>
            <div>
              <div className="muted" style={{ fontSize: 10, letterSpacing: "0.06em" }}>EXPIRES</div>
              <div className="mono" style={{ fontWeight: 600, marginTop: 2 }}>{a.expiresAt ? utc(a.expiresAt) : "—"}</div>
            </div>
            <div>
              <div className="muted" style={{ fontSize: 10, letterSpacing: "0.06em" }}>PUBLIC KEY</div>
              <div className="mono" style={{ fontWeight: 600, marginTop: 2 }} title={a.pubkey}>
                {a.pubkey || "—"}
                {a.pubkeyBytes ? <span className="muted"> · {a.pubkeyBytes} bytes</span> : null}
              </div>
            </div>
          </div>
          <div>
            <div className="muted" style={{ fontSize: 10, letterSpacing: "0.06em", marginBottom: 6 }}>ALLOWED MESSAGES</div>
            <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
              {(a.allowedMsgTypes ?? []).length === 0 && <span className="muted">none</span>}
              {(a.allowedMsgTypes ?? []).map((t) => (
                <MsgTag key={t} type={t.split(".").pop() || t} />
              ))}
            </div>
          </div>
          <div>
            <div className="muted" style={{ fontSize: 10, letterSpacing: "0.06em", marginBottom: 6 }}>LIFETIME SPEND</div>
            <div className="mono" style={{ fontSize: 13, marginBottom: 6 }}>
              {aeth(a.spentUaeth || "0")} of {aeth(a.spendLimitUaeth || "0")}
            </div>
            <MiniBar pct={pct} />
          </div>
        </>
      )}

      {a.kind === "guardian_threshold" && (
        <div style={{ fontSize: 13 }}>
          <div className="muted" style={{ fontSize: 10, letterSpacing: "0.06em", marginBottom: 6 }}>
            NEXT SEQUENCE {a.nextSequence ?? 0}
          </div>
          <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
            {(a.guardianKeys ?? []).map((k, i) => (
              <span key={`${k}-${i}`} className="mono muted">{k}</span>
            ))}
          </div>
          <div className="muted" style={{ marginTop: 8, fontSize: 12 }}>
            Any {a.threshold} of these {a.guardians} ML-DSA-44 keys can authorize an action. Guardians do not need a funded account.
          </div>
        </div>
      )}
    </div>
  );
}

function TxRow({ t }: { t: AATransaction }) {
  return (
    <div className="row" style={{ gridTemplateColumns: "minmax(0,1.1fr) 180px minmax(0,1fr) 90px" }}>
      <span style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0 }}>
        <TxHash hash={t.hash} />
        {t.code !== 0 && <span className="tag" style={{ color: "var(--danger)", background: "var(--danger-12)" }}>FAILED</span>}
      </span>
      <MsgTag type={t.action} />
      <span className="mono muted ellipsis" title={t.detail}>{t.detail || "—"}</span>
      <span className="muted right">{t.time ? timeAgo(t.time) : `block ${t.height}`}</span>
    </div>
  );
}

function Lookup({ initial = "" }: { initial?: string }) {
  const [q, setQ] = useState(initial);
  const navigate = useNavigate();
  function submit(e: FormEvent) {
    e.preventDefault();
    const addr = q.trim();
    if (!addr) return;
    navigate(`/accounts/${encodeURIComponent(addr)}`);
  }
  return (
    <form onSubmit={submit} className="aa-lookup" role="search">
      <input
        value={q}
        onChange={(e) => setQ(e.target.value)}
        placeholder="Look up aether1…"
        aria-label="Account address"
        spellCheck={false}
      />
      <button type="submit" className="btn-outline">Open</button>
    </form>
  );
}

export default function Accounts() {
  const { address } = useParams();
  const decoded = address ? decodeURIComponent(address) : "";
  const page = useApi(() => api.aa(decoded || undefined), [decoded], 15000);
  const data = page.data;
  const accounts = data?.accounts ?? [];
  const txs = data?.transactions ?? [];
  const one = decoded ? accounts.find((a) => a.address === decoded) ?? accounts[0] : null;

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <div className="crumbs">
            {decoded && <Link to="/accounts">Account abstraction</Link>}
            {decoded && <span> / </span>}
          </div>
          <h1 className="page-title">{decoded ? "Account" : "Account abstraction"}</h1>
          <div className="page-sub">
            {data?.summary || "Session keys and guardian thresholds from x/accountauth."}
          </div>
        </div>
        <div className="head-stats">
          <HeadStat label="Module">{data ? data.module : "—"}</HeadStat>
          <HeadStat label={decoded ? "Authenticators" : "Accounts"}>{data ? (decoded ? one?.authenticators.length ?? "—" : accounts.length) : "—"}</HeadStat>
          <HeadStat label="Messages">{data ? txs.length : "—"}</HeadStat>
        </div>
      </div>

      <Lookup initial={decoded} />
      <ErrorBanner error={page.error} />
      {data?.note && <NoticeBanner>{data.note}</NoticeBanner>}

      {page.loading && !data ? (
        <div className="loading">Loading…</div>
      ) : data && !data.supported ? (
        <div className="card card-pad gap-top-lg">
          <div className="card-title">No account-abstraction module on this node</div>
          <p className="dim" style={{ marginBottom: 0, lineHeight: 1.55 }}>
            The query service for x/accountauth is not available. This page is not showing a made-up smart-account module.
          </p>
        </div>
      ) : decoded && one ? (
        <>
          <div className="card card-pad gap-top-lg">
            <div className="muted" style={{ fontSize: 12, letterSpacing: "0.06em" }}>ACCOUNT</div>
            <div style={{ marginTop: 8 }}><AddressLink address={one.address} /></div>
            {one.lastAction && (
              <div className="muted" style={{ marginTop: 8, fontSize: 13 }}>
                Latest indexed message: {one.lastAction}
                {one.lastHeight ? ` at block ${one.lastHeight}` : ""}
              </div>
            )}
          </div>
          <div className="gap-top-lg">
            <div className="card-title" style={{ marginBottom: 12 }}>Configuration</div>
            {one.authenticators.length === 0 ? (
              <div className="card"><div className="empty">This account has no authenticators registered now.</div></div>
            ) : (
              <div className="svc-grid">
                {one.authenticators.map((a) => (
                  <AuthenticatorCard key={a.id} a={a} />
                ))}
              </div>
            )}
          </div>
          <div className="card gap-top-lg">
            <div className="card-head">
              <span className="card-title">Recent transactions</span>
              <span className="card-meta">register, revoke, exec</span>
            </div>
            {txs.length === 0 ? <div className="empty">No indexed account-abstraction messages for this account.</div> : txs.map((t) => <TxRow key={`${t.hash}-${t.action}`} t={t} />)}
          </div>
        </>
      ) : (
        <>
          <div className="card gap-top-lg">
            <div className="card-head">
              <span className="card-title">Accounts</span>
              <span className="card-meta">discovered from indexed messages, configuration read live</span>
            </div>
            {accounts.length === 0 ? (
              <div className="empty">
                No account has registered an authenticator in the indexed transactions. An account does so with MsgRegisterAuthenticator, then MsgExecAuthenticated acts for it.
              </div>
            ) : (
              accounts.map((a) => (
                <div key={a.address} className="row" style={{ gridTemplateColumns: "minmax(0,1.4fr) 120px minmax(0,1fr) 120px" }}>
                  <AddressLink address={a.address} />
                  <span className="mono">{a.authenticators.length} live</span>
                  <span className="mono muted ellipsis">{a.lastAction || "—"}</span>
                  <Link to={`/accounts/${encodeURIComponent(a.address)}`} className="right">Configuration</Link>
                </div>
              ))
            )}
          </div>
          <div className="card gap-top-lg">
            <div className="card-head">
              <span className="card-title">Recent transactions</span>
              <span className="card-meta">{(data?.messages ?? []).map((m) => m.split(".").pop()).filter(Boolean).join(" · ")}</span>
            </div>
            {txs.length === 0 ? <div className="empty">No account-abstraction transactions indexed.</div> : txs.map((t) => <TxRow key={`${t.hash}-${t.action}-${t.account}`} t={t} />)}
          </div>
        </>
      )}
    </div>
  );
}
