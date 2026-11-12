# Key change retires old delegations: on at block 786,960

**H = 786,960** (`KeyChangeRevokesHeight` in `x/accountauth/keygen.go`),
proposed 2026-11-12 at a tip of about 723,400. At 5.5 s blocks it is about
**14:00 UTC on Monday 2026-11-16 (8:00 AM CST)**; every 0.1 s of block time
moves that by about 2 hours. It is 720 blocks from each seat boundary.
**H − 600 = 786,360** is the abort line.

**For:** the five validators (seed, sync3, sync4, Sydney, peer-1), plus
any other node running `aetherd`.

**Private until H has passed:** this fixes GHSA-cgcr-f6g2-ch5v
([REVIEW.md](REVIEW.md) F8, [PQ-KEY-ROTATION.md](PQ-KEY-ROTATION.md) "What
a key change retires"). Build from the advisory's private fork, keep the
binary and this note off public channels, and say only "a consensus fix"
until the advisory is published.

## What switches on at H

From H every account's key change (a rotation applying, a guardian
recovery) moves it to its next key generation, and an authenticator,
authz grant or fee allowance made under an earlier key is refused when
used. Grants, allowances and authenticator registrations also record a
generation, so they use a little more gas from H.

## How this upgrade works

**It adds no store, so no node halts at H and nobody restarts there.**

**An old binary agrees until the first transaction after H that the new
rules treat differently:** any authz grant, fee allowance or authenticator
registration (different gas), or any use of one made under an earlier key.
So **all five must run the new binary before H.**

## 1. Build once, on a workstation (never on a validator), with Go 1.26.10

From the private fork's `fix` branch:

```bash
git fetch origin && git checkout origin/fix
grep -n "KeyChangeRevokesHeight int64 = " x/accountauth/keygen.go          # 786_960
grep -n "AuxPoWInputGuardHeight int64 = 712_080" x/pow/auxpow.go          # still there
grep -n "RotationActivationHeight int64 = 518_940" x/accountauth/rotation.go
go version                              # go1.26.10
go test ./x/accountauth/... ./app
bash scripts/prebuild.sh                # dist/prebuilt-<commit>/, with SHA256SUMS
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o dist/aetherd-$(git rev-parse --short HEAD).exe ./cmd/aetherd
sha256sum dist/prebuilt-*/aetherd dist/aetherd-*.exe
git log -1 --format='%h %s'
```

Copy the Linux `aetherd` to sync3, sync4, Sydney and the seed, and stage
the `.exe` on the seed for Joshua with its sha256.

## 2. Swap, one node at a time, before the abort line

**First, all five must be signing.** Restart a validator only while the
others sign. Order: **sync3, sync4, Sydney, seed** (the seed after
Joshua's OK), with peer-1 (step 3) any time in between. For each, as in
[UPGRADE-INPUT-GUARDS.md](UPGRADE-INPUT-GUARDS.md) step 2:

```bash
curl -s localhost:26657/block | jq -r '.result.block.last_commit.signatures[] | select(.block_id_flag==2) | .validator_address'   # five
BIN=$(systemctl show -p ExecStart aetherd | grep -o '/[^ ;]*aetherd[^ ;]*' | head -1)
sha256sum "$BIN"
sudo cp "$BIN" "$BIN.pre-keychange"
sudo install -m 0755 <prebuilt folder>/aetherd "$BIN.new" && sudo mv "$BIN.new" "$BIN"
sudo systemctl restart aetherd         # if it hangs: sudo systemctl kill -s KILL aetherd && sudo systemctl start aetherd
sha256sum /proc/$(systemctl show -p MainPID --value aetherd)/exe   # matches step 1
MYADDR=$(curl -s localhost:26657/status | jq -r .result.validator_info.address)
curl -s localhost:26657/block | jq -r '.result.block.last_commit.signatures[].validator_address' | grep -c "$MYADDR"   # 1, before the next node
```

## 3. peer-1 (Joshua, on DardenPC, elevated PowerShell)

```powershell
Get-FileHash C:\aether-data\aetherd-<commit>.exe     # must match Gitty's sum
Copy-Item C:\aether-data\peer1-ensure.ps1 C:\aether-data\peer1-ensure.ps1.pre-keychange
```

1. In `C:\aether-data\peer1-ensure.ps1`, change the binary to
   `aetherd-<commit>.exe`. Keep the old `.exe`.
2. Stop the process on port 26667 and start the task:
   ```powershell
   Get-NetTCPConnection -LocalPort 26667 -State Listen | ForEach-Object { Stop-Process -Id $_.OwningProcess }
   Start-ScheduledTask -TaskName "Aether Peer-1"
   ```
3. Check it: the running binary's hash, and `catching_up` false on
   `http://127.0.0.1:26667/status`.

Every `aetherd` command on DardenPC takes `--home C:\aether-peer1`.

**Abort rule.** If all five aren't on the new binary by **786,360**, put
every swapped node back on `.pre-keychange`, tell Joshua, and H moves.

## 4. After H

- All five signed 786,950 to 786,970.
- Joshua merges the fork's pull request from the advisory page and
  publishes the advisory; then the release (`v0.2.22-testnet`) and the
  public notes.
