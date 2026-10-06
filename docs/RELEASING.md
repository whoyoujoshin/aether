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

The registry never accepts a version twice. `0.2.6-testnet` was published to it by hand on 2026-10-03 (to add the remote), with no matching release, so the next tag is `v0.2.7-testnet` or later.

## 2. Release Binaries (automatic, about 5 minutes)

`.github/workflows/release.yml` publishes a prerelease with:

- `aether-<platform>.tar.gz` for Linux, Windows and macOS (Intel and Apple Silicon): `aetherd`, `wallet`, `faucet`, `explorer`, `agentmcp`, `paywall`, `powminer`
- `aether-wallet.mcpb`: agentmcp as an MCP bundle for every platform (built by `scripts/package-mcpb.sh`)
- `server.json`: the bundle's MCP Registry entry, with its SHA-256, and the public read-only endpoint (`https://explorer.157-245-252-221.sslip.io/mcp`) as its remote

Check the run in the Actions tab and that the release has all six files.

## 3. MCP Registry (automatic)

After the release is published, the workflow's `registry` job checks that the released `aether-wallet.mcpb` matches `server.json`'s SHA-256, then publishes `io.github.whoyoujoshin/aether-wallet`. It signs in as the repository's GitHub Actions run (`mcp-publisher login github-oidc`), so there's no token to store: the registry lets this repository's workflows publish `io.github.whoyoujoshin/*` names. The listing updates at https://registry.modelcontextprotocol.io within minutes; catalogs that mirror the registry pick it up on their own schedules.

If that job fails, publish by hand from an empty folder with the `whoyoujoshin` GitHub account ([`mcp-publisher`](https://github.com/modelcontextprotocol/registry/releases)):

```bash
curl -LO https://github.com/whoyoujoshin/aether/releases/download/v0.2.1-testnet/server.json
mcp-publisher login github     # device code: approve it while signed in as whoyoujoshin
mcp-publisher publish
```
