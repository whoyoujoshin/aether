# Releasing

Tags are pushed by hand, so nothing reaches `@latest`, the release downloads or the MCP Registry without someone deciding to ship it. Tag when `agentmcp`, `paywall`, the clients or anything chain-facing changes; explorer and docs changes don't need a release.

## 1. Tag

From a clone, on the merge commit you're releasing (normally the tip of `main`):

```bash
git fetch origin
git tag -a v0.2.1-testnet origin/main -m "v0.2.1-testnet: <what changed>"
git push origin v0.2.1-testnet
```

Versions are `vMAJOR.MINOR.PATCH-testnet`; Go orders them, so `go install .../agentmcp@latest` picks the newest.

## 2. Release Binaries (automatic, about 5 minutes)

`.github/workflows/release.yml` publishes a prerelease with:

- `aether-<platform>.tar.gz` for Linux, Windows and macOS (Intel and Apple Silicon): `aetherd`, `wallet`, `faucet`, `explorer`, `agentmcp`, `paywall`, `powminer`
- `aether-wallet.mcpb`: agentmcp as an MCP bundle for every platform (built by `scripts/package-mcpb.sh`)
- `server.json`: the bundle's MCP Registry entry, with its SHA-256

Check the run in the Actions tab and that the release has all six files.

## 3. MCP Registry

Publishing needs the `whoyoujoshin` GitHub account, because the name `io.github.whoyoujoshin/aether-wallet` is verified by signing in as it. Install [`mcp-publisher`](https://github.com/modelcontextprotocol/registry/releases), then from an empty folder:

```bash
curl -LO https://github.com/whoyoujoshin/aether/releases/download/v0.2.1-testnet/server.json
mcp-publisher login github     # opens a device-code sign-in
mcp-publisher publish
```

The listing appears at https://registry.modelcontextprotocol.io within minutes; catalogs that mirror the registry pick it up on their own schedules. Publish once per release; a new version replaces the listed one.
