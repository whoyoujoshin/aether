# Seat rule upgrade: on at block 396,000

**For:** the four validators (seed, sync3, sync4, peer-1), plus any other
node running `aetherd`. **Deadline:** every node on the new binary before
block **396,000**. That is about 03:30 UTC on 22 October 2026 at 5.6-second
blocks (the tip was 349,354 at 02:16 UTC on 19 October). Check the tip and
the time left before each step.

**Progress (2026-10-19).**
- **Linux nodes:** sync3, sync4 and the seed have run `3988485` since 03:16
  UTC (tip 349,999). The Linux `aetherd` sha256 is `a2acd59d…f4ea9e`.
- **peer-1:** its `.exe` (sha256 `697e6398…c793d`) is staged on the seed at
  `/root/aetherd-3988485.exe`. peer-1 is the one left.

**Abort rule.** If all four aren't on the new binary by block **395,400**,
put every node already swapped back on the binary it ran before, and tell
Joshua. Nothing has activated before 396,000, so the old binaries still
agree with everyone.

## What switches on at 396,000

`SeatRuleActivationHeight` (`x/pow/seats.go`, `docs/SEAT-RULE.md`):

- **Seat work is hashes proved.** Each accepted submission, native or
  merged, adds the difficulty it met. Merged work is credited to the
  reward address in its proof.
- **Power follows seat work** over a three-epoch window. An address needs
  1% of the window's work to qualify, and the top 21 are seated.
- **Power moves at most 10% of the total per epoch.**
- **Bond floor:** a validator's escrow fills to 1,000 AETH, and rewards
  beyond that are paid straight to its miner address.

A node without the binary falls off the chain at the first submission
accepted at or after 396,000, because seat work and bond-floor payouts
change state from then. The first epoch boundary under the rule is the end
of block **397,439**. No miner changes are needed: `powminer` is
unaffected.

## 1. Build once, on a workstation (never on a validator)

From the commit that merged the height (`main`):

```bash
git fetch origin && git checkout origin/main
grep -n "SeatRuleActivationHeight int64 = 396_000" x/pow/seats.go
#   must show: a binary without it isn't the upgrade
bash scripts/prebuild.sh               # dist/prebuilt-<commit>/aetherd, with SHA256SUMS
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o dist/aetherd-$(git rev-parse --short HEAD).exe ./cmd/aetherd
sha256sum dist/prebuilt-*/aetherd dist/aetherd-*.exe
git log -1 --format='%h %s'
```

Copy the Linux `aetherd` to sync3, sync4 and the seed. Send Joshua the
`.exe` and its sha256 for peer-1.

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
sudo cp "$BIN" "$BIN.pre-seatrule"
sudo systemctl stop aetherd
sudo install -m 0755 <prebuilt folder>/aetherd "$BIN"
sudo systemctl start aetherd
sha256sum "$BIN"                       # matches step 1
```

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

1. In `C:\aether-data\peer1-ensure.ps1`, change the binary name to
   `aetherd-<commit>.exe`.
2. Stop the running `aetherd` that holds port 26667, then start the task,
   which starts the binary the script names:
   ```powershell
   Get-NetTCPConnection -LocalPort 26667 -State Listen | ForEach-Object { Stop-Process -Id $_.OwningProcess }
   Start-ScheduledTask -TaskName "Aether Peer-1"
   ```
3. Check it runs the new file. `version --long` prints an empty version
   and commit (the build sets no ldflags), so go by the file and its hash:
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
  Check by hash: `aetherd version --long` prints no version or commit.
- Send Joshua the commit, the height when the last node finished, and
  peer-1's running path and hash.

## 5. Watch the first boundary (397,439)

```bash
curl -s localhost:26657/validators | jq -r '.result.validators[] | "\(.address) \(.voting_power)"'
```

Before it, four validators at 1,000,000 each. After it, the total is
about 100,000,000. Each power has moved toward its share of the hashes
proved since 396,000, with at most 10 points of share moving in all, so
from 25% each it moves partway. Four keep signing.

- **A miner that was down for that epoch** loses about 10% of the power a
  boundary, instead of dropping out at once.
- **Rewards:** from 396,000, new rewards above a validator's 1,000 AETH of
  escrow go straight to its miner address (`aetherd q bank balances
  <miner>` grows). Escrow already above the floor (peer-1 held about
  27,246 AETH on 19 October) stays locked and is released as before when
  its unlock height passes.

## If something goes wrong

- **A node won't start on the new binary** (before 396,000): put the old
  one back and start it, then send Joshua the log:
  ```bash
  sudo systemctl stop aetherd
  sudo install -m 0755 "$BIN.pre-seatrule" "$BIN"
  sudo systemctl start aetherd
  journalctl -u aetherd -n 100 --no-pager
  ```
- **The chain stalls during the swaps:** two validators are down. Bring
  back whichever restarted last, on either binary.
- **After 396,000:** don't roll back. An old binary disagrees from the
  first submission at that height. Tell Joshua instead.
