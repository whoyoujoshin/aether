import { useState } from "react";
import { api, Proposal, Tally } from "../api";
import { useApi } from "../hooks";
import { AddressLink, CopyButton } from "../components/Hash";
import { ProposalStatusBadge } from "../components/StatusBadge";
import { ErrorBanner, HeadStat, StackBar, pct } from "../components/ui";
import { aeth, duration, utc } from "../format";

function kind(p: Proposal): string {
  return p.proposalType === "PROPOSAL_TYPE_PARAM_CHANGE" ? "ParamChange" : p.proposalType === "PROPOSAL_TYPE_TREASURY_SPEND" ? "TreasurySpend" : p.proposalType.replace("PROPOSAL_TYPE_", "");
}

function title(p: Proposal): string {
  if (p.proposalType === "PROPOSAL_TYPE_TREASURY_SPEND") return `Spend ${aeth(p.amount || "0")} from the treasury`;
  if (p.proposalType === "PROPOSAL_TYPE_PARAM_CHANGE") return "Change module parameters";
  return `Proposal #${p.id}`;
}

function shortDate(unix: number): string {
  if (!unix) return "—";
  return new Date(unix * 1000).toLocaleDateString("en-US", { month: "short", day: "numeric", year: "numeric", timeZone: "UTC" });
}

function tallySegments(t: Tally) {
  return [
    { label: "Yes", value: Number(t.yesPower) || 0, color: "#16c784" },
    { label: "No", value: Number(t.noPower) || 0, color: "#c0503a" },
    { label: "Abstain", value: Number(t.abstainPower) || 0, color: "#9a9186" },
    { label: "No with veto", value: Number(t.vetoPower) || 0, color: "#6b645b" },
  ];
}

const status = (p: Proposal) => p.status.replace("PROPOSAL_STATUS_", "");

function LiveVote({ p, activeValidators }: { p: Proposal; activeValidators: number }) {
  const detail = useApi(() => api.proposalTally(p.id), [p.id], 15000);
  const t = detail.data?.tally;
  const segs = t ? tallySegments(t) : [];
  const total = segs.reduce((a, s) => a + s.value, 0);
  const left = p.votingEndTime - Date.now() / 1000;
  const quorum = t ? t.validVoterCount >= t.quorumThreshold : false;
  const voteCmd = `aetherd tx governance vote ${p.id} yes --from <validator-key>`;

  return (
    <div className="live-vote">
      <div style={{ minWidth: 0 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 10, flexWrap: "wrap" }}>
          <span className="mono muted" style={{ fontSize: 13, fontWeight: 500 }}>
            #{p.id}
          </span>
          <span className="pill live" style={{ padding: "4px 10px" }}>
            <span className="dot glow pulse" />
            Voting · {left > 0 ? `${duration(left)} left` : "closing"}
          </span>
          <span className="mono muted" style={{ fontSize: 12, fontWeight: 500 }}>
            {kind(p)}
          </span>
        </div>
        <div style={{ fontSize: 26, fontWeight: 600, letterSpacing: "-0.01em", marginTop: 14 }}>{title(p)}</div>
        <div className="dim" style={{ fontSize: 15, lineHeight: 1.55, marginTop: 10, maxWidth: 560 }}>
          {p.proposalType === "PROPOSAL_TYPE_TREASURY_SPEND" ? (
            <>
              If it passes, {aeth(p.amount || "0")} goes from the treasury to <AddressLink address={p.recipient} copy={false} />.
            </>
          ) : (
            "If it passes, the chain executes the proposal's parameter change."
          )}{" "}
          Only active validators' votes count, weighted by their tenure.
        </div>
        <div style={{ display: "flex", gap: "8px 24px", marginTop: 18, fontSize: 13, flexWrap: "wrap" }} className="muted">
          <span>
            Deposit <span className="mono dim">{aeth(p.totalDeposit || "0")}</span>
          </span>
          <span>
            Ends <span className="dim">{utc(p.votingEndTime)}</span>
          </span>
        </div>
      </div>
      <div>
        <ErrorBanner error={detail.error} />
        <div style={{ display: "flex", justifyContent: "space-between", fontSize: 13 }} className="muted">
          <span>
            Turnout{" "}
            <span className="mono" style={{ color: "var(--bone)" }}>
              {t ? `${t.validVoterCount}${activeValidators ? ` / ${activeValidators}` : ""}` : "—"}
            </span>{" "}
            validators
          </span>
          <span>{t ? (quorum ? "quorum reached" : `quorum needs ${t.quorumThreshold}`) : ""}</span>
        </div>
        <div style={{ margin: "12px 0 18px" }}>
          <StackBar segments={segs} height={14} />
        </div>
        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "12px 20px" }}>
          {segs.map((s) => (
            <div key={s.label} style={{ display: "flex", alignItems: "center", gap: 10, fontSize: 14 }}>
              <span className="swatch" style={{ background: s.color }} />
              {s.label}
              <span className="mono" style={{ marginLeft: "auto" }}>
                {pct(s.value, total)}
              </span>
            </div>
          ))}
        </div>
        <div className="well code" style={{ marginTop: 22 }}>
          <pre style={{ padding: "12px 40px 12px 14px", fontSize: 12 }}>{voteCmd}</pre>
          <CopyButton value={voteCmd} />
        </div>
      </div>
    </div>
  );
}

