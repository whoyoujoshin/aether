import { useParams, Link } from "react-router-dom";
import { api, PowProof } from "../api";
import { useApi } from "../hooks";
import { AddressLink, BlockLink, CopyButton, TxHash } from "../components/Hash";
import { TxStatusText } from "../components/StatusBadge";
import { ErrorBanner, KV, MiniBar, MsgTag } from "../components/ui";
import { aeth, coins, int, timeAgo, uaethOf, utc } from "../format";

/** A hash with its leading zeros lit, the part proof-of-work is about. */
function LitHash({ hex, target = false }: { hex: string; target?: boolean }) {
  const lead = /^0*/.exec(hex)?.[0].length ?? 0;
  return (
    <div className={`pow-hash${target ? " target" : ""}`}>
      <span className="lead">{hex.slice(0, lead + (target ? 2 : 0))}</span>
      {hex.slice(lead + (target ? 2 : 0))}
    </div>
  );
}

function Proof({ p, first }: { p: PowProof; first: boolean }) {
  const accepted = p.code === 0;
  return (
    <div className={first ? "" : "pow-divider"}>
      <div style={{ display: "flex", alignItems: "center", gap: 10, marginTop: first ? 16 : 0, fontSize: 13, flexWrap: "wrap" }}>
        <span className="muted">Miner</span>
        <AddressLink address={p.miner} />
      </div>
      {p.kind === "native" && p.hash && p.target ? (
        <>
          <div className="mono muted" style={{ fontSize: 12, fontWeight: 500, marginTop: 14 }}>
            scrypt hash of the header for block {p.claimedHeight !== undefined && <BlockLink height={p.claimedHeight} />}
          </div>
          <LitHash hex={p.hash} />
          <div className="mono muted" style={{ fontSize: 12, fontWeight: 500, marginTop: 12 }}>
            must be below target
          </div>
          <LitHash hex={p.target} target />
          <div className="metric-grid pow-divider" style={{ gridTemplateColumns: "1fr 1fr" }}>
            <div>
              <div className="muted" style={{ fontSize: 12 }}>Difficulty</div>
              <div className="v">{int(p.difficulty ?? 0)}</div>
            </div>
            <div>
              <div className="muted" style={{ fontSize: 12 }}>Nonce</div>
              <div className="v">{int(p.nonce ?? 0)}</div>
            </div>
          </div>
        </>
      ) : (
        <div className="muted" style={{ fontSize: 13, marginTop: 12, lineHeight: 1.5 }}>
          Merged mining (AuxPoW): the work is proven against a parent chain's block header. <TxHash hash={p.txHash} copy={false} /> has the details.
        </div>
      )}
      <div style={{ display: "flex", alignItems: "center", gap: 8, marginTop: 16, fontSize: 13, flexWrap: "wrap" }} className={accepted ? "green" : "danger"}>
        <span className="dot" />
        {accepted
          ? p.margin
            ? `Valid — ${p.margin}× below target`
            : "Accepted by the chain"
          : p.valid
            ? `Hash meets the target, but the chain rejected the submission (code ${p.code})`
            : `Rejected by the chain (code ${p.code})`}
        {accepted && p.reward && <span className="muted">· minted {coins(p.reward)}</span>}
      </div>
      {!accepted && (
        <div style={{ fontSize: 12, marginTop: 6 }}>
          <TxHash hash={p.txHash} copy={false} /> <span className="muted">has the reason</span>
        </div>
      )}
    </div>
  );
}

const TX_COLS = "200px 150px minmax(0,1fr) minmax(0,1fr) 140px 70px";

