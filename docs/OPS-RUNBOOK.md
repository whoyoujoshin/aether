# Ops runbook: height alerts and binary cutovers

For the four validators of `aether-testnet-1` (seed, sync3, sync4, peer-1). This is the repeatable procedure. Past one-off swaps are written up in `docs/UPGRADE-*.md`; use the scripts here for the next one.

Nothing in this runbook changes consensus. Builds happen on a workstation or in CI. A compile on a validator has halted the chain (2026-10-17, sync3).

## Two monitors

| Process | What it watches | Where |
|---|---|---|
| `aether-minerwatch` (`cmd/minerwatch`) | One gRPC node and the miner addresses: submissions, seats, balance, that node's stall, `signing_low`, `power_concentrated`, `validator_new` | Seed, already |
| `aether-chainwatch` (`cmd/chainwatch`) | One or more CometBFT RPCs: tip stall, a node behind or `catching_up`, RPC down, a validator that cannot be stopped | Seed, beside minerwatch |

minerwatch's `power_concentrated` fires when a share is **over** a third. chainwatch's stop check is the CometBFT rule: a block commits only when the precommits hold **strictly more than 2/3**. A validator at exactly 1/3 is already unsafe to stop, because the others hold exactly 2/3.

Webhook URLs and HMAC secrets stay in env files on the host (`chmod 600`). They are not in unit files or this repo.

## chainwatch alerts

| Alert | Meaning | What to do |
|---|---|---|
| `tip_stalled` | The highest height among synced nodes has not moved for `--stall-after` (default 3m), or every watched RPC is down or catching up. `reason` is `height_unchanged` or `no_synced_node`. | See who signed the last block before restarting anyone. With four validators the chain halts when two are down, or when one that holds a third or more goes down. |
| `tip_resumed` | The tip moved again. | None, unless a node is still in `node_behind`. |
| `node_behind` | That node reports `catching_up`, or its height is more than `--behind-blocks` (default 10) behind the tip. | Leave it. It is not a reliable signer yet. A planned restart does this for a few blocks; `node_synced` follows. |
| `node_synced` | A node that was behind has caught up. | Safe to look at the next node. |
| `rpc_unreachable` | That RPC failed `--unreachable-after` polls in a row (default 3). | The process or the network path is down. `signing_low` from minerwatch is the matching signal when the node is a validator whose votes stop landing. |
| `rpc_recovered` | The RPC answers again. | Check `catching_up` and that it signs. |
| `validator_unsafe_to_stop` | Stopping this validator leaves the others at or under 2/3. | Do not restart it. Restart a validator the report marks safe, or wait until power moves at an epoch boundary. |
| `validator_safe_to_stop` | That validator dropped back to a share the others can cover, or it left the set. | A restart of that node can commit without it. |

`tip_stalled` on a chainwatch that only polls the local RPC cannot tell "this node is behind" from "the chain halted". Point it at two RPCs (local, plus another validator, or the public seed RPC from a second host) so a stuck local node becomes `node_behind` while the other height still moves.

A chainwatch on the seed dies with the seed. A second copy on another host, watching `http://127.0.0.1:26657` and the seed's public RPC (`https://rpc.157-245-252-221.sslip.io`) with `--tip-from` set to the seed, reports the seed going quiet.

## Deploy chainwatch

On a workstation, from a clean checkout, with `go version` printing `go1.26.10`:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o dist/chainwatch ./cmd/chainwatch
sha256sum dist/chainwatch
scp dist/chainwatch root@<seed>:/root/chainwatch
```

`scripts/prebuild.sh` also puts `chainwatch` in `dist/prebuilt-<commit>/` with `SHA256SUMS`. Do not run either build on a validator.

On the seed:

```bash
install -m 600 /dev/null /root/chainwatch.env
# edit /root/chainwatch.env from scripts/ops/chainwatch.env.example
# CHAINWATCH_WEBHOOK and/or CHAINWATCH_DISCORD_WEBHOOK, and CHAINWATCH_SECRET
install -m 644 scripts/ops/aether-chainwatch.service /etc/systemd/system/aether-chainwatch.service
# add --node lines for every RPC this host can actually dial
systemctl daemon-reload
systemctl enable --now aether-chainwatch
journalctl -u aether-chainwatch -n 20 --no-pager
```

The unit's `EnvironmentFile=-/root/chainwatch.env` supplies the webhook variables. Add a `--node name=http://host:26657` for each RPC. Names are what the alerts say. Confirm a test event by running one poll:

```bash
/root/chainwatch --once --node seed=http://127.0.0.1:26657 --state /root/chainwatch.state
```

`--state` is what stops a restart from repeating an alert that is still true.

### Safe to stop

Before any validator restart:

```bash
/root/chainwatch --rpc http://127.0.0.1:26657 --safe-to-stop
/root/chainwatch --rpc http://127.0.0.1:26657 --safe-to-stop --validator B7F08625
```

Exit 0 means every validator (or the named one) can go offline and the others still hold more than 2/3. Exit 2 means a restart of that validator halts the chain. Exit 1 means the RPC could not be read, or the address is not in the set. `--json` prints the same report for scripts. Consensus addresses are the 40 hex digits from `/status` (`validator_info.address`); an 8-digit prefix works when only one validator matches. The live prefixes operators use are seed `F94934BD`, sync3 `CCA35D28`, sync4 `BCEF78B5`, peer-1 `B7F08625`.

## Cut over a binary

One new `aetherd`, built once, then each validator restarted only after the previous one is signing again. The scripts default to dry-run. `--apply` / `-Apply` is what restarts a node.

