import { useState, type ReactNode } from "react";
import { Link, useParams } from "react-router-dom";
import { api, TransactionDetail, TxEvent, TxMessage } from "../api";
import { useApi } from "../hooks";
import { AddressLink, BlockLink, CopyButton } from "../components/Hash";
import { TxStatusBadge } from "../components/StatusBadge";
import { ErrorBanner, KV, MiniBar, MsgTag, Tabs, TypeUrl } from "../components/ui";
import { aeth, aethNumber, amountIn, coins, int, shortHash, splitTypeUrl, timeAgo, utc } from "../format";

type Coin = { denom: string; amount: string };

function isCoinList(v: unknown): v is Coin[] {
  return Array.isArray(v) && v.length > 0 && v.every((c) => c && typeof c === "object" && "denom" in c && "amount" in c);
}

function coinListText(v: Coin[]): string {
  return v.map((c) => (c.denom === "uaeth" ? aeth(c.amount) : amountIn(c.amount, c.denom))).join(" + ");
}

function uaethIn(v: unknown): string {
  if (isCoinList(v)) return v.find((c) => c.denom === "uaeth")?.amount ?? "";
  return "";
}

const addrRe = /^aether1[0-9a-z]{20,}$/;

function label(key: string): string {
  const s = key.replace(/_/g, " ");
  return s.charAt(0).toUpperCase() + s.slice(1);
}

/** One field of a decoded message, drawn by what it holds. */
function Value({ k, v }: { k: string; v: unknown }): ReactNode {
  if (typeof v === "string") {
    if (addrRe.test(v)) return <AddressLink address={v} />;
    if ((k === "amount" || k === "deposit") && /^\d+$/.test(v)) return <span className="mono">{aeth(v)}</span>;
    if (k === "proposal_id" && /^\d+$/.test(v)) return <Link to="/governance" className="mono">#{v}</Link>;
    return <span className="mono dim">{v || "—"}</span>;
  }
  if (typeof v === "number" || typeof v === "boolean") return <span className="mono dim">{String(v)}</span>;
  if (isCoinList(v)) return <span className="mono">{coinListText(v)}</span>;
  if (Array.isArray(v) && v.every((x) => typeof x === "string")) {
    return (
      <span style={{ display: "flex", flexWrap: "wrap", gap: 8 }}>
        {v.length === 0 ? <span className="faint">none</span> : v.map((x) => (addrRe.test(x) ? <AddressLink key={x} address={x} /> : <span key={x} className="mono dim">{x}</span>))}
      </span>
    );
  }
  if (v === null || v === undefined) return <span className="faint">—</span>;
  return (
    <pre className="mono dim" style={{ margin: 0, fontSize: 12, whiteSpace: "pre-wrap", wordBreak: "break-all" }}>
      {JSON.stringify(v, null, 2)}
    </pre>
  );
}

/** A message box; MsgExec's inner messages nest inside it. */
function Message({ msg, index }: { msg: TxMessage; index: string }) {
  const inner = Array.isArray(msg.msgs) ? (msg.msgs as TxMessage[]) : [];
  const fields = Object.entries(msg).filter(([k]) => k !== "@type" && !(k === "msgs" && inner.length));
  return (
    <div className={`msg-box${index.includes(".") ? " inner" : ""}`}>
      <div className="msg-head">
        <span className="mono muted" style={{ fontSize: 12 }}>
          #{index}
        </span>
        <TypeUrl url={msg["@type"]} />
      </div>
      {fields.length > 0 && (
        <div className="msg-fields">
          {fields.map(([k, v]) => (
            <FieldRow key={k} k={k} v={v} />
          ))}
        </div>
      )}
      {inner.map((m, i) => (
        <Message key={i} msg={m} index={`${index}.${i}`} />
      ))}
    </div>
  );
}

function FieldRow({ k, v }: { k: string; v: unknown }) {
  return (
    <>
      <span className="k">{label(k)}</span>
      <span className="v">
        <Value k={k} v={v} />
      </span>
    </>
  );
}

