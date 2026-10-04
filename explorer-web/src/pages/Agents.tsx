import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api, AgentCard } from "../api";
import { useApi } from "../hooks";
import { CopyButton } from "../components/Hash";
import { ErrorBanner } from "../components/ui";
import { int } from "../format";

function Code({ children }: { children: string }) {
  return (
    <div className="well code">
      <pre>{children}</pre>
      <CopyButton value={children} />
    </div>
  );
}

// Docs links come from our own API, but only ever link http(s).
function safeHref(url: string): string | undefined {
  return /^https?:\/\//i.test(url) ? url : undefined;
}

const docLinks = [
  ["start", "Agent quick start"],
  ["mcp", "MCP wallet guide"],
  ["integration", "Integration guide"],
  ["releases", "Prebuilt binaries"],
];

const sections: [string, string][] = [
  ["quickstart", "Quickstart"],
  ["endpoints", "Endpoints"],
  ["by-hand", "By hand"],
  ["spending", "Spending caps"],
  ["payments", "Pay and get paid"],
];

type Lang = "mcp" | "curl" | "aetherd";

function samples(c: AgentCard): Record<Lang, string> {
  const ep = c.endpoints;
  return {
    mcp: `${c.mcp.install}\n${c.mcp.init}`,
    curl: [
      c.faucet ? `curl -X POST ${c.faucet.url} \\\n  -H 'Content-Type: application/json' \\\n  -d '{"address":"${c.addressPrefix}1..."}'\n` : "",
      `curl '${ep.explorer}/api/address?addr=${c.addressPrefix}1...'`,
    ]
      .filter(Boolean)
      .join("\n"),
    aetherd: `aetherd query bank balances ${c.addressPrefix}1... \\\n  --node ${ep.rpc || "<rpc>"} --chain-id ${c.chainId}`,
  };
}

/** The live agent card as a bot sees it, with how long the explorer took to answer. */
function LiveResponse() {
  const [res, setRes] = useState<{ ms: number; status: number; body: string } | null>(null);
  useEffect(() => {
    let cancelled = false;
    const t0 = performance.now();
    fetch("/api/agents")
      .then(async (r) => {
        const body = await r.json().catch(() => ({}));
        const trimmed = r.ok
          ? { chainId: body.chainId, height: body.height, addressPrefix: body.addressPrefix, denom: body.denom, decimals: body.decimals, endpoints: body.endpoints }
          : body;
        if (!cancelled) setRes({ ms: Math.round(performance.now() - t0), status: r.status, body: JSON.stringify(trimmed, null, 2) });
      })
      .catch((e) => !cancelled && setRes({ ms: 0, status: 0, body: String(e) }));
    return () => {
      cancelled = true;
    };
  }, []);
  const ok = res && res.status >= 200 && res.status < 300;
  return (
    <div className="card">
      <div className="mono" style={{ padding: "12px 16px", borderBottom: "1px solid var(--border)", fontSize: 12, fontWeight: 500, color: res ? (ok ? "var(--green)" : "var(--danger)") : "var(--muted)" }}>
        GET /api/agents · {res ? `${res.status || "error"} · ${res.ms}ms` : "…"}
      </div>
      <div className="code">
        <pre>{res?.body ?? ""}</pre>
      </div>
    </div>
  );
}

