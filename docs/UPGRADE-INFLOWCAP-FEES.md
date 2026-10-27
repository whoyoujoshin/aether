# Inflow cap and fees-to-treasury upgrade: on at block H

**Draft.** `H` isn't chosen yet (Linear AET-6). Both
`InflowCapActivationHeight` (`app/inflowcap.go`) and
`FeesToTreasuryActivationHeight` (`app/fees.go`) are the 30,000,000
placeholder until a PR sets them to `H`. Every `H` below means that number.

**For:** the four validators (seed, sync3, sync4, peer-1), plus any other
node running `aetherd`. **Deadline:** every node on the new binary before
block **H − 1** is committed.

## What switches on at H

- **x/inflowcap** (`docs/IBC-INFLOW-CAP.md`). A token from another chain
  comes in only if it is listed for its channel, and only up to its cap;
  anything else gets an error acknowledgement and is refunded on the
  sending chain. On the testnet, H sets the list once
  (`testnetInflowEntries`): **USDC over `channel-2` capped at 100 USDC**
  and **OSMO over `channel-3` at 1,000 OSMO**. Every other voucher (INJ,
  the old Osmosis `channel-1`) is refused from H. AETH and Aether's own
  vouchers coming home are never capped. Governance changes the list
  afterwards (`aetherd tx inflowcap draft-update-params`).
- **Fees to the treasury** (`app/fees.go`). At the end of every block
  from H, everything in `fee_collector` (every denom) moves to the
  treasury, the uaeth credited to its ledger, with a `fees_to_treasury`
  event.

**It adds a store, so every node halts at H and needs one restart there**
(the same mechanism as x/escrow at 161,000, `docs/CUTOVER-161000.md`).
A node on the new binary runs exactly as before until then. A node still
on the old binary at H splits from the others.

## Before you pick H: the restart needs more than two-thirds of the power

Blocks resume after H only once validators holding **more than 2/3 of
the voting power** have restarted. At 472,590 peer-1 held 42.3%, so
**the chain can't resume at H without peer-1**: the three Linux nodes
together had 57.7%. Either:

- **pick H for a time Joshua is at DardenPC**, or
- **run `powminer --duty 0.6` on DardenPC first** (AET-11) and wait until
  peer-1 is under a third. Then sync3, sync4 and the seed restart the
  chain on their own and peer-1 catches up after.

Check the powers before choosing:

```bash
curl -s localhost:26657/validators | jq -r '.result.validators[] | "\(.address) \(.voting_power)"'
```

Pick H at least a day out (a build, four swaps and a margin), and not
within ~100 blocks of a seat boundary (the end of `n × 1440 − 1`), so a
boundary and the halt don't land together.

## 1. Build once, on a workstation (never on a validator), with Go 1.26.10

From the commit that merged H (`main`):

```bash
git fetch origin && git checkout origin/main
grep -n "InflowCapActivationHeight int64 = " app/inflowcap.go      # must show H
grep -n "FeesToTreasuryActivationHeight int64 = " app/fees.go     # must show H
grep -n "SeatHardeningHeight int64 = 460_800" x/pow/seat_hardening.go
go version                             # go1.26.10
go test ./x/inflowcap/... ./app
bash scripts/prebuild.sh               # dist/prebuilt-<commit>/aetherd, with SHA256SUMS
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o dist/aetherd-$(git rev-parse --short HEAD).exe ./cmd/aetherd
sha256sum dist/prebuilt-*/aetherd dist/aetherd-*.exe
git log -1 --format='%h %s'
```

Copy the Linux `aetherd` to sync3, sync4 and the seed. Stage the `.exe` on
the seed for Joshua with its sha256.

## 2. Swap, one node at a time (well before H)

Order: **sync3, sync4, seed**, with peer-1 (step 3) any time in between.
For each node:

**a. Check four validators signed the latest block:**

```bash
curl -s localhost:26657/block | jq -r '.result.block.last_commit.signatures[] | select(.block_id_flag==2) | .validator_address'
```

**b. Back up the data and swap the binary:**

```bash
BIN=$(systemctl show -p ExecStart aetherd | grep -o '/[^ ;]*aetherd[^ ;]*' | head -1)
sha256sum "$BIN"                       # 9a78e03a…97123: the 3ad12a6 build
sudo cp "$BIN" "$BIN.pre-inflowcap"
sudo systemctl stop aetherd
sudo cp -a ~/.aether ~/.aether-backup-pre-inflowcap   # the node's real home; for a missed swap (below)
sudo install -m 0755 <prebuilt folder>/aetherd "$BIN"
sudo systemctl start aetherd
sha256sum /proc/$(systemctl show -p MainPID --value aetherd)/exe   # matches step 1
```

Check free disk first (`df -h ~`): the backup is the size of the data
directory. Skip it where space is short; it's insurance, not a step.

**c. Wait until it's back and signing** before the next node:

```bash
MYADDR=$(curl -s localhost:26657/status | jq -r .result.validator_info.address)
curl -s localhost:26657/block | jq -r '.result.block.last_commit.signatures[].validator_address' | grep -c "$MYADDR"   # 1
```

## 3. peer-1 (Joshua, on DardenPC, elevated PowerShell)

```powershell
Get-FileHash C:\aether-data\aetherd-<commit>.exe     # must match Gitty's sum
Copy-Item C:\aether-data\peer1-ensure.ps1 C:\aether-data\peer1-ensure.ps1.pre-inflowcap
```