export default function Block() {
  const { height: heightParam } = useParams();
  const height = Number(heightParam);
  const block = useApi(() => api.block(height), [height]);
  const b = block.data;

  const confirmations = b && b.latestHeight ? b.latestHeight - b.height : 0;
  const epochLen = b?.epochLength ?? 0;
  const reward = (b?.pow ?? [])
    .filter((p) => p.code === 0 && uaethOf(p.reward))
    .reduce((a, p) => a + BigInt(uaethOf(p.reward)), 0n);
  const miners = [...new Set((b?.pow ?? []).filter((p) => p.code === 0).map((p) => p.miner))];
  const hasNext = b ? b.latestHeight > b.height : false;

  return (
    <div className="page">
      <div className="crumbs">
        <Link to="/blocks">Blocks</Link>
        <span>/</span>
        <span className="dim">{Number.isFinite(height) ? int(height) : heightParam}</span>
      </div>
      <div className="title-row">
        <h1 className="page-title">
          Block{" "}
          <span className="mono red" style={{ textShadow: "0 0 24px rgba(192,80,58,.5)" }}>
            {Number.isFinite(height) ? int(height) : heightParam}
          </span>
        </h1>
        <div style={{ display: "flex", gap: 6 }}>
          <Link to={`/blocks/${height - 1}`} className={`icon-btn${height <= 1 ? " off" : ""}`} aria-label="Previous block">
            ‹
          </Link>
          <Link to={`/blocks/${height + 1}`} className={`icon-btn${b && !hasNext ? " off" : ""}`} aria-label="Next block">
            ›
          </Link>
        </div>
        {b && (
          <span className="pill ok">
            <span className="dot" />
            Final · {int(confirmations)} confirmation{confirmations === 1 ? "" : "s"}
          </span>
        )}
      </div>
      {b && (
        <div className="hash-line">
          {b.hash}
          <CopyButton value={b.hash} />
        </div>
      )}

      <ErrorBanner error={block.error} />

      {block.loading && !b ? (
        <div className="loading">Loading…</div>
      ) : b ? (
        <>
          <div className="split gap-top-lg">
            <div className="card">
              <KV label="Timestamp">
                <span>
                  {utc(b.time)} <span className="muted">· {timeAgo(b.time)}</span>
                </span>
              </KV>
              <KV label="Epoch">
                {epochLen > 1 ? (
                  <>
                    <span className="mono">{int(Math.floor(b.height / epochLen))}</span>
                    <span className="muted">
                      block {int((b.height % epochLen) + 1)} of {int(epochLen)}
                    </span>
                    <MiniBar pct={(((b.height % epochLen) + 1) / epochLen) * 100} />
                  </>
                ) : (
                  <span className="mono">{int(b.height)}</span>
                )}
              </KV>
              <KV label="Proposed by">
                {b.proposer ? <AddressLink address={b.proposer} full /> : <span className="mono muted">{b.proposerAddress.toUpperCase()}</span>}
              </KV>
              {miners.length > 0 && (
                <KV label={miners.length === 1 ? "Mined by" : "Miners"}>
                  {miners.map((m) => (
                    <AddressLink key={m} address={m} full={miners.length === 1} />
                  ))}
                </KV>
              )}
              {reward > 0n && (
                <KV label="Block reward">
                  <span className="mono">
                    {aeth(reward.toString())} <span className="muted">· {int(reward.toString())} uaeth</span>
                  </span>
                </KV>
              )}
              <KV label="Transactions">
                <span>
                  {b.txs.length > 0 ? (
                    <a href="#block-txs">
                      {b.txs.length} transaction{b.txs.length === 1 ? "" : "s"}
                    </a>
                  ) : (
                    <span className="muted">none</span>
                  )}
                </span>
              </KV>
              <KV label="Gas used">
                <span className="mono" style={{ fontSize: 13 }}>
                  {int(b.gasUsed)} / {b.maxGas > 0 ? int(b.maxGas) : `${int(b.gasWanted)} wanted`}
                </span>
                {(b.maxGas > 0 || b.gasWanted > 0) && <MiniBar bone pct={(b.gasUsed / (b.maxGas > 0 ? b.maxGas : b.gasWanted)) * 100} />}
                <span className="mono muted" style={{ fontSize: 13 }}>
                  {b.maxGas > 0
                    ? `${((b.gasUsed / b.maxGas) * 100).toFixed(1)}%`
                    : b.gasWanted > 0
                      ? `${Math.round((b.gasUsed / b.gasWanted) * 100)}% · no block limit`
                      : "no block limit"}
                </span>
              </KV>
              <KV label="Size">
                <span className="mono" style={{ fontSize: 13 }}>
                  {int(b.sizeBytes)} bytes
                </span>
              </KV>
              <KV label="Parent hash">
                {b.height > 1 ? (
                  <Link to={`/blocks/${b.height - 1}`} className="mono break" style={{ fontSize: 13 }}>
                    {b.parentHash}
                  </Link>
                ) : (
                  <span className="mono muted">genesis</span>
                )}
              </KV>
              <KV label="App hash">
                <span className="mono dim break" style={{ fontSize: 13 }}>
                  {b.appHash || "—"}
                </span>
              </KV>
            </div>

            <div className="stack">
              <div className="card-glow">
                <div className="eyebrow red">Proof of work</div>
                {b.pow.length > 0 ? (
                  b.pow.map((p, i) => <Proof key={p.txHash + i} p={p} first={i === 0} />)
                ) : (
                  <div className="muted" style={{ fontSize: 13, marginTop: 14, lineHeight: 1.55 }}>
                    No work was submitted in this block. On Aether, validators sign the blocks, and miners send their proof-of-work in as{" "}
                    <MsgTag type="MsgSubmitPoW" /> transactions, each earning the block reward.
                  </div>
                )}
              </div>

              <div className="card card-pad">
                <div className="eyebrow">Neighbours</div>
                <div className="neighbours">
                  {b.height > 1 ? (
                    <Link className="n" to={`/blocks/${b.height - 1}`}>
                      {int(b.height - 1)}
                    </Link>
                  ) : (
                    <span className="n off">—</span>
                  )}
                  <div className="link hot" />
                  <span className="n cur">{int(b.height)}</span>
                  <div className="link" />
                  {hasNext ? (
                    <Link className="n" to={`/blocks/${b.height + 1}`}>
                      {int(b.height + 1)}
                    </Link>
                  ) : (
                    <span className="n off">{int(b.height + 1)}</span>
                  )}
                </div>
              </div>
            </div>
          </div>

          <div className="card gap-top" id="block-txs">
            <div className="card-head">
              <span className="card-title">Transactions in this block</span>
              <span className="mono muted" style={{ fontSize: 12, fontWeight: 500 }}>
                {b.txs.length}
              </span>
            </div>
            {b.txs.length > 0 ? (
              <div className="scroll-x">
                <div className="rows">
                  <div className="row th" style={{ gridTemplateColumns: TX_COLS, padding: "11px 22px" }}>
                    <span>Hash</span>
                    <span>Type</span>
                    <span>From</span>
                    <span>To</span>
                    <span className="right">Amount</span>
                    <span className="right">Status</span>
                  </div>
                  {b.txs.map((t) => (
                    <div key={t.hash} className="row hover" style={{ gridTemplateColumns: TX_COLS, padding: "12px 22px", fontSize: 13 }}>
                      <TxHash hash={t.hash} copy={false} />
                      <div>
                        <MsgTag type={t.msgType} />
                        {t.msgCount > 1 && <span className="muted mono" style={{ fontSize: 11, marginLeft: 6 }}>+{t.msgCount - 1}</span>}
                      </div>
                      <span style={{ fontSize: 12 }}>
                        <AddressLink address={t.from} plain copy={false} />
                      </span>
                      <span style={{ fontSize: 12 }}>
                        <AddressLink address={t.to} plain copy={false} />
                      </span>
                      <span className="right mono">{t.amount ? coins(t.amount) : "—"}</span>
                      <span className="right">
                        <TxStatusText code={t.code} />
                      </span>
                    </div>
                  ))}
                </div>
              </div>
            ) : (
              <div className="empty">No transactions in this block.</div>
            )}
          </div>
        </>
      ) : null}
    </div>
  );
}
