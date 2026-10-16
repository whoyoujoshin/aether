# Connect an agent

Three MCP servers, each with a line or block for every common host. The
first two are hosted: there's nothing to install, so start there.

| Server | What the agent gets | Where it runs |
|---|---|---|
| **Test wallet** | A funded testnet wallet (test AETH, and test USDC while the funder has some), paying listed services with standard x402, and the [agent quest](QUEST.md). Custodial, testnet only ([TEST-WALLET.md](TEST-WALLET.md)) | Hosted: `https://explorer.157-245-252-221.sslip.io/sandbox/mcp` |
| **Read-only** | Balances, transactions, miners and the paid-service directory. Holds no key | Hosted: `https://explorer.157-245-252-221.sslip.io/mcp` |
| **Your own wallet** | `agentmcp`: a spend-capped wallet whose key stays on your machine (1 AETH per payment and 5 AETH a day by default) | Local: `agentmcp init` prints its config |

## Hosted servers

The test wallet is shown. For the read-only server, use the URL ending in
`/mcp` instead of `/sandbox/mcp`, and another name.

**Claude Code**

```bash
claude mcp add --transport http aether-test-wallet https://explorer.157-245-252-221.sslip.io/sandbox/mcp
```

**Claude Desktop and claude.ai:** Settings → Connectors → Add custom
connector, with the URL above. No sign-in is needed.

**Cursor:** [![Add to Cursor](https://cursor.com/deeplink/mcp-install-dark.svg)](https://cursor.com/en/install-mcp?name=aether-test-wallet&config=eyJ1cmwiOiJodHRwczovL2V4cGxvcmVyLjE1Ny0yNDUtMjUyLTIyMS5zc2xpcC5pby9zYW5kYm94L21jcCJ9)
or in `~/.cursor/mcp.json`:

```json
{ "mcpServers": { "aether-test-wallet": { "url": "https://explorer.157-245-252-221.sslip.io/sandbox/mcp" } } }
```

**Codex** (`~/.codex/config.toml`):

```toml
[mcp_servers.aether-test-wallet]
url = "https://explorer.157-245-252-221.sslip.io/sandbox/mcp"
```

**VS Code** (`.vscode/mcp.json`):

```json
{ "servers": { "aether-test-wallet": { "type": "http", "url": "https://explorer.157-245-252-221.sslip.io/sandbox/mcp" } } }
```

**OpenClaw** (merge into `~/.openclaw/openclaw.json`; `openclaw config
schema` has the current fields):

```json
{ "mcp": { "servers": { "aether-test-wallet": { "url": "https://explorer.157-245-252-221.sslip.io/sandbox/mcp", "transport": "streamable-http" } } } }
```

**Any other client** that speaks Streamable HTTP takes the URL as it is.
For one that only runs local (stdio) servers, bridge it with
`npx -y mcp-remote https://explorer.157-245-252-221.sslip.io/sandbox/mcp`.

Then try a prompt:

> Create an Aether test wallet, wait until it's funded, find a paid service
> under 0.001 AETH and buy one call from it. Show me what you got and the
> payment's transaction.

## Your own wallet

```bash
go install github.com/whoyoujoshin/aether/cmd/agentmcp@latest   # Go 1.25+, or take it from a release archive
agentmcp init
```

`init` creates the agent's key, asks the testnet faucet for funds, waits
for them and prints the exact config for your machine. It prints Claude
Code's `claude mcp add` line, the `mcpServers` block (Claude Desktop,
Cursor and most clients), Codex's `codex mcp add` line, VS Code's
`.vscode/mcp.json` and OpenClaw's `mcp.servers` block, each with the
spend caps set. Run it again to print them again for the same key.

Selling to agents instead? See [SELL.md](SELL.md).

The read-only tools can also run locally from npm, with no key:
`npx -y -p aether-chain-client aether-mcp`, or in an `mcpServers` block
`{"command": "npx", "args": ["-y", "-p", "aether-chain-client", "aether-mcp"]}`.