### 1. Build, on a workstation

```bash
bash scripts/ops/build-aetherd.sh --out dist/aetherd-cutover
```

This refuses to run if `aetherd` is an active systemd unit or process. It requires Go `go1.26.10`, and writes `aetherd`, `aetherd.exe`, `SHA256SUMS` and `GO_VERSION`.

### 2. Linux nodes

Copy `scripts/ops/nodes.example` to a file that is not committed. Fill `name ssh rpc validator [unit]` in restart order. `ssh` is `root@host` (the swap runs `systemctl` as root). `validator` is that node's 40-hex consensus address. `rpc` is the RPC as seen **on that host** (`http://127.0.0.1:26657`). The host needs `curl` and `jq`. A sensible order when every validator is under a third is sync3, sync4, peer-1 (Windows, next section), seed.

Dry-run (read-only checks run for real):

```bash
bash scripts/ops/cutover.sh \
  --nodes nodes.txt \
  --activation-height HEIGHT \
  --prebuilt dist/aetherd-cutover \
  --chain-id aether-testnet-1
```

Preflight on every node, before any restart:

- the new binary's sha256 matches `SHA256SUMS`, and `GO_VERSION` is `go1.26.10`
- the node is synced (`catching_up` false) and has at least `--min-peers` (1)
- it signed the latest block
- the latest commit carries strictly more than 2/3 of the voting power
- stopping **this** validator leaves the others strictly more than 2/3

`--apply` repeats that check, copies the binary, and on the node runs `scripts/ops/remote-swap.sh`, which checks again and only then stops the unit, installs over the path in the unit's `ExecStart`, and starts it. The previous binary is kept at that path plus `.pre-cutover` (override with `--backup-suffix`). An existing backup is not overwritten. The script waits until the height has moved, `catching_up` is false, this validator signed, and the commit still has more than 2/3. Then it checks the running binary's sha256. It does not touch the next node until that passes.

A node already running that sha256 is skipped. The running binary is found through the unit (`systemctl show -p MainPID`), not `pidof`: the binaries are named `aetherd-<commit>`, which `pidof aetherd` doesn't match. `--only NAME` limits the run to one node. `--allow-down` is for a process that is already stopped: it skips the signing, peer, and safe-to-stop checks. It is not a way past an UNSAFE line while the node is still up.

**A validator that holds a third or more** (peer-1 at about 41% today) is UNSAFE to stop: while it restarts the others hold 2/3 or less and no block commits. Past the seat hardening it isn't removed for downtime, so blocks resume as soon as it is back. To swap it anyway, run that one node with `--only NAME --accept-halt` (`-AcceptHalt` on peer-1). That accepts blocks stopping for the restart, and only that: the node must still be synced, have peers and have signed the latest block, and the commit must still be over 2/3. Do it with every other validator signing, and have the new binary ready to start at once.

### 3. peer-1 (Windows)

Elevated PowerShell on DardenPC. The task "Aether Peer-1" runs `peer1-ensure.ps1`, which names the exe. RPC is `http://127.0.0.1:26667`.

```powershell
.\scripts\windows\cutover-peer1.ps1 `
  -Binary C:\aether-data\aetherd-<commit>.exe `
  -Sha256 <hex from SHA256SUMS> `
  -ActivationHeight HEIGHT
# then the same command with -Apply
```

Dry-run prints the plan and still reads the RPC. `-Apply` checks the same 2/3 conditions, copies the exe, backs up `peer1-ensure.ps1` to `peer1-ensure.ps1.pre-cutover` (it does not overwrite an existing backup), points `$exe` at the new file, stops whatever is listening on 26667, and starts the task. The old exe stays on disk.

### 4. After the last node

- `chainwatch --safe-to-stop` and a block whose commit includes every validator you care about
- `sha256sum` of the running Linux binary (`sha256sum /proc/$(systemctl show -p MainPID --value aetherd)/exe`; `pidof aetherd` finds nothing when the file is `aetherd-<commit>`), and `Get-FileHash` of the peer-1 process path
- the activation height, and the height when the last node finished

## Roll back, only before activation

`--activation-height` is the first block the new binary must agree on. Below that height the previous binary still matches the chain. At and after it, an old binary can diverge, so the scripts refuse to put it back.

Linux, one node:

```bash
bash scripts/ops/rollback.sh --nodes nodes.txt --name sync3 --activation-height HEIGHT
bash scripts/ops/rollback.sh --apply --nodes nodes.txt --name sync3 --activation-height HEIGHT
```

That restores `ExecStart`'s path plus the backup suffix and restarts the unit. The height is the highest any node in `nodes.txt` reports, since the node being rolled back is usually the one whose RPC is down; with no node answering, it refuses. If `remote-swap.sh` fails to start the new process **and** the height it read is still below activation, it restores the backup itself and does not continue to the next node.

peer-1:

```powershell
.\scripts\windows\cutover-peer1.ps1 -Rollback -ActivationHeight HEIGHT -HeightRpc <another node's RPC>
.\scripts\windows\cutover-peer1.ps1 -Apply -Rollback -ActivationHeight HEIGHT -HeightRpc <another node's RPC>
```

`-HeightRpc` (one or more URLs) gives the height when peer-1's own RPC doesn't answer; the highest height read decides. That puts the backed-up `peer1-ensure.ps1` back and restarts the task. If `-Apply` starts the new exe and it does not sign before the wait ends, the script restores that backup on its own only while the height from before the swap is still below activation.

Above the activation height, leave the new binary in place and tell the operator. Do not start a second `aetherd` for the same validator key.
