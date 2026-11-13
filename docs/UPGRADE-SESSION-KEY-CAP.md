# Session keys' spend limit holds: on at block 812,880

**H = 812,880** (`SessionKeyCapHeight` in `x/accountauth/session_cap.go`),
chosen 2026-11-13 at a tip of 733,035. At 5.66 s blocks it is about
**10:20 UTC on Wednesday 2026-11-18 (4:20 AM CST)**; every 0.1 s of block
time moves that by about 2 hours. It is 720 blocks from each seat
boundary. **H − 600 = 812,280** is the abort line.

**For:** the five validators (seed, sync3, sync4, Sydney, peer-1), plus
any other node running `aetherd`.

**Private until H has passed:** this fixes GHSA-j2qr-v7fr-w36f
([REVIEW.md](REVIEW.md) F9, [ACCOUNT_ABSTRACTION.md](ACCOUNT_ABSTRACTION.md)
"How authorization is checked"). Build from the advisory's private fork,
keep the binary and this note off public channels, and say only "a
consensus fix" until the advisory is published.

**After 786,960:** this build also carries the key-change fix
([UPGRADE-KEY-CHANGE.md](UPGRADE-KEY-CHANGE.md)), with its height
unchanged. Swap only once 786,960 has passed and its checks are done, so
the two upgrades don't overlap.

## What switches on at H

- A session key's spend is what leaves the account: each message it
  runs is charged the fall in the account's uaeth balance, whatever the
  message. A message that lowers any other balance (USDC) is refused, and
  an exec that would pass the limit fails as a whole.
- A session key never runs, and can't be registered allowing, a message
  that hands the account's authority on or moves value already out of its
  balance: authz `MsgGrant` and `MsgExec`, a fee allowance, registering,
  revoking or running another authenticator, rotation and recovery,
  `MsgRegisterValidatorPubkey`, interchain accounts, `MsgReleaseEscrow`.
- Session-key execs read the account's balances before and after each
  message, so they use a little more gas from H.

## How this upgrade works

**It adds no store, so no node halts at H and nobody restarts there.**

**An old binary agrees until the first transaction after H that the new
rules treat differently:** any `MsgExecAuthenticated` through a session
key (different gas, and refusals), or a session-key registration allowing
one of the messages above. So **all five must run the new binary before
H.**

## 1. Build once, on a workstation (never on a validator), with Go 1.26.10

From the GHSA-j2qr private fork's `fix` branch:

```bash
git fetch origin && git checkout origin/fix
grep -n "SessionKeyCapHeight int64 = " x/accountauth/session_cap.go       # 812_880
grep -n "KeyChangeRevokesHeight int64 = " x/accountauth/keygen.go         # 786_960
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
[UPGRADE-KEY-CHANGE.md](UPGRADE-KEY-CHANGE.md) step 2, with the backup
named `.pre-sessioncap`:

```bash
curl -s localhost:26657/block | jq -r '.result.block.last_commit.signatures[] | select(.block_id_flag==2) | .validator_address'   # five
BIN=$(systemctl show -p ExecStart aetherd | grep -o '/[^ ;]*aetherd[^ ;]*' | head -1)
sha256sum "$BIN"                       # the key-change build
sudo cp "$BIN" "$BIN.pre-sessioncap"
sudo install -m 0755 <prebuilt folder>/aetherd "$BIN.new" && sudo mv "$BIN.new" "$BIN"
sudo systemctl restart aetherd         # if it hangs: sudo systemctl kill -s KILL aetherd && sudo systemctl start aetherd
sha256sum /proc/$(systemctl show -p MainPID --value aetherd)/exe   # matches step 1
MYADDR=$(curl -s localhost:26657/status | jq -r .result.validator_info.address)
curl -s localhost:26657/block | jq -r '.result.block.last_commit.signatures[].validator_address' | grep -c "$MYADDR"   # 1, before the next node
```

## 3. peer-1 (Joshua, on DardenPC, elevated PowerShell)

```powershell
Get-FileHash C:\aether-data\aetherd-<commit>.exe     # must match Gitty's sum
Copy-Item C:\aether-data\peer1-ensure.ps1 C:\aether-data\peer1-ensure.ps1.pre-sessioncap
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

**Abort rule.** If all five aren't on the new binary by **812,280**, put
every swapped node back on `.pre-sessioncap` (the key-change build), tell
Joshua, and H moves.

## 4. After H

- All five signed 812,870 to 812,890.
- Joshua merges the fork's pull request from the advisory page and
  publishes the advisory; then the release and the public notes.