function ProposalRow({ p }: { p: Proposal }) {
  const [open, setOpen] = useState(false);
  const detail = useApi(() => api.proposalTally(p.id), [p.id]);
  const t = detail.data?.tally;
  const segs = t ? tallySegments(t) : [];
  const total = segs.reduce((a, s) => a + s.value, 0);
  const votes = detail.data?.votes ?? [];
  const when = status(p) === "VOTING_PERIOD" ? `Ends ${shortDate(p.votingEndTime)}` : status(p) === "DEPOSIT_PERIOD" ? `Deposit until ${shortDate(p.depositEndTime)}` : shortDate(p.votingEndTime || p.submitTime);

  return (
    <>
      <div
        className="row hover"
        style={{ gridTemplateColumns: "56px minmax(0,1fr) 160px 170px 110px", padding: "15px 22px", cursor: "pointer" }}
        onClick={() => setOpen(!open)}
        role="button"
        aria-expanded={open}
      >
        <span className="mono muted" style={{ fontSize: 13, fontWeight: 500 }}>
          #{p.id}
        </span>
        <div style={{ minWidth: 0 }}>
          <div style={{ fontSize: 15, fontWeight: 500 }}>{title(p)}</div>
          <div className="mono muted" style={{ fontSize: 12, marginTop: 3 }}>
            {kind(p)}
          </div>
        </div>
        <div className="vote-bar" title={t ? `yes ${pct(segs[0].value, total)} · no ${pct(segs[1].value + segs[3].value, total)}` : undefined}>
          {total > 0 && (
            <>
              <div style={{ width: `${(segs[0].value / total) * 100}%`, background: "var(--green)" }} />
              <div style={{ width: `${((segs[1].value + segs[3].value) / total) * 100}%`, background: "var(--red)" }} />
            </>
          )}
        </div>
        <span className="muted" style={{ fontSize: 13 }}>
          {when}
        </span>
        <span className="right">
          <ProposalStatusBadge status={p.status} />
        </span>
      </div>
      {open && (
        <div style={{ padding: "4px 22px 18px 78px", borderBottom: "1px solid var(--rule)", fontSize: 13 }}>
          {t && (
            <div className="muted" style={{ display: "flex", gap: "6px 24px", flexWrap: "wrap", marginBottom: 10 }}>
              <span>
                valid voters <span className="mono dim">{t.validVoterCount}</span>
              </span>
              <span>
                quorum <span className="mono dim">{t.quorumThreshold}</span>
              </span>
              {segs.map((s) => (
                <span key={s.label}>
                  {s.label.toLowerCase()} <span className="mono dim">{String(s.value)}</span>
                </span>
              ))}
            </div>
          )}
          {p.recipient && (
            <div className="muted" style={{ marginBottom: 10 }}>
              Recipient <AddressLink address={p.recipient} />
            </div>
          )}
          {votes.length > 0 ? (
            votes.map((v) => (
              <div key={v.voter} style={{ display: "flex", gap: 16, padding: "6px 0", alignItems: "center" }}>
                <AddressLink address={v.voter} plain />
                <span className="mono">{v.option.replace("VOTE_OPTION_", "").replace(/_/g, " ")}</span>
                <span className="mono muted" style={{ marginLeft: "auto" }}>
                  weight {v.weight}
                </span>
              </div>
            ))
          ) : (
            <div className="faint">{detail.loading ? "Loading votes…" : "No votes cast yet."}</div>
          )}
        </div>
      )}
    </>
  );
}

