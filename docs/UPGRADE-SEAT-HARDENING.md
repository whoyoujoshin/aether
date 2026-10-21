# Seat hardening upgrade: on at block 460,800

**For:** the four validators (seed, sync3, sync4, peer-1), plus any other
node running `aetherd`. **Deadline:** every node on the new binary before
block **460,800**. That is about 08:15 UTC on 26 October 2026 at 5.6-second
blocks, or about 15:00 UTC at 5.9 seconds (the tip was 380,879 at 03:38 UTC
on 21 October). Check the tip and the time left before each step.

**Progress (2026-10-21).**
- **Build:** Gitty built `3ad12a6` (the merge of #159) on their workstation
  with Go 1.26.10. Both height greps match and `go test ./x/pow/...` passes.
  - Linux `aetherd` (sync3, sync4, seed): sha256
    `9a78e03acc057be37c45c762881ddc4443555bbe2d8f106833319f2b83897123`.
  - peer-1 `aetherd-3ad12a6.exe`: sha256
    `8d0c26e79af684c96372253e5f392e5c07922205130af12ba0725cb00827e5f1`. It is
    over the chat attachment limit, so it is staged on the seed for Joshua
    to download, as `3988485`'s was.
- **Swaps:** not started; they wait for the 397,439 check. At 5.64-second
  blocks (tip 386,178 at 11:55 UTC on 21 October), 397,439 is about 05:30
  UTC on 22 October, 460,200 about 07:50 UTC and 460,800 about 08:50 UTC on
  26 October.

**When to swap.** After the seat rule's first boundary (the end of
397,439, about 05:30 UTC on 22 October) has been checked and all four are
signing, so only one change is in flight at a time. The new binary carries
the seat rule too, so a node swapped earlier is also fine.

**Abort rule.** If all four aren't on the new binary by block **460,200**,
put every node already swapped back on the binary it ran before
(`3988485`), and tell Joshua. Nothing changes before 460,800, so
`3988485` still agrees with everyone.

## What switches on at 460,800

`SeatHardeningHeight` (`x/pow/seat_hardening.go`, `docs/SEAT-RULE.md`).
Four fixes for the weaknesses `docs/SEAT-SIM.md` found in the seat rule:

- **The 1% bar counts eligible work only.** Work credited to an address
  with no consensus key, or a banned one, no longer counts toward the
  total. All four miners are registered, so nothing visible changes today.
- **A longer liveness window: 1,440 blocks instead of 60.** A validator is
  removed for downtime only after missing more than 720 of the last 1,440
  blocks, about 68 minutes. Windows kept under the old size start over at
  the height.
- **A cap on downtime removals:** at most 10% of the power between two
  boundaries. With four validators at about a quarter each, **none is
  removed for downtime.** A validator that goes offline stays in the set,
  and the rounds it would propose time out, so blocks get slower while
  it's down instead of the validator dropping out. Validators holding
  more than a third of the power down still halt the chain, as before.
- **Reserved power on return:** a validator that was removed for downtime
  and whose miner adds work afterwards gets its power back at the next
  boundary.

A node without the binary falls off the chain at block 460,800 itself:
every block records which validators signed, and from that height the
record has a different size. The first epoch boundary under the hardening
is the end of block **462,239**. No miner changes are needed: `powminer`
is unaffected.

## 1. Build once, on a workstation (never on a validator), with Go 1.26.10

From the commit that merged the height (`main`):

```bash
git fetch origin && git checkout origin/main
grep -n "SeatHardeningHeight int64 = 460_800" x/pow/seat_hardening.go
#   must show: a binary without it isn't the upgrade
grep -n "SeatRuleActivationHeight int64 = 396_000" x/pow/seats.go
#   must show too: the seat rule stays
go version                             # go1.26.10
go test ./x/pow/...
bash scripts/prebuild.sh               # dist/prebuilt-<commit>/aetherd, with SHA256SUMS
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o dist/aetherd-$(git rev-parse --short HEAD).exe ./cmd/aetherd
sha256sum dist/prebuilt-*/aetherd dist/aetherd-*.exe
git log -1 --format='%h %s'
```

Copy the Linux `aetherd` to sync3, sync4 and the seed. Send Joshua the
`.exe` and its sha256 for peer-1. The build uses Go 1.26 and the running
`3988485` binaries use Go 1.25.8; that mix is fine (a devnet crossed three
boundaries with both, with no app-hash errors).

## 2. Swap, one node at a time

Order: **sync3, sync4, seed**, with peer-1 (step 3) any time in between.
For each node:

**a. Before touching it,** check that four validators signed the latest
block:

