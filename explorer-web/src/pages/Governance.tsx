import { Fragment, useState } from "react";
import { api, ProposalTally } from "../api";
import { useApi } from "../hooks";
import { AddressLink } from "../components/Hash";
import { ProposalStatusBadge } from "../components/StatusBadge";

function formatTime(unix: number): string {
  if (!unix) return "—";
  return new Date(unix * 1000).toLocaleString();
}

function ProposalDetail({ id }: { id: number }) {
  const detail = useApi(() => api.proposalTally(id), [id]);

  if (detail.loading && !detail.data) return <div className="loading">Loading tally…</div>;
  if (detail.error) return <div className="error-banner" style={{ margin: 16 }}>{detail.error}</div>;
  if (!detail.data) return null;

  const { tally, votes }: ProposalTally = detail.data;

  return (
    <div style={{ padding: "16px 18px", borderTop: "1px solid var(--border-subtle)" }}>
      <div className="section-title" style={{ marginTop: 0 }}>
        Tally
      </div>
      <table className="kv-table">
        <tbody>
          <tr>
            <td>Valid Voter Count</td>
            <td className="mono">{tally.validVoterCount}</td>
          </tr>
          <tr>
            <td>Yes Power</td>
            <td className="mono">{tally.yesPower}</td>
          </tr>
          <tr>
            <td>No Power</td>
            <td className="mono">{tally.noPower}</td>
          </tr>
          <tr>
            <td>Abstain Power</td>
            <td className="mono">{tally.abstainPower}</td>
          </tr>
          <tr>
            <td>Veto Power</td>
            <td className="mono">{tally.vetoPower}</td>
          </tr>
          <tr>
            <td>Quorum Threshold</td>
            <td className="mono">{tally.quorumThreshold}</td>
          </tr>
        </tbody>
      </table>

      <div className="section-title">Votes ({votes.length})</div>
      {votes.length > 0 ? (
        <table>
          <thead>
            <tr>
              <th>Voter</th>
              <th>Option</th>
              <th>Weight</th>
            </tr>
          </thead>
          <tbody>
            {votes.map((v) => (
              <tr key={v.voter}>
                <td>
                  <AddressLink address={v.voter} />
                </td>
                <td>{v.option.replace("VOTE_OPTION_", "")}</td>
                <td className="mono">{v.weight}</td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : (
        <div className="empty-state">No votes cast yet.</div>
      )}
    </div>
  );
}

export default function Governance() {
  const proposals = useApi(api.proposals, [], 10000);
  const [expanded, setExpanded] = useState<number | null>(null);

  return (
    <div className="page">
      <h1 className="page-title">Governance</h1>

      <div className="panel">
        <div className="panel-header">
          <div className="panel-title">Proposals</div>
          <div className="panel-meta">click a row for tally &amp; votes</div>
        </div>
        {proposals.error && <div className="error-banner" style={{ margin: 16 }}>{proposals.error}</div>}
        {proposals.loading && !proposals.data ? (
          <div className="loading">Loading…</div>
        ) : proposals.data && proposals.data.length > 0 ? (
          <table>
            <thead>
              <tr>
                <th>ID</th>
                <th>Recipient</th>
                <th>Amount</th>
                <th>Type</th>
                <th>Status</th>
                <th>Submitted</th>
              </tr>
            </thead>
            <tbody>
              {proposals.data.map((p) => (
                <Fragment key={p.id}>
                  <tr
                    onClick={() => setExpanded(expanded === p.id ? null : p.id)}
                    style={{ cursor: "pointer" }}
                  >
                    <td className="mono">#{p.id}</td>
                    <td>
                      <AddressLink address={p.recipient} />
                    </td>
                    <td className="mono">{p.amount}</td>
                    <td>{p.proposalType.replace("PROPOSAL_TYPE_", "")}</td>
                    <td>
                      <ProposalStatusBadge status={p.status} />
                    </td>
                    <td className="mono">{formatTime(p.submitTime)}</td>
                  </tr>
                  {expanded === p.id && (
                    <tr>
                      <td colSpan={6} style={{ padding: 0 }}>
                        <ProposalDetail id={p.id} />
                      </td>
                    </tr>
                  )}
                </Fragment>
              ))}
            </tbody>
          </table>
        ) : (
          <div className="empty-state">No proposals found.</div>
        )}
      </div>
    </div>
  );
}
