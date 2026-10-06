# Aether — Project Context for Claude Code

## What this is
Aether is a custom sovereign blockchain built on Cosmos SDK, featuring:
- A **proof-of-work consensus layer** (custom, not the default Tendermint/CometBFT validator-based consensus)
- A **treasury module**
- A **governance module**

Repo: `github.com/whoyoujoshin/aether`
Solo dev project. Originated from an earlier scaffolding pass in Grok, then built out further here.

## Environment
- **OS:** Windows, developed via PowerShell
- **Stack:** Cosmos SDK v0.50.14, CometBFT v0.38.26, ibc-go v8.8.0, Go 1.25
- **Data directory:** `C:\aether-data` (moved off OneDrive — see gotcha below)
- **Critical:** every `aetherd` command for peer-1 (DardenPC) must include `--home C:\aether-peer1`, its live node home. `C:\aether-data` is the git repo, not a node home. Don't omit this flag or assume a default home dir.

## Current state (as of 2026-10-06)
- **Public testnet `aether-testnet-1`** is live, with four validators: the seed, sync3 and sync4 (DigitalOcean, run by Gitty) and peer-1 (DardenPC). All four run `2c32e6a` or later. The tip was about 217,000 on 2026-10-06.
- **October upgrade** activated at block 205,000 (`October2026UpgradeHeight`): the ASERT difficulty retarget, the randomness beacon, and the merged-mining rules.
- **IBC to Osmosis testnet** has been live since 2026-10-04 (Aether `channel-1` ↔ Osmosis `channel-11841`). Helicase relays onto Aether from the validators; `cmd/outbound` on the seed relays onto Osmosis. See `docs/OSMOSIS-TESTNET.md`.
- **USDC** is Circle's testnet USDC on Injective, through Osmosis (`wallet.TestnetUSDC`). Noble's testnet is shutting down. The first transfer from Injective over `channel-77026` is still to do.
- **`--gas auto`** works on ML-DSA accounts at the default adjustment, with fees given either way (PRs #86 and #88). The node answering `--node` needs `5267ab3` or later for the `--gas-prices` case.
- The latest release tag is `v0.2.5-testnet`. The MCP Registry's `0.2.6-testnet` entry was published by hand and points at the `v0.2.5` bundle; the next tag should be `v0.2.7-testnet`.

## Known gotchas (hard-won, don't relearn these)

1. **CometBFT validator set lock-in.** After the first successful `InitChain`, the validator set is locked in. Editing `genesis.json` afterward is silently ignored — no error, it just doesn't take effect. The only fix is a full wipe of the `.aether` directory and re-init. If something "isn't taking effect" after a genesis edit, check this first.

2. **Cosmos SDK v0.50 type renames.** `sdk.Int` → `math.Int`, `sdk.NewDecFromInt` → `math.LegacyNewDecFromInt`. These renames are pervasive across the codebase — if you see a type error referencing `sdk.Int` or similar, it's almost always this.

3. **AppGenesis wrapper structure.** In SDK v0.50, validators nest under `consensus.validators` (a sibling of `consensus.params`) — not at the top level like older CometBFT docs/examples suggest. Don't trust older tutorials on genesis structure.

4. **OneDrive + LevelDB = corruption.** OneDrive sync causes repeated LevelDB corruption (`version does not exist` panics). This is why chain data lives in `C:\aether-data`, outside any synced folder. Never let `.aether` end up under a synced directory again.

5. **Protobuf codegen placement.** `buf.yaml` / `buf.gen.yaml` live inside `proto/` (not the project root) to match the `aether.pow.v1` package path. After running `buf generate`, generated files must be manually moved into `x/pow/tx.pb.go` — this isn't automatic.

## Tooling in the repo
- `aether.ps1` — PowerShell wrapper script with `reset` / `start` / `genesis` subcommands, auto-injects `--home`
- `DEVNET.md` — devnet setup/reset guide
- `genesis.template.json` — template for genesis resets
- `find_nonce.go` — standalone helper for brute-forcing valid PoW nonces matching the keeper's encoding exactly (useful reference for how the keeper expects nonce/hash encoding)

## Working style / preferences
- This is iterative, multi-session debugging work — pick up from actual current file state rather than assuming past fixes are still in place, since the human sometimes resolves blockers independently between sessions.
- When something breaks, check the gotchas list above before going down a fresh rabbit hole.
- Prefer running the real `aetherd` command / build / `buf generate` and reading actual output over speculating about what should happen.
- **Validator safety:** a validator key runs in exactly one place. Stop the old process before starting the new one, never both at once. Restart a validator only while the other three are signing, one node at a time.
- **Operators:** Joshua forwards messages to Gitty himself. Write notes for Gitty as text Joshua can copy; don't email Gitty directly.