export default function Agents() {
  const card = useApi(api.agents, [], 15000);
  const dir = useApi(api.services, [], 60000);
  const [lang, setLang] = useState<Lang>("mcp");
  const [active, setActive] = useState("quickstart");
  const c = card.data;
  const ep = c?.endpoints;
  const services = dir.data?.services.length;

  return (
    <div className="guide">
      <nav className="guide-nav">
        <div className="eyebrow">Guide</div>
        {sections.map(([id, label]) => (
          <a key={id} href={`#${id}`} className={active === id ? "on" : ""} onClick={() => setActive(id)}>
            {label}
          </a>
        ))}
        <div className="eyebrow">Machine-readable</div>
        <a href="/start.md" className="mono" style={{ fontSize: 13 }}>
          /start.md
        </a>
        <a href="/api/agents" className="mono" style={{ fontSize: 13 }}>
          /api/agents
        </a>
        <a href="/api/stats" className="mono" style={{ fontSize: 13 }}>
          /api/stats
        </a>
      </nav>

      <div className="guide-main">
        <div className="eyebrow red">For agents</div>
        <h1 className="page-title" style={{ marginTop: 10 }}>
          Read and act on Aether from code
        </h1>
        <div className="dim" style={{ fontSize: 15, lineHeight: 1.6, marginTop: 10, maxWidth: 640 }}>
          Everything a bot needs to start on this chain: the chain ID, public endpoints, the faucet, and an MCP wallet whose spending the chain itself can cap.
          From nothing to a first payment on one page, copy-paste for MCP, TypeScript and Python: <a href="/start.md">/start.md</a>.
        </div>

        <div style={{ marginTop: 18 }}>
          <ErrorBanner error={card.error} prefix="Failed to load the agent card" />
          {c && c.warnings.length > 0 && (
            <div className="warning-banner">
              {c.warnings.map((w) => (
                <div key={w}>{w}</div>
              ))}
            </div>
          )}
        </div>

        <div className="stat-tiles" style={{ marginTop: 6 }}>
          <div className="stat-tile">
            <div className="muted" style={{ fontSize: 12 }}>Chain ID</div>
            <div className="v">{c?.chainId ?? "—"}</div>
          </div>
          <Link to="/blocks" className="stat-tile" style={{ color: "inherit", textDecoration: "none" }}>
            <div className="muted" style={{ fontSize: 12 }}>Latest height</div>
            <div className="v">{c?.height ? int(c.height) : "—"}</div>
          </Link>
          <div className="stat-tile">
            <div className="muted" style={{ fontSize: 12 }}>Faucet</div>
            <div className="v">
              {!c ? "—" : !c.faucet ? "none" : c.faucet.reachable ? <span className="green">reachable</span> : <span className="danger">unreachable</span>}
            </div>
          </div>
          <div className="stat-tile">
            <div className="muted" style={{ fontSize: 12 }}>Spend-capped grants</div>
            <div className="v">{!c ? "—" : c.authz.active ? <span className="green">live</span> : <span className="muted">from block {int(c.authz.activationHeight)}</span>}</div>
          </div>
        </div>

        {c && ep && (
          <>
            <div id="quickstart" className="guide-section split" style={{ gridTemplateColumns: "minmax(0,1fr) minmax(0,1fr)" }}>
              <div className="card">
                <div className="seg">
                  {(["mcp", "curl", "aetherd"] as Lang[]).map((l) => (
                    <button key={l} className={l === lang ? "on" : ""} onClick={() => setLang(l)}>
                      {l}
                    </button>
                  ))}
                </div>
                <div className="code">
                  <pre>{samples(c)[lang]}</pre>
                  <CopyButton value={samples(c)[lang]} />
                </div>
              </div>
              <LiveResponse />
            </div>

            <div className="prose guide-section" style={{ marginTop: 16 }}>
              <p>
                <span className="mono">init</span> creates a disposable ML-DSA-44 account (its recovery phrase is shown once), funds it from the faucet, waits for
                the funds and prints the exact <span className="mono">claude mcp add …</span> command and JSON config for your client, already pointed at this network.
                Needs Go 1.25+, or use a prebuilt binary. The wallet caps spending per payment and per day, and can ask its owner to approve larger payments.
              </p>
              <div style={{ display: "flex", gap: 6, flexWrap: "wrap", marginTop: 12 }}>
                {c.mcp.tools.map((t) => (
                  <span key={t} className="tag" style={{ color: "var(--text-2)", background: "var(--cell)" }}>
                    {t}
                  </span>
                ))}
              </div>
            </div>

            <div id="endpoints" className="card guide-section">
              <div className="card-head">
                <span className="card-title">Endpoints</span>
                <span className="card-meta">
                  machine-readable: <a href="/api/agents">/api/agents</a>
                </span>
              </div>
              {(
                [
                  ["HTTP", "RPC (CometBFT)", ep.rpc],
                  ["GRPC", "gRPC", ep.grpc],
                  ["POST", "Faucet", ep.faucet],
                  ["GET", "Explorer API", ep.explorer ? ep.explorer + "/api" : ""],
                  ["P2P", "Seed", ep.seed],
                ] as const
              )
                .filter(([, , v]) => v)
                .map(([method, k, v]) => (
                  <div key={k} className="row hover" style={{ gridTemplateColumns: "64px 150px minmax(0,1fr)", padding: "13px 18px" }}>
                    <span className="mono" style={{ fontSize: 11, fontWeight: 600, color: method === "POST" ? "var(--red)" : "var(--green)" }}>
                      {method}
                    </span>
                    <span className="muted">{k}</span>
                    <span style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0 }}>
                      <span className="mono ellipsis" style={{ fontSize: 13, fontWeight: 500 }}>
                        {v}
                      </span>
                      <CopyButton value={v} />
                    </span>
                  </div>
                ))}
              <div className="row" style={{ gridTemplateColumns: "64px 150px minmax(0,1fr)", padding: "13px 18px" }}>
                <span />
                <span className="muted">Units</span>
                <span className="muted" style={{ fontSize: 13 }}>
                  1 {c.displayDenom} = 10<sup>{c.decimals}</sup> <span className="mono dim">{c.denom}</span> · addresses start{" "}
                  <span className="mono dim">{c.addressPrefix}1…</span> · {c.signatures}
                </span>
              </div>
            </div>

            <div id="by-hand" className="card card-pad guide-section">
              <span className="card-title">Or by hand</span>
              <div className="prose" style={{ marginTop: 12 }}>
                {c.faucet && (
                  <>
                    <p>Fund any address from the faucet (rate-limited):</p>
                    <div style={{ marginTop: 10 }}>
                      <Code>{`curl -X POST ${c.faucet.url} \\\n  -H 'Content-Type: application/json' \\\n  -d '{"address":"aether1..."}'`}</Code>
                    </div>
                  </>
                )}
                <p>Check a balance and its recent transfers:</p>
                <div style={{ marginTop: 10 }}>
                  <Code>{`curl '${ep.explorer}/api/address?addr=aether1...'`}</Code>
                </div>
                <p>
                  With <span className="mono">aetherd</span> (build it from the repo), point any command at the node with{" "}
                  <span className="mono">
                    --node {ep.rpc || "<rpc>"} --chain-id {c.chainId}
                  </span>
                  .
                </p>
              </div>
            </div>

            <div id="spending" className="card card-pad guide-section">
              <div style={{ display: "flex", gap: 12, alignItems: "baseline", flexWrap: "wrap" }}>
                <span className="card-title">Let an agent spend from your account, capped by the chain</span>
                <span className="card-meta">x/authz + x/feegrant</span>
              </div>
              <div className="prose" style={{ marginTop: 12 }}>
                <Code>{`aetherd tx authz grant <agent-address> send --spend-limit 1000000uaeth --expiration <unix-ts> --from <you>
aetherd tx feegrant grant <you> <agent-address> --spend-limit 100000uaeth --from <you>
aetherd tx authz revoke <agent-address> /cosmos.bank.v1beta1.MsgSend --from <you>`}</Code>
                <p>
                  Then run the wallet with <span className="mono">--granter &lt;you&gt; --fee-granter &lt;you&gt;</span>: the agent needs no balance, the chain
                  enforces the limit and expiry, and you can revoke at any time. Any address page here shows its spending permissions.
                </p>
              </div>
            </div>

            <div id="payments" className="card card-pad guide-section">
              <div style={{ display: "flex", gap: 12, alignItems: "baseline", flexWrap: "wrap" }}>
                <span className="card-title">Pay and get paid</span>
                <span className="card-meta">HTTP 402 · {c.paymentSchemes.join(" · ")}</span>
              </div>
              <div className="prose" style={{ marginTop: 12 }}>
                <p>
                  Agents pay for API calls with <span className="mono">fetch_paid</span>: per request, from a prepaid balance, or from a capped on-chain allowance
                  the seller collects later. Sell your own with <span className="mono">cmd/paywall</span> or the Node and Python seller kits, and list it in the{" "}
                  <Link to="/services">service directory</Link>
                  {services !== undefined && ` (${services} listed)`}.
                </p>
                <p>
                  {docLinks
                    .filter(([key]) => safeHref(c.docs[key] ?? ""))
                    .map(([key, label], i) => (
                      <span key={key}>
                        {i > 0 && " · "}
                        <a href={safeHref(c.docs[key])} target="_blank" rel="noopener noreferrer">
                          {label}
                        </a>
                      </span>
                    ))}
                </p>
              </div>
            </div>
          </>
        )}
        {card.loading && !c && <div className="loading">Loading…</div>}
      </div>
    </div>
  );
}