function attr(events: TxEvent[], type: string, key: string): string {
  for (const e of events) if (e.type === type) for (const a of e.attributes) if (a.key === key) return a.value;
  return "";
}

/** Net uaeth change per address, from the chain's coin_spent / coin_received events. */
function balanceChanges(events: TxEvent[]): [string, bigint][] {
  const net = new Map<string, bigint>();
  for (const e of events) {
    const sign = e.type === "coin_spent" ? -1n : e.type === "coin_received" ? 1n : 0n;
    if (!sign) continue;
    const who = e.attributes.find((a) => a.key === (sign < 0n ? "spender" : "receiver"))?.value;
    const amt = e.attributes.find((a) => a.key === "amount")?.value ?? "";
    const m = /^(\d+)uaeth$/.exec(amt.split(",").find((c) => c.endsWith("uaeth")) ?? "");
    if (!who || !m) continue;
    net.set(who, (net.get(who) ?? 0n) + sign * BigInt(m[1]));
  }
  return [...net.entries()].filter(([, v]) => v !== 0n);
}

const A = ({ a }: { a: string }) => <AddressLink address={a} copy={false} />;
const Amt = ({ u }: { u: string }) => <span className="mono" style={{ fontWeight: 600 }}>{aeth(u)}</span>;

/** A one-sentence, plain-English account of what the transaction did, plus its headline amount. */
function summarize(tx: TransactionDetail): { text: ReactNode; amount: string } | null {
  const m = tx.messages[0];
  if (!m) return null;
  const [type] = splitTypeUrl(m["@type"]);
  const failed = tx.code !== 0;
  const tried = (s: ReactNode) => (failed ? <>tried to {s}, but it failed</> : s);
  const str = (k: string) => (typeof m[k] === "string" ? (m[k] as string) : "");

  switch (type) {
    case "MsgSend": {
      const u = uaethIn(m.amount);
      return {
        text: (
          <>
            <A a={str("from_address")} /> {failed ? "tried to send" : "sent"} {u ? <Amt u={u} /> : isCoinList(m.amount) ? coinListText(m.amount) : "funds"} to{" "}
            <A a={str("to_address")} />
            {failed && ", but it failed"}
          </>
        ),
        amount: u,
      };
    }
    case "MsgExec": {
      const inner = Array.isArray(m.msgs) ? (m.msgs as TxMessage[]) : [];
      const send = inner.find((x) => splitTypeUrl(x["@type"])[0] === "MsgSend");
      if (send) {
        const u = uaethIn(send.amount);
        return {
          text: (
            <>
              <A a={str("grantee")} /> {failed ? "tried to use" : "used"} a grant to send {u ? <Amt u={u} /> : "funds"} from <A a={send.from_address as string} /> to{" "}
              <A a={send.to_address as string} />
              {failed && ", but it failed"}
            </>
          ),
          amount: u,
        };
      }
      return { text: <><A a={str("grantee")} /> {tried(<>ran {inner.length} message{inner.length === 1 ? "" : "s"} under a grant</>)}</>, amount: "" };
    }
    case "MsgSubmitPoW": {
      const minted = attr(tx.events, "coinbase", "amount");
      const u = /^(\d+)uaeth$/.exec(minted)?.[1] ?? "";
      const native = m.native as { height?: string } | undefined;
      return {
        text: (
          <>
            <A a={str("miner")} /> {tried(<>submitted proof-of-work{native?.height ? <> for block <BlockLink height={Number(native.height)} /></> : m.aux_pow ? " by merged mining" : ""}</>)}
            {!failed && u && <>, minting a <Amt u={u} /> reward</>}
          </>
        ),
        amount: u,
      };
    }
    case "MsgVote":
      return {
        text: (
          <>
            <A a={str("voter")} /> {tried(<>voted <b>{String(m.option ?? "").replace("VOTE_OPTION_", "").replace(/_/g, " ").toLowerCase()}</b> on proposal <Link to="/governance">#{str("proposal_id")}</Link></>)}
          </>
        ),
        amount: "",
      };
    case "MsgSubmitProposal":
      return {
        text: (
          <>
            <A a={str("proposer")} /> {tried(<>proposed sending <Amt u={str("amount") || "0"} /> from the treasury to </>)}
            <A a={str("recipient")} />
          </>
        ),
        amount: str("amount"),
      };
    case "MsgDeposit":
      return { text: <><A a={str("depositor")} /> {tried(<>deposited <Amt u={str("amount") || "0"} /> on proposal <Link to="/governance">#{str("proposal_id")}</Link></>)}</>, amount: str("amount") };
    case "MsgGrant":
      return { text: <><A a={str("granter")} /> {tried(<>let <A a={str("grantee")} /> spend from their account, within limits the chain enforces</>)}</>, amount: "" };
    case "MsgRevoke":
      return { text: <><A a={str("granter")} /> {tried(<>revoked <A a={str("grantee")} />'s permission to spend</>)}</>, amount: "" };
    case "MsgRegisterValidatorPubkey":
      return { text: <><A a={str("miner")} /> {tried("registered a consensus key, so it can sign blocks once it's selected as a validator")}</>, amount: "" };
    default:
      return {
        text: (
          <>
            {tx.signer ? <A a={tx.signer} /> : "Someone"} {tried(<>sent a <MsgTag type={type} /></>)}
            {tx.messages.length > 1 && ` and ${tx.messages.length - 1} more message${tx.messages.length === 2 ? "" : "s"}`}
          </>
        ),
        amount: "",
      };
  }
}

const TABS = ["Overview", "Events", "Raw JSON"] as const;

export default function Transaction() {
  const { hash = "" } = useParams();
  const tx = useApi(() => api.tx(hash), [hash]);
  const [tab, setTab] = useState<(typeof TABS)[number]>("Overview");
  const t = tx.data;

  const confirmations = t && t.latestHeight ? t.latestHeight - t.height : 0;
  const summary = t ? summarize(t) : null;
  const changes = t ? balanceChanges(t.events) : [];
  const inner = t ? t.messages.reduce((a, m) => a + (Array.isArray(m.msgs) ? m.msgs.length : 0), 0) : 0;
  const types = t ? [...new Set(t.msgTypes)] : [];

  return (
    <div className="page">
      <div className="crumbs">
        <span>Transactions</span>
        <span>/</span>
        <span className="dim mono">{shortHash(hash)}</span>
      </div>
      <div className="title-row" style={{ gap: 14 }}>
        <h1 className="page-title">Transaction</h1>
        {t && <TxStatusBadge code={t.code} />}
        {types.map((ty) => (
          <MsgTag key={ty} type={ty} />
        ))}
      </div>
      <div className="hash-line">
        {(t?.hash ?? hash).toUpperCase()}
        <CopyButton value={t?.hash ?? hash} />
      </div>

      <ErrorBanner error={tx.error} />

      {tx.loading && !t ? (
        <div className="loading">Loading…</div>
      ) : t ? (
        <>
          {summary && (
            <div className="summary">
              <div className="summary-text">{summary.text}</div>
              {summary.amount && (
                <div className="right" style={{ flex: "none" }}>
                  <div className="mono" style={{ fontSize: 26, fontWeight: 600 }}>
                    {aeth(summary.amount)}
                  </div>
                  <div className="muted" style={{ fontSize: 12, marginTop: 4 }}>
                    {int(summary.amount)} uaeth
                  </div>
                </div>
              )}
            </div>
          )}

          {t.code !== 0 && t.rawLog && (
            <div className="error-banner gap-top" style={{ marginBottom: 0 }}>
              <span className="mono">
                {t.codespace ? `${t.codespace} · ` : ""}code {t.code}:
              </span>{" "}
              {t.rawLog}
            </div>
          )}

          <Tabs tabs={TABS} value={tab} onChange={setTab} />

          {tab === "Overview" && (
            <div className="split w360">
              <div className="stack">
                <div className="card">
                  <KV label="Block">
                    <span>
                      <BlockLink height={t.height} /> <span className="muted">· {int(confirmations)} confirmations</span>
                    </span>
                  </KV>
                  <KV label="Timestamp">
                    <span>
                      {utc(t.timestamp)} <span className="muted">· {timeAgo(t.timestamp)}</span>
                    </span>
                  </KV>
                  <KV label="Signer">
                    <AddressLink address={t.signer} full />
                  </KV>
                  <KV label="Fee">
                    <span className="mono" style={{ fontSize: 13 }}>
                      {t.fee ? (
                        <>
                          {aeth(t.fee)} <span className="muted">· {int(t.fee)} uaeth</span>
                        </>
                      ) : (
                        "—"
                      )}
                    </span>
                  </KV>
                  <KV label="Gas used / wanted">
                    <span className="mono" style={{ fontSize: 13 }}>
                      {int(t.gasUsed)} / {int(t.gasWanted)}
                    </span>
                    {t.gasWanted > 0 && (
                      <>
                        <MiniBar bone pct={(t.gasUsed / t.gasWanted) * 100} />
                        <span className="mono muted" style={{ fontSize: 13 }}>
                          {Math.round((t.gasUsed / t.gasWanted) * 100)}%
                        </span>
                      </>
                    )}
                  </KV>
                  <KV label="Sequence">
                    <span className="mono" style={{ fontSize: 13 }}>
                      {t.sequence ?? "—"}
                    </span>
                  </KV>
                  <KV label="Memo">
                    {t.memo ? (
                      <span className="mono dim break" style={{ fontSize: 13 }} title="Set by the sender; not verified">
                        {t.memo}
                      </span>
                    ) : (
                      <span className="muted" style={{ fontStyle: "italic" }}>
                        none
                      </span>
                    )}
                  </KV>
                </div>

                <div className="card card-pad">
                  <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
                    <span className="card-title">Messages</span>
                    <span className="mono muted" style={{ fontSize: 12, fontWeight: 500 }}>
                      {t.messages.length} outer{inner > 0 && ` · ${inner} inner`}
                    </span>
                  </div>
                  {t.messages.length > 0 ? (
                    t.messages.map((m, i) => <Message key={i} msg={m} index={String(i)} />)
                  ) : (
                    <div className="muted" style={{ marginTop: 14, fontSize: 13 }}>
                      The node didn't return this transaction's messages.
                    </div>
                  )}
                </div>

                {t.auxPow && (
                  <div className="card">
                    <div className="card-head">
                      <span className="card-title">Merged mining (AuxPoW)</span>
                    </div>
                    <KV label="Parent header">
                      <span className="mono dim break" style={{ fontSize: 12 }}>{t.auxPow.parentHeaderBase64}</span>
                    </KV>
                    <KV label="Coinbase tx">
                      <span className="mono dim break" style={{ fontSize: 12 }}>{t.auxPow.coinbaseTxBase64}</span>
                    </KV>
                    <KV label="Aux block hash">
                      <span className="mono dim break" style={{ fontSize: 12 }}>{t.auxPow.auxBlockHashBase64}</span>
                    </KV>
                  </div>
                )}
              </div>

              <div className="stack">
                <div className="card card-pad">
                  <div className="eyebrow">Lifecycle</div>
                  <div style={{ display: "flex", flexDirection: "column", marginTop: 16 }}>
                    <div className="timeline-step">
                      <div className="timeline-rail">
                        <div className="node" style={{ background: "var(--text-2)" }} />
                        <div className="line" />
                      </div>
                      <div style={{ paddingBottom: 14 }}>
                        <div>Signed</div>
                        <div className="mono muted" style={{ fontSize: 12, marginTop: 2 }}>
                          {t.sequence !== null ? `account sequence ${t.sequence}` : "by the signer"}
                        </div>
                      </div>
                    </div>
                    <div className="timeline-step">
                      <div className="timeline-rail">
                        <div className="node" style={{ background: "var(--red)", boxShadow: "0 0 10px var(--red)" }} />
                        <div className="line" />
                      </div>
                      <div style={{ paddingBottom: 14 }}>
                        <div>
                          Included in block <BlockLink height={t.height} />
                        </div>
                        <div className="mono muted" style={{ fontSize: 12, marginTop: 2 }}>
                          {utc(t.timestamp).split(", ").slice(-1)[0]}
                        </div>
                      </div>
                    </div>
                    <div className="timeline-step">
                      <div className="timeline-rail">
                        <div className="node" style={{ background: t.code === 0 ? "var(--green)" : "var(--danger)" }} />
                      </div>
                      <div>
                        <div>{t.code === 0 ? "Final" : "Final, failed"}</div>
                        <div className="mono muted" style={{ fontSize: 12, marginTop: 2 }}>
                          {int(confirmations)} confirmations
                        </div>
                      </div>
                    </div>
                  </div>
                </div>

                <div className="card card-pad">
                  <div className="eyebrow">Balance changes</div>
                  {changes.length > 0 ? (
                    <div style={{ display: "flex", flexDirection: "column", gap: 12, marginTop: 14, fontSize: 13 }}>
                      {changes.map(([who, v]) => (
                        <div key={who} style={{ display: "flex", justifyContent: "space-between", gap: 12 }}>
                          <AddressLink address={who} plain copy={false} />
                          <span className="mono nowrap" style={{ color: v > 0n ? "var(--green)" : "var(--red)" }}>
                            {v > 0n ? "+" : ""}
                            {aethNumber(v.toString())} AETH
                          </span>
                        </div>
                      ))}
                      <div className="faint" style={{ fontSize: 12 }}>
                        Net, fees included, from the chain's coin events.
                      </div>
                    </div>
                  ) : (
                    <div className="muted" style={{ marginTop: 14, fontSize: 13 }}>
                      No AETH moved.
                    </div>
                  )}
                </div>

                {t.transfers.length > 0 && (
                  <div className="card card-pad">
                    <div className="eyebrow">Transfers</div>
                    <div style={{ display: "flex", flexDirection: "column", gap: 10, marginTop: 14, fontSize: 12 }}>
                      {t.transfers.map((tr, i) => (
                        <div key={i} style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
                          <AddressLink address={tr.from} plain copy={false} />
                          <span className="faint">→</span>
                          <AddressLink address={tr.to} plain copy={false} />
                          <span className="mono" style={{ marginLeft: "auto" }}>
                            {coins(tr.amount)}
                          </span>
                        </div>
                      ))}
                    </div>
                  </div>
                )}
              </div>
            </div>
          )}

          {tab === "Events" && (
            <div className="card">
              {t.events.length > 0 ? (
                t.events.map((ev, i) => (
                  <div key={i} className="row" style={{ gridTemplateColumns: "200px minmax(0,1fr)", fontSize: 13, alignItems: "start" }}>
                    <span className="mono red">{ev.type}</span>
                    <span className="mono dim break">
                      {ev.attributes.length ? ev.attributes.map((a) => `${a.key}=${a.value}`).join(" · ") : "—"}
                    </span>
                  </div>
                ))
              ) : (
                <div className="empty">No events.</div>
              )}
            </div>
          )}

          {tab === "Raw JSON" && (
            <div className="card code">
              <pre style={{ overflow: "auto", maxHeight: 720 }}>{JSON.stringify(t.raw ?? t, null, 2)}</pre>
              <CopyButton value={JSON.stringify(t.raw ?? t, null, 2)} />
            </div>
          )}
        </>
      ) : null}
    </div>
  );
}
