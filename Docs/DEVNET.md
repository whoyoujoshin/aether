# Aether Devnet — Reset & Validator Setup

This documents the exact, working procedure for standing up a local single-validator
devnet, plus the schema gotchas that cost real time to work out. Follow this in order
every time you wipe `.aether`.

## Why this isn't just `aetherd init` and `aetherd start`

This project's Cosmos SDK version (v0.50.x) uses `genutiltypes.AppGenesis`, a wrapper
type around the classic CometBFT genesis doc. It does **not** match the genesis
format shown in most CometBFT docs/tutorials. Two things trip people up:

1. **Validators are NOT top-level.** In stock CometBFT, `"validators"` sits at the
   root of the genesis file. In this project's format, validators live *nested
   inside* `"consensus"`, as a sibling of `"params"`:
   ```json
   "consensus": {
     "validators": [ ... ],
     "params": { ... }
   }
   ```
   This is confirmed directly in `cosmos-sdk/x/genutil/client/cli/init.go`, which
   constructs `appGenesis.Consensus = &types.ConsensusGenesis{Validators: ..., Params: ...}`.

2. **`aetherd init` never creates a validator entry.** It generates
   `priv_validator_key.json` (the node's actual consensus keypair) but leaves
   genesis's validator list empty. You must inject a validator by hand (see below)
   until/unless a genutil-based "add validator" flow is wired in for this custom
   PoW chain (there's no `x/staking` module to do this automatically).

3. **CometBFT locks in the validator set after the first successful `InitChain`.**
   Once a node completes its ABCI handshake once, the parsed validator set is
   persisted into CometBFT's own state DB. **Any edits you make to `genesis.json`
   after that first successful start are silently ignored** — the node keeps using
   what it already loaded. If you need to change the validator set, you must fully
   wipe `.aether` and start over. This is the single biggest time-sink if missed:
   it looks like your edits "aren't taking," when actually they're just not being
   re-read.

## Clean reset procedure (follow exactly, in order)

```powershell
# 1. Full wipe — required any time you're changing the validator set
Remove-Item -Recurse -Force $env:USERPROFILE\.aether -ErrorAction SilentlyContinue

# 2. Fresh init — creates a new priv_validator_key.json (new keypair each time)
go run ./cmd/aetherd init mynode --chain-id aether-testnet-1 --overwrite

# 3. Get this node's actual consensus pubkey (authoritative — don't hand-copy from
#    priv_validator_key.json, this command formats it correctly for genesis)
go run ./cmd/aetherd comet show-validator
```

Copy the `"key"` value from step 3's output. Then, **before running `start` for the
first time**, edit `$env:USERPROFILE\.aether\config\genesis.json` and replace the
entire file with `genesis.template.json` (in this repo), substituting:
- `genesis_time` → current UTC timestamp (or leave whatever `init` generated)
- the `pub_key.value` under `consensus.validators[0]` → the key from step 3

Leave `"address"` out of the validator entry entirely — CometBFT derives it from
the pubkey. (You can also supply it explicitly if you want to double-check it
against the `addr=` field printed in the node's own startup log, but it's not
required.)

Only after that edit is saved, start the node:

```powershell
go run ./cmd/aetherd start --minimum-gas-prices="0.0001aeth"
```

## Confirming success

Look for these lines, in order, in the startup log:

```
>>> InitChainer called! Validators: 1
... This node is a validator addr=... pubKey=...
... Reactor  module=consensus waitSync=false
... finalizing commit of block ... height=1 ...
... committed state ... height=1 ...
```

`waitSync=false` and `"This node is a validator"` are the two lines that confirm
the validator set actually took. If you instead see `waitSync=true` and
`"This node is not a validator"`, the pubkey in genesis didn't match this node's
key — usually because of the stale-state issue in point 3 above (you edited
genesis after an earlier successful start on the same `.aether` folder).

## Sanity-check commands (while the node is running, in a second terminal)

```powershell
# What validator set did CometBFT actually load? (ground truth, bypasses any
# assumptions about what the file "should" contain)
(Invoke-WebRequest -UseBasicParsing http://127.0.0.1:26657/genesis).Content

# Is this node an active validator with nonzero voting power?
(Invoke-WebRequest -UseBasicParsing http://127.0.0.1:26657/status).Content
```
(`-UseBasicParsing` avoids a PowerShell script-execution security prompt.)

## Known remaining gaps (as of this writing)

The `x/pow` module compiles and is wired into `app.go`, but nothing in the app
actually calls it yet:
- No `MsgSubmitPoW` / msg server — miners can't submit proofs.
- No `BeginBlocker`/`EndBlocker` hook calling `DistributeBlockReward`.
- `AdjustDifficulty` is a stub that just returns the current difficulty.
- Params/difficulty/reward are stored as raw JSON in the KV store rather than
  proto-marshaled types — fine for a devnet, should become `.proto`-defined
  before a real testnet.
