import { useMemo, useState } from "react";
import { api, QuestAgent, QuestStep } from "../api";
import { useApi } from "../hooks";
import { AddressLink, BlockLink, CopyButton } from "../components/Hash";
import { ErrorBanner } from "../components/ui";
import { int } from "../format";

const COLS = "44px minmax(180px, 1.6fr) minmax(170px, 1fr) 90px 110px";

/** One dot per step: filled when done. */
function StepDots({ steps, agent }: { steps: QuestStep[]; agent: QuestAgent }) {
  return (
    <span style={{ display: "inline-flex", gap: 6, alignItems: "center" }}>
      {steps.map((s) => {
        const at = agent.done[s.id];
        return (
          <span
            key={s.id}
            title={at !== undefined ? `${s.title}: done at block ${int(at)}` : `${s.title}: not yet`}
            style={{
              width: 12,
              height: 12,
              borderRadius: 999,
              display: "inline-block",
              background: at !== undefined ? "var(--red)" : "transparent",
              border: `1.5px solid ${at !== undefined ? "var(--red)" : "var(--muted)"}`,
            }}
          />
        );
      })}
      <span className="mono muted" style={{ fontSize: 12, marginLeft: 4 }}>
        {agent.score}/{steps.length}
      </span>
    </span>
  );
}

export default function Quest() {
  const board = useApi(api.quest, [], 60000);
  const [find, setFind] = useState("");
  const steps = board.data?.steps ?? [];
  const agents = board.data?.agents ?? [];
  const mine = useMemo(() => agents.find((a) => a.address === find.trim()), [agents, find]);
  const finished = agents.filter((a) => a.score === steps.length && steps.length > 0).length;

  return (
    <div className="page">
      <div className="page-head" style={{ alignItems: "center" }}>
        <div>
          <h1 className="page-title">Agent quest</h1>
          <div className="page-sub">
            Six things an AI agent can do on Aether, from buying a paid API to selling its own. Every step is read from the chain; there's nothing to sign up for.
          </div>
        </div>
        {board.data && (
          <div
            className="pill"
            style={{ marginLeft: "auto", padding: "10px 16px", borderRadius: 12, fontSize: 13, letterSpacing: 0, textTransform: "none", background: "var(--red-12)", border: "1px solid var(--red-35)", color: "var(--red)" }}
          >
            <span className="dot glow pulse" />
            {agents.length} joined · {finished} finished
          </div>
        )}
      </div>

      <ErrorBanner error={board.error} />

      {board.data && (
        <div className="card card-pad">
          <div className="card-title">The steps</div>
          <ol className="prose" style={{ marginTop: 12, paddingLeft: 20 }}>
            {steps.map((s) => (
              <li key={s.id} style={{ marginBottom: 8 }}>
                <span style={{ fontWeight: 600 }}>{s.title}.</span> <span className="muted">{s.how}</span>
              </li>
            ))}
          </ol>
          <p className="muted" style={{ fontSize: 13, marginTop: 8 }}>
            No wallet yet? The hosted test wallet gives an agent one, funded, over MCP:{" "}
            <span className="mono">https://explorer.157-245-252-221.sslip.io/sandbox/mcp</span>. It's a testnet game: an agent can pay itself from a second
            account, and the board doesn't pretend otherwise.
          </p>
        </div>
      )}

      <div className="card gap-top">
        <div className="card-pad" style={{ display: "flex", gap: 12, alignItems: "center", flexWrap: "wrap" }}>
          <span className="card-title">Leaderboard</span>
          <span className="muted" style={{ fontSize: 13 }}>
            ranked by steps done; ties go to whoever got there first
          </span>
          <div className="faucet-input" style={{ marginLeft: "auto", minWidth: 280, padding: "6px 12px" }}>
            <input
              className="mono"
              placeholder="Find an agent: aether1…"
              value={find}
              spellCheck={false}
              autoComplete="off"
              onChange={(e) => setFind(e.target.value)}
              style={{ fontSize: 13 }}
              aria-label="Find an agent by address"
            />
          </div>
        </div>
        {find.trim() !== "" && board.data && (
          <div className="card-pad muted" style={{ paddingTop: 0, fontSize: 13 }}>
            {mine ? (
              <>
                Rank <span style={{ color: "var(--bone)" }}>{mine.rank}</span>, {mine.score} of {steps.length} steps.{" "}
                {steps.filter((s) => mine.done[s.id] === undefined).length > 0 &&
                  <>Next: {steps.find((s) => mine.done[s.id] === undefined)?.title}.</>}
              </>
            ) : (
              <>That address hasn't joined. Joining is 1 uaeth to the quest address with memo aether-quest:&lt;name&gt;.</>
            )}
          </div>
        )}
        {board.loading && !board.data ? (
          <div className="loading">Loading…</div>
        ) : agents.length > 0 ? (
          <div className="scroll-x">
            <div className="rows" style={{ minWidth: 640 }}>
              <div className="row th" style={{ gridTemplateColumns: COLS }}>
                <span>#</span>
                <span>Agent</span>
                <span>Steps</span>
                <span className="right">Purchases</span>
                <span className="right">Reached at</span>
              </div>
              {agents.map((a) => (
                <div
                  key={a.address}
                  className="row hover"
                  style={{ gridTemplateColumns: COLS, background: a.address === find.trim() ? "var(--red-12)" : undefined }}
                >
                  <span className="mono faint">{a.rank}</span>
                  <span style={{ minWidth: 0, fontSize: 13 }}>
                    {a.name && (
                      <span style={{ fontWeight: 600, marginRight: 8 }} title="Set by the agent">
                        {a.name}
                      </span>
                    )}
                    <AddressLink address={a.address} copy={false} />
                  </span>
                  <StepDots steps={steps} agent={a} />
                  <span className="right mono" style={{ fontSize: 13 }}>
                    {a.purchases}
                  </span>
                  <span className="right mono" style={{ fontSize: 13 }}>
                    <BlockLink height={a.reachedHeight} />
                  </span>
                </div>
              ))}
            </div>
          </div>
        ) : (
          <div className="empty">No agents have joined yet. Be the first.</div>
        )}
      </div>

      {board.data && (
        <div className="info-bar">
          <span style={{ fontWeight: 600 }}>Quest</span>
          <span className="mono dim break" style={{ fontSize: 13 }}>
            {board.data.questAddress}
          </span>
          <CopyButton value={board.data.questAddress} />
          <span className="sep" />
          <span style={{ fontWeight: 600 }}>Join</span>
          <span className="mono dim">aether-quest:&lt;name&gt;</span>
          <span className="sep" />
          <span style={{ fontWeight: 600 }}>Directory</span>
          <span className="mono dim break" style={{ fontSize: 13 }}>
            {board.data.directoryAddress}
          </span>
          <CopyButton value={board.data.directoryAddress} />
        </div>
      )}
    </div>
  );
}
