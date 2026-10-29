# Inflow cap and fees-to-treasury upgrade: on at block 511,920

**H = 511,920** (Linear AET-6, chosen by Joshua on 2026-10-28 at tip
~488,500). It is the middle of epoch 355, 720 blocks from the boundaries
at the ends of 511,199 and 512,639. At 5.64 s blocks it is about 16:40
UTC (11:40 AM CDT) on Thursday 2026-10-29; block times of 5.6 to 5.9 s
move that by about an hour and a half either way. `InflowCapActivationHeight`
(`app/inflowcap.go`), `FeesToTreasuryActivationHeight` (`app/fees.go`)
and `TreasuryCoinSpendActivationHeight` (`x/governance/types.go`) are all
511,920; earlier binaries carried 30,000,000. Every `H` below means
511,920; **H − 600 = 511,320** is the abort line.

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
  event. Uaeth sent straight to the treasury's address, which its ledger
  never saw (1.000001 AETH on 10-28, so `ledger_matches_bank_balance`
  reads false), is credited at H with a `treasury_ledger_credited` event,
  so governance can spend it; the ledger is only ever raised.
- **Treasury spends in any denom** (`x/governance`,
  `TreasuryCoinSpendActivationHeight`). A treasury-spend proposal's amount
  may be one coin, e.g. `1000000ibc/064D…` for 1 USDC (CLI:
  `submit-proposal <recipient> 1000000 <deposit> --denom ibc/064D…`), paid
  from the treasury's bank balance; a plain integer is still uaeth through
  the ledger. Before H a coin amount is refused at submission, as always.

**It adds a store, so every node halts at H and needs one restart there**
(the same mechanism as x/escrow at 161,000, `docs/CUTOVER-161000.md`).
A node on the new binary runs exactly as before until then. A node still
on the old binary at H splits from the others.

## Before H: the restart needs more than two-thirds of the power

Blocks resume after H only once validators holding **more than 2/3 of
the voting power** have restarted. At 472,590 peer-1 held 42.3%, so
**the chain can't resume at H without peer-1**: the three Linux nodes
together had 57.7%. Either:

- **pick H for a time Joshua is at DardenPC**, or
- **run `powminer --duty 0.6` on DardenPC first** (AET-11) and wait until
  peer-1 is under a third. Then sync3, sync4 and the seed restart the
  chain on their own and peer-1 catches up after.

Both hold for 511,920: peer-1's miner has run at `--duty 0.6` since
2026-10-28 03:55 UTC (488,451; peer-1 44.1% at 488,464 with 34.5% of
epoch 339's work), so it should be under a third about three boundaries
later (~492,479), and H is in Joshua's day. At 502,941 (all four on
`1080a98`) peer-1 held 31.1% with a 27.4% work share, and blocks were
5.82 s, which puts H near 17:15 UTC (12:15 PM CDT). Check the powers
again the evening before:

```bash
curl -s localhost:26657/validators | jq -r '.result.validators[] | "\(.address) \(.voting_power)"'
```

H had to be at least a day out (a build, four swaps and a margin), and not
within ~100 blocks of a seat boundary (the end of `n × 1440 − 1`), so a
boundary and the halt don't land together. 511,920 is 720 from each.

## 1. Build once, on a workstation (never on a validator), with Go 1.26.10

From the commit that merged H (`main`):

```bash
git fetch origin && git checkout origin/main
grep -n "InflowCapActivationHeight int64 = " app/inflowcap.go      # must show 511_920
grep -n "FeesToTreasuryActivationHeight int64 = " app/fees.go     # must show 511_920
grep -n "TreasuryCoinSpendActivationHeight int64 = " x/governance/types.go   # must show 511_920
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
If the query node runs (`docs/QUERY-NODE.md`), swap it too, any time
before H: it has no voting power, so it needs no signing check, only the
same binary (its unit is `aether-query`). For each validator:

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

**While the swaps are mixed, send no inflow cap messages.** The new
binary knows `x/inflowcap`'s message types (`MsgUpdateParams`, directly or
inside a param-change proposal) and the old one doesn't: a node on the
old binary can't decode such a transaction, one on the new binary decodes
it and charges its fee, and the two disagree on the block. Nobody needs to
send one before H (the testnet list is set by H itself); keep it that way
until all four run the new binary, and do the swaps in one sitting.

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
sudo systemctl restart aether-query  # the query node, if it runs
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
- `get_inflow_caps` reports `active: true`. The seed's agentmcp and test
  wallet are from `561fc6c` (10-20), older than the tool, so the public
  `/mcp` doesn't have it until they're redeployed after H from the
  `dist/prebuilt-1080a98` that built `aetherd`: its `agentmcp` over
  `/root/agentmcp` (backup first) and `systemctl restart aether-agentmcp`,
  and `PREBUILT=… sandbox.sh` for the test wallet; aetherd untouched.
  Until then, `aetherd q inflowcap caps` is the check.
- A small USDC transfer from Injective arrives; one over the room is
  refused `over_cap` and refunded on Injective after `outbound.exe`
  relays the error ack.
- `fees_to_treasury` events appear in blocks that carried fees, the H
  block has a `treasury_ledger_credited` event, and the treasury's
  `Balance` query reads `ledger_matches_bank_balance: true`.

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
  acknowledgement or timeout keeps failing until a fixed binary ships.
  Helicase sends each relay message as its own transaction, so the rest
  of the path keeps moving; the failing one is proposed again every ~10
  blocks. Tell Joshua and Claude with the tx.
- **After H:** don't roll back. Tell Joshua instead.
