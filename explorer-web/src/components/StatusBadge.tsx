/** A success/failure pill for a transaction's result code (0 = success, matching Cosmos SDK convention). */
export function TxStatusBadge({ code }: { code: number }) {
  if (code === 0) {
    return <span className="badge badge-success">Success</span>;
  }
  return <span className="badge badge-danger">Failed ({code})</span>;
}

/** A pill for a governance proposal's status enum string, e.g. "PROPOSAL_STATUS_PASSED". */
export function ProposalStatusBadge({ status }: { status: string }) {
  const short = status.replace("PROPOSAL_STATUS_", "");
  const kind =
    short === "PASSED"
      ? "badge-success"
      : short === "REJECTED" || short === "FAILED_QUORUM" || short === "EXECUTION_FAILED"
        ? "badge-danger"
        : "badge-neutral";
  return <span className={`badge ${kind}`}>{short.replace(/_/g, " ")}</span>;
}
