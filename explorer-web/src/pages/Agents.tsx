import { Link } from "react-router-dom";
import { api } from "../api";
import { useApi } from "../hooks";
import { CopyButton } from "../components/Hash";

function Code({ children }: { children: string }) {
  return (
    <div className="code-block">
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
];

export default function Agents() {
  const card = useApi(api.agents, [], 15000);
  const dir = useApi(api.services, [], 60000);
  const c = card.data;
  const ep = c?.endpoints;
  const services = dir.data?.services.length;

  return (
    <div className="page">
      <h1 className="page-title">For AI agents</h1>

      {card.error && <div className="error-banner">Failed to load the agent card: {card.error}</div>}
      {c && c.warnings.length > 0 && (
        <div className="warning-banner">
          {c.warnings.map((w) => (
            <div key={w}>{w}</div>
          ))}
        </div>
      )}

      <div className="stat-grid">
        <div className="stat-card">
          <div className="stat-label">Chain ID</div>
          <div className="stat-value mono">{c?.chainId ?? "—"}</div>
        </div>
        <Link to="/blocks" className="stat-card" style={{ display: "block", textDecoration: "none" }}>
          <div className="stat-label">Latest Height</div>
          <div className="stat-value">{c?.height || "—"}</div>
        </Link>
        <div className="stat-card">
          <div className="stat-label">Faucet</div>
          <div className="stat-value">
            {!c ? "—" : !c.faucet ? "none" : c.faucet.reachable ? (
              <span className="badge badge-success">reachable</span>
            ) : (
              <span className="badge badge-danger">unreachable</span>
            )}
          </div>
        </div>
        <div className="stat-card">
          <div className="stat-label">Spend-capped grants</div>
          <div className="stat-value">
            {!c ? "—" : c.authz.active ? (
              <span className="badge badge-success">live</span>
            ) : (
              <span className="badge badge-neutral">from block {c.authz.activationHeight}</span>
            )}
          </div>
        </div>
      </div>

      {c && (
        <>
          <div className="panel">
            <div className="panel-header">
              <div className="panel-title">Start in two commands</div>
              <div className="panel-meta">MCP wallet for Claude, Cursor and other MCP clients</div>
            </div>
            <div className="panel-body">
              <Code>{`${c.mcp.install}\n${c.mcp.init}`}</Code>
              <p>
                <span className="mono">init</span> creates a disposable ML-DSA-44 account (its recovery phrase is shown once), funds it from the
                faucet, waits for the funds and prints the exact <span className="mono">claude mcp add …</span> command and JSON config for your
                client, already pointed at this network. Needs Go 1.25+. The wallet caps spending per payment and per day, and can ask its
                owner to approve larger payments.
              </p>
              <div className="tag-row">
                {c.mcp.tools.map((t) => (
                  <span key={t} className="badge badge-neutral mono" style={{ textTransform: "none" }}>
                    {t}
                  </span>
                ))}
              </div>
            </div>
          </div>

          <div className="panel">
            <div className="panel-header">
              <div className="panel-title">Endpoints</div>
              <div className="panel-meta">
                machine-readable: <a href="/api/agents" className="mono">/api/agents</a>
              </div>
            </div>
            <table>
              <tbody>
                {[
                  ["RPC (CometBFT)", ep!.rpc],
                  ["gRPC", ep!.grpc],
                  ["Faucet", ep!.faucet],
                  ["Explorer API", ep!.explorer + "/api"],
                  ["Seed", ep!.seed],
                ]
                  .filter(([, v]) => v)
                  .map(([k, v]) => (
                    <tr key={k}>
                      <td>{k}</td>
                      <td>
                        <span className="mono" style={{ wordBreak: "break-all" }}>
                          {v}
                        </span>{" "}
                        <CopyButton value={v} />
                      </td>
                    </tr>
                  ))}
                <tr>
                  <td>Units</td>
                  <td>
                    1 {c.displayDenom} = 10<sup>{c.decimals}</sup> <span className="mono">{c.denom}</span> · addresses start{" "}
                    <span className="mono">{c.addressPrefix}1…</span> · {c.signatures}
                  </td>
                </tr>
              </tbody>
            </table>
          </div>

          <div className="panel">
            <div className="panel-header">
              <div className="panel-title">Or by hand</div>
            </div>
            <div className="panel-body">
              {c.faucet && (
                <>
                  <p>Fund any address from the faucet (rate-limited):</p>
                  <Code>{`curl -X POST ${c.faucet.url} \\\n  -H 'Content-Type: application/json' \\\n  -d '{"address":"aether1..."}'`}</Code>
                </>
              )}
              <p>Check a balance and its recent transfers:</p>
              <Code>{`curl '${ep!.explorer}/api/address?addr=aether1...'`}</Code>
              <p>
                With <span className="mono">aetherd</span> (build it from the repo), point any command at the node with{" "}
                <span className="mono">--node {ep!.rpc || "<rpc>"} --chain-id {c.chainId}</span>.
              </p>
            </div>
          </div>

          <div className="panel">
            <div className="panel-header">
              <div className="panel-title">Let an agent spend from your account, capped by the chain</div>
              <div className="panel-meta">x/authz + x/feegrant</div>
            </div>
            <div className="panel-body">
              <Code>{`aetherd tx authz grant <agent-address> send --spend-limit 1000000uaeth --expiration <unix-ts> --from <you>
aetherd tx feegrant grant <you> <agent-address> --spend-limit 100000uaeth --from <you>
aetherd tx authz revoke <agent-address> /cosmos.bank.v1beta1.MsgSend --from <you>`}</Code>
              <p>
                Then run the wallet with <span className="mono">--granter &lt;you&gt; --fee-granter &lt;you&gt;</span>: the agent needs no balance, the
                chain enforces the limit and expiry, and you can revoke at any time. Any address page here shows its spending permissions.
              </p>
            </div>
          </div>

          <div className="panel">
            <div className="panel-header">
              <div className="panel-title">Pay and get paid</div>
              <div className="panel-meta">HTTP 402 · {c.paymentSchemes.join(" · ")}</div>
            </div>
            <div className="panel-body">
              <p>
                Agents pay for API calls with <span className="mono">fetch_paid</span>: per request, from a prepaid balance, or from a capped
                on-chain allowance the seller collects later. Sell your own with <span className="mono">cmd/paywall</span> or the Node and Python
                seller kits, and list it in the <Link to="/services">service directory</Link>
                {services !== undefined && ` (${services} listed)`}.
              </p>
              <p className="panel-meta">
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
  );
}
