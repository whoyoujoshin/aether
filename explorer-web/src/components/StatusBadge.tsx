/** A success/failure pill for a transaction's result code (0 = success, matching Cosmos SDK convention). */
export function TxStatusBadge({ code }: { code: number }) {
  if (code === 0) {
    return (
      <span className="pill ok">
        <span className="dot glow" />
        Success
      </span>
    );
  }
  return (
    <span className="pill bad">
      <span className="dot" />
      Failed ({code})
    </span>
  );
}

/** The compact OK / FAILED text used in dense transaction lists. */
export function TxStatusText({ code }: { code: number }) {
  return code === 0 ? (
    <span className="status-text green">OK</span>
  ) : (
    <span className="status-text danger" title={`code ${code}`}>
      FAILED
    </span>
  );
}

const proposalColors: Record<string, [string, string]> = {
  VOTING_PERIOD: ["var(--red)", "var(--red-14)"],
  DEPOSIT_PERIOD: ["var(--text-2)", "var(--cell)"],
  PASSED: ["var(--green)", "rgba(22,199,132,.12)"],
};

/** A pill for a governance proposal's status enum string, e.g. "PROPOSAL_STATUS_PASSED". */
export function ProposalStatusBadge({ status }: { status: string }) {
  const short = status.replace("PROPOSAL_STATUS_", "");
  const [color, bg] = proposalColors[short] ?? ["var(--muted)", "var(--cell)"];
  const label = short === "VOTING_PERIOD" ? "VOTING" : short === "DEPOSIT_PERIOD" ? "DEPOSIT" : short.replace(/_/g, " ");
  return (
    <span className="tag status-text" style={{ color, background: bg }}>
      {label}
    </span>
  );
}