```bash
curl -s localhost:26657/block | jq -r '.result.block.last_commit.signatures[] | select(.block_id_flag==2) | .validator_address'
```

**b. Swap the binary:**

```bash
BIN=$(systemctl show -p ExecStart aetherd | grep -o '/[^ ;]*aetherd[^ ;]*' | head -1)
echo "$BIN"                            # the unit's binary, e.g. /root/aetherd-2c32e6a
sha256sum "$BIN"                       # a2acd59d…f4ea9e: the 3988485 build
sudo cp "$BIN" "$BIN.pre-hardening"
sudo systemctl stop aetherd
sudo install -m 0755 <prebuilt folder>/aetherd "$BIN"
sudo systemctl start aetherd
sha256sum /proc/$(pidof aetherd)/exe   # matches step 1
```

The file names still carry old commits (the seed's is
`/root/aetherd-5267ab3`), so go by the hash, never the path.

**c. Wait until it's back and signing** before the next node:

```bash
curl -s localhost:26657/status | jq '.result.sync_info | {latest_block_height, catching_up}'
MYADDR=$(curl -s localhost:26657/status | jq -r .result.validator_info.address)
curl -s localhost:26657/block | jq -r '.result.block.last_commit.signatures[].validator_address' | grep -c "$MYADDR"
#   1 = this validator signed the latest block
```

## 3. peer-1 (Joshua, on DardenPC, elevated PowerShell)

The scheduled task runs with highest privileges, so use an elevated
window.

```powershell
Get-FileHash C:\aether-data\aetherd-<commit>.exe     # must match Gitty's sum
```

1. In `C:\aether-data\peer1-ensure.ps1`, change the binary name from
   `aetherd-3988485.exe` to `aetherd-<commit>.exe`. Keep
   `aetherd-3988485.exe` in the folder for the abort rule.
2. Stop the running `aetherd` that holds port 26667, then start the task,
   which starts the binary the script names:
   ```powershell
   Get-NetTCPConnection -LocalPort 26667 -State Listen | ForEach-Object { Stop-Process -Id $_.OwningProcess }
   Start-ScheduledTask -TaskName "Aether Peer-1"
   ```
3. Check it runs the new file:
   ```powershell
   $p = (Get-NetTCPConnection -LocalPort 26667 -State Listen | Select-Object -First 1).OwningProcess
   (Get-Process -Id $p).Path                                   # C:\aether-data\aetherd-<commit>.exe
   Get-FileHash (Get-Process -Id $p).Path                      # Gitty's sum
   (Invoke-RestMethod http://127.0.0.1:26667/status).result.sync_info.catching_up   # False
   (Invoke-RestMethod http://127.0.0.1:26667/block).result.block.last_commit.signatures |
     Where-Object block_id_flag -eq 2 | ForEach-Object validator_address        # includes B7F08625...
   ```

Keep the VPN off.

## 4. Done

- All four validators signing (step 2a shows four).
- Every Linux node reports the same `sha256sum`, and peer-1 the `.exe`'s.
- Send Joshua the commit, the height when the last node finished, and
  peer-1's running path and hash.

**Optional, any time:** the explorer, the MCP servers and the test wallet
report miner status, which measures the 1% bar the way the chain does
from 460,800. Redeploying them from the same prebuild (`PREBUILT=…` with
`deploy-explorer.sh`, `install.sh` and `sandbox.sh`) keeps that report
exact. It only differs if unregistered work appears, and it's not
consensus, so it can wait.

## 5. Watch 460,800 and the first boundary (462,239)

```bash
curl -s localhost:26657/validators | jq -r '.result.validators[] | "\(.address) \(.voting_power)"'
```

- **At 460,800:** four keep signing, and powers don't change (they move
  only at boundaries).
- **At the end of 462,239:** powers move toward each miner's share of the
  work as at every boundary since 397,439. Every validator should still
  clear 1% of the window's work.
- **A validator that goes offline from now on** isn't removed. Blocks
  slow down while it's out (its proposer rounds time out), so a run of
  slow blocks with one validator missing from step 2a's list is the sign.
  Bring it back as usual.

## If something goes wrong

- **A node won't start on the new binary** (before 460,800): put the old
  one back and start it, then send Joshua the log:
  ```bash
  sudo systemctl stop aetherd
  sudo install -m 0755 "$BIN.pre-hardening" "$BIN"
  sudo systemctl start aetherd
  journalctl -u aetherd -n 100 --no-pager
  ```
- **The chain stalls during the swaps:** two validators are down. Bring
  back whichever restarted last, on either binary.
- **After 460,800:** don't roll back. An old binary disagrees from that
  block on. Tell Joshua instead.
