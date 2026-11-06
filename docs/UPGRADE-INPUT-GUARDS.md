# Review fixes on the testnet: on at block 712,080

**H = 712,080** (`AuxPoWInputGuardHeight` in `x/pow/auxpow.go` and
`ModuleAccountsActivationHeight` in `app/module_accounts.go`, kept equal by
a test), proposed 2026-11-06 at tip 625,266. At 5.45 s blocks it is about
**15:00 UTC on Wednesday 2026-11-11, 9:00 AM CST**; every 0.1 s of block
time moves that by about 2.4 hours. It is 720 blocks from each seat
boundary (the ends of 711,359 and 712,799). Earlier binaries carried a
50,000,000 placeholder. **H − 600 = 711,480** is the abort line.

**For:** the four validators (seed, sync3, sync4, peer-1), plus any other
node running `aetherd`.

## What switches on at H

Two findings from the review ([REVIEW.md](REVIEW.md) §3), already on from
block 1 on fresh chains:

- **F2/F3, AuxPoW input:** a merged-mining proof with no coinbase or chain
  branch, a coinbase whose scriptSig length overflows, a coinbase branch
  over 32 levels, or a branch hash that isn't 32 bytes is refused with an
  error. Below H the same proof panics inside its transaction, which the
  SDK turns into a failed transaction with a different code and gas, or is
  hashed in full before it fails. Well-formed proofs are checked exactly
  as before.
- **F5, module accounts:** at H every module account is created, or an
  ordinary account left at a module's address converted in place. On the
  testnet all seven already exist as module accounts (checked on the seed
  at 609,151), so this changes nothing: `TestModuleAccounts_NoChangeWhereTheyAllExist`
  checks the app hash is the same at every block across the height when
  they all exist.

## How this upgrade works

**It adds no store, so no node halts at H and nobody restarts there.**

**An old binary agrees until the first malformed AuxPoW proof after H.**
Before H both binaries treat every transaction the same. From H a
malformed proof fails with a different code on the new build than on the
old one, so the results hash differs and every node still on the old
binary splits at the next block. During the bug hunt someone may send one
on purpose, so **all four must be on the new binary before H**; there is
no waiting until after.

## 1. Build once, on a workstation (never on a validator), with Go 1.26.10

From the commit that merged H (`main`):

```bash
git fetch origin && git checkout origin/main
grep -n "AuxPoWInputGuardHeight int64 = " x/pow/auxpow.go               # 712_080
grep -n "ModuleAccountsActivationHeight int64 = " app/module_accounts.go # 712_080
grep -n "RotationActivationHeight int64 = 518_940" x/accountauth/rotation.go   # still there
go version                             # go1.26.10
go test ./x/pow/... ./app
bash scripts/prebuild.sh               # dist/prebuilt-<commit>/, with SHA256SUMS
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o dist/aetherd-$(git rev-parse --short HEAD).exe ./cmd/aetherd
sha256sum dist/prebuilt-*/aetherd dist/aetherd-*.exe
git log -1 --format='%h %s'
```

Copy the Linux `aetherd` to sync3, sync4 and the seed, and stage the
`.exe` on the seed for Joshua with its sha256.

The new build also carries what main gained since the validators' build
without changing a testnet rule: tipping and the price query (v0.2.18,
[UPGRADE-V0218.md](UPGRADE-V0218.md)), and governance's panic recovery
(F1, which only matters if a passed proposal's message panics, which
would otherwise stop the chain).

## 2. Swap, one node at a time, before the abort line

**First, all four must be signing.** A validator restart takes it out of
the signing set for a few blocks; with only three validators in the set,
that stops blocks. If one is out (peer-1 when DardenPC's miner stops),
bring it back first.

Order: **sync3, sync4, seed** (the seed after Joshua's OK), with peer-1
(step 3) any time in between. For each validator:

**a. Check four validators signed the latest block:**

```bash
curl -s localhost:26657/block | jq -r '.result.block.last_commit.signatures[] | select(.block_id_flag==2) | .validator_address'
```

**b. Swap the binary:**

```bash
BIN=$(systemctl show -p ExecStart aetherd | grep -o '/[^ ;]*aetherd[^ ;]*' | head -1)
sha256sum "$BIN"                       # the build it runs now
sudo cp "$BIN" "$BIN.pre-guards"
sudo install -m 0755 <prebuilt folder>/aetherd "$BIN.new" && sudo mv "$BIN.new" "$BIN"
sudo systemctl restart aetherd         # if it hangs: sudo systemctl kill -s KILL aetherd && sudo systemctl start aetherd
sha256sum /proc/$(systemctl show -p MainPID --value aetherd)/exe   # matches step 1
```

No data backup is needed: nothing changes on disk at the swap, and the
old binary can go back on the same data at any time before H.

**c. Wait until it's back and signing** before the next node:

```bash
MYADDR=$(curl -s localhost:26657/status | jq -r .result.validator_info.address)
curl -s localhost:26657/block | jq -r '.result.block.last_commit.signatures[].validator_address' | grep -c "$MYADDR"   # 1
```

## 3. peer-1 (Joshua, on DardenPC, elevated PowerShell)

```powershell
Get-FileHash C:\aether-data\aetherd-<commit>.exe     # must match Gitty's sum
Copy-Item C:\aether-data\peer1-ensure.ps1 C:\aether-data\peer1-ensure.ps1.pre-guards
```

1. In `C:\aether-data\peer1-ensure.ps1`, change the binary to
   `aetherd-<commit>.exe`. Keep the old `.exe`.
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

**Abort rule.** If all four aren't on the new binary by **711,480**, put
every swapped node back on `.pre-guards` (the old `.exe` on DardenPC),
tell Joshua, and H moves in a new PR. That's safe at any point before H.

## 4. After H

- Check all four signed the blocks around H (712,070 to 712,090).
- The module accounts still answer `code 0` ([REVIEW.md](REVIEW.md) §3's
  check).
- F2, F3 and F5 are then fixed on the testnet too; [BUG-HUNT.md](BUG-HUNT.md)
  keeps them out of scope either way.

## If something goes wrong

- **A node won't start on the new binary:** put `.pre-guards` back, start
  it, and send the log (`journalctl -u aetherd -n 100 --no-pager`).
- **A node still on the old binary split at a malformed proof after H:**
  stop it, run `aetherd rollback` with the old binary (one height), install
  the new binary and start it. If it got further than one block, restore
  its data from a synced node.
