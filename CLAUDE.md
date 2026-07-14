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
- **Stack:** Cosmos SDK v0.50.12, CometBFT v0.38.12, Go
- **Data directory:** `C:\aether-data` (moved off OneDrive — see gotcha below)
- **Critical:** every `aetherd` command must include `--home C:\aether-data\.aether`. Don't omit this flag or assume a default home dir.

## Current state (as of last check-in)
- Build is clean; `BeginBlock` difficulty retargeting runs stably
- `x/auth` and `x/bank` fully wired into `app.go` (keepers, AnteHandler, configurator, interface registry)
- `DistributeBlockReward` uses real `MintCoins` + `SendCoinsFromModuleToAccount` / `SendCoinsFromModuleToModule` calls
- `pow.ModuleName` is registered in `maccPerms` with `Minter` permission
- `MsgSubmitPoW` CLI command is wired into `cmd/aetherd/main.go`
- **Open item:** confirm a successful end-to-end `MsgSubmitPoW` transaction on devnet

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
