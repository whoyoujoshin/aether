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

## 4. npm and PyPI (automatic)

The same tag runs `publish-clients.yml`, which tests both clients and publishes them as `aether-chain-client`: the TypeScript client to [npm](https://www.npmjs.com/package/aether-chain-client), the Python client to [PyPI](https://pypi.org/project/aether-chain-client/) (imported as `aether_client`). The version is the tag's number: `v0.2.9-testnet` publishes `0.2.9`. A registry never accepts a version twice, so a failed release is fixed with the next tag, not a re-run.

- Neither registry needs a secret: both trust this repository's `publish-clients.yml` by name (a trusted publisher on the `aether-chain-client` package on npm and project on PyPI). Renaming the file breaks publishing until both are updated too.
- npm's package settings disallow token publishing, so a leaked token can't publish; only this workflow can.

### The ElizaOS plugin

The same workflow publishes `clients/eliza` as [`elizaos-plugin-aether`](https://www.npmjs.com/package/elizaos-plugin-aether), at the tag's version, depending on the client version that tag published. A trusted publisher is set in a package's settings on npmjs.com, so the package has to exist first: its first version goes up by hand, once; until then the job skips publishing with a warning. On a computer with Node 20 or later (DardenPC), after a tag's `aether-chain-client` is on npm (here 0.2.11), in PowerShell:

```powershell
git clone https://github.com/whoyoujoshin/aether C:\aether-publish   # a separate clone: leave C:\aether-data (peer-1's) alone
cd C:\aether-publish\clients\ts; npm ci; npm run build
cd ..\eliza; npm ci; npm test
npm version 0.2.11 --no-git-tag-version
npm pkg set "dependencies.aether-chain-client=^0.2.11"
npm login
npm publish --access public
cd C:\; Remove-Item -Recurse -Force C:\aether-publish
```

Then on npmjs.com, in the package's **Settings**: add a trusted publisher (GitHub Actions, `whoyoujoshin` / `aether`, workflow `publish-clients.yml`, no environment) and tick **Allow npm publish** (without it the workflow can only stage a version for approval), and under publishing access choose to require two-factor authentication and disallow tokens. Later tags publish it from the workflow. Done for 0.2.11 on 2026-10-11.

On Windows, run npm as `npm.cmd` if PowerShell refuses `npm.ps1` (scripts disabled), and note that `cmd` drops a `^` in arguments: the hand-published 0.2.11 depends on `aether-chain-client` `0.2.11` exactly; the workflow, on Linux, writes `^<version>`.