const FILTERS = ["All", "Voting", "Passed", "Rejected"] as const;
const matches: Record<(typeof FILTERS)[number], (s: string) => boolean> = {
  All: () => true,
  Voting: (s) => s === "VOTING_PERIOD" || s === "DEPOSIT_PERIOD",
  Passed: (s) => s === "PASSED",
  Rejected: (s) => s === "REJECTED" || s === "FAILED_QUORUM" || s === "EXPIRED" || s === "EXECUTION_FAILED",
};

export default function Governance() {
  const proposals = useApi(api.proposals, [], 10000);
  const params = useApi(api.governanceParams, [], 60000);
  const [filter, setFilter] = useState<(typeof FILTERS)[number]>("All");

  const list = [...(proposals.data ?? [])].sort((a, b) => b.id - a.id);
  const live = list.find((p) => status(p) === "VOTING_PERIOD");
  const g = params.data;

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1 className="page-title">Governance</h1>
          <div className="page-sub">On-chain proposals, decided by Aether's active validators.</div>
        </div>
        <div className="head-stats">
          <HeadStat label="Quorum">60%</HeadStat>
          <HeadStat label="Pass threshold">⅔</HeadStat>
          <HeadStat label="Voting period">{g ? (g.votingPeriod ? duration(g.votingPeriod) : "—") : "—"}</HeadStat>
          <HeadStat label="Min deposit">{g ? aeth(String(g.minDeposit)) : "—"}</HeadStat>
        </div>
      </div>

      <ErrorBanner error={proposals.error} />

      {live && <LiveVote p={live} activeValidators={g?.activeValidators ?? 0} />}

      <div className="card gap-top">
        <div className="card-head">
          <span className="card-title">All proposals</span>
          <div className="chip-row" style={{ marginLeft: "auto" }}>
            {FILTERS.map((f) => (
              <button key={f} className={`chip${f === filter ? " on" : ""}`} onClick={() => setFilter(f)}>
                {f}
              </button>
            ))}
          </div>
        </div>
        {proposals.loading && !proposals.data ? (
          <div className="loading">Loading…</div>
        ) : list.length > 0 ? (
          <div className="scroll-x">
            <div className="rows">
              {list
                .filter((p) => matches[filter](status(p)))
                .map((p) => (
                  <ProposalRow key={p.id} p={p} />
                ))}
              {list.filter((p) => matches[filter](status(p))).length === 0 && <div className="empty">No {filter.toLowerCase()} proposals.</div>}
            </div>
          </div>
        ) : (
          <div className="empty">No proposals yet.</div>
        )}
        <div className="muted" style={{ padding: "12px 22px", borderTop: "1px solid var(--rule)", fontSize: 12 }}>
          Passes with ⅔ of non-abstain voting power; rejected if No with veto reaches ⅓. Quorum is 60% of validators. Click a proposal for its votes.
        </div>
      </div>
    </div>
  );
}