1. In `C:\aether-data\peer1-ensure.ps1`, change the binary from
   `aetherd-3ad12a6.exe` to `aetherd-<commit>.exe`. Keep the old `.exe`.
2. Stop the process on port 26667 and start the task:
   ```powershell
   Get-NetTCPConnection -LocalPort 26667 -State Listen | ForEach-Object { Stop-Process -Id $_.OwningProcess }
   Start-ScheduledTask -TaskName "Aether Peer-1"
   ```
3. Check it:
   ```powershell
   $p = (Get-NetTCPConnection -LocalPort 26667 -State Listen | Select-Object -First 1).OwningProcess
   Get-FileHash (Get-Process -Id $p).Path                     # Gitty's sum
   (Invoke-RestMethod http://127.0.0.1:26667/status).result.sync_info.catching_up   # False
   ```

Every `aetherd` command on DardenPC takes `--home C:\aether-peer1`. Keep
the VPN off.

**Abort rule.** If all four aren't on the new binary by **H − 600**, put
every swapped node back on `.pre-inflowcap` (`aetherd-3ad12a6.exe` on
DardenPC) and tell Joshua; H moves in a new PR. Nothing changes before H,
so the old binary still agrees with everyone.

## 4. In the last ~50 blocks before H: nothing in flight on the listed channels

The cap counts vouchers sent back and not yet settled, but only sends
made from H on. A USDC or OSMO voucher sent back before H that then fails
is refunded on top of the cap once. Check, on any node:

```bash
aetherd q ibc channel packet-commitments transfer channel-2 -o json | jq '.commitments | length'   # 0
aetherd q ibc channel packet-commitments transfer channel-3 -o json | jq '.commitments | length'   # 0
```

If either isn't 0, a send is still out: it settles within its timeout
(Helicase brings the ack or the timeout). It's harmless unless it fails,
and then only by its own amount, once.

Also note the supplies the caps start against:

```bash
# on the seed, whose REST API is on (this app's CLI has no total-supply query; x/inflowcap's
# `caps` only answers from H on)
curl -s "localhost:1317/cosmos/bank/v1beta1/supply/by_denom?denom=ibc/064D82A67318DD30F54A4E17B6E487C2864E8B075A5871D3ACAD7A7129F32C5C" | jq -r .amount.amount   # USDC, cap 100000000
curl -s "localhost:1317/cosmos/bank/v1beta1/supply/by_denom?denom=ibc/47BD209179859CDE4A2806763D7189B6E6FE13A17880FE2B42DE1E6C1E329E23" | jq -r .amount.amount   # OSMO, cap 1000000000
```

A supply already over its cap isn't an error: nothing more of it comes
in until it falls back under.

## 5. At H: every node halts, restart each once

Each node stops on its own at H:

```
CONSENSUS FAILURE!!! ... x/inflowcap activates at height H: restart this node ...
```

Once a node shows that, restart it (order doesn't matter):

```bash
sudo systemctl restart aetherd       # sync3, sync4, seed
```

```powershell
# peer-1, elevated: stop the halted process, the task starts the same binary
Get-NetTCPConnection -LocalPort 26667 -State Listen | ForEach-Object { Stop-Process -Id $_.OwningProcess }
Start-ScheduledTask -TaskName "Aether Peer-1"
```

Blocks resume once more than two-thirds of the power is back (see
"Before you pick H"). If `systemctl restart` hangs, as it did on the seed
on 2026-10-22: `sudo systemctl kill -s KILL aetherd && sudo systemctl start aetherd`.

## 6. After

```bash
curl -s localhost:26657/status | jq -r .result.sync_info.latest_block_height   # moving past H
aetherd q inflowcap caps                 # the two entries, with supply, in_flight and room
aetherd q inflowcap params
aetherd q treasury balance -o json | jq .real_bank_balances
```

- All four signing (step 2a).
- `get_inflow_caps` on the public `/mcp` reports `active: true` (no
  redeploy needed: the services read the module when it answers).
- A small USDC transfer from Injective arrives; one over the room is
  refused `over_cap` and refunded on Injective after `outbound.exe`
  relays the error ack.
- `fees_to_treasury` events appear in blocks that carried fees.

Send Joshua the commit, the height each node restarted at, and peer-1's
running path and hash.

## If something goes wrong

- **A node won't start on the new binary (before H):** put
  `.pre-inflowcap` back, start it, send the log
  (`journalctl -u aetherd -n 100 --no-pager`).
- **A node was missed and ran H on the old binary:** it split from the
  others. Stop it, restore `~/.aether-backup-pre-inflowcap` **keeping its
  current `priv_validator_state.json`** (copy it aside first and put it
  back over the backup's), install the new binary, start it: it catches
  up, halts once at H, restart it. `aetherd rollback` undoes only one
  height, so it's not enough if the node went further.
- **The chain doesn't resume after H:** count who restarted against the
  powers above; more than two-thirds must be back on the new binary.
- **A settlement fails with `ErrSettlementGrew` (inflowcap code 6):** a
  refund tried to mint more than its send burned. That packet's
  acknowledgement or timeout keeps failing until a fixed binary ships;
  nothing else is affected. Tell Joshua and Claude with the tx.
- **After H:** don't roll back. Tell Joshua instead.
