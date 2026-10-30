# PQ key rotation upgrade: on at block 518,940

**H = 518,940** (`RotationActivationHeight`, `x/accountauth/rotation.go`),
chosen by Joshua on 2026-10-29 at tip 512,395. At 5.59 s blocks it is
about **03:30 UTC on 2026-10-30, 10:30 PM CDT on the 29th**; 5.5 to 5.8 s
blocks move that by about 20 minutes either way. It is 541 blocks after
the seat boundary at the end of 518,399. Earlier binaries carried
40,000,000. **H − 600 = 518,340** (about 9:35 PM CDT) is the abort line.

**For:** the four validators (seed, sync3, sync4, peer-1), plus the query
node if it runs, and any other node running `aetherd`.

## What switches on at H

PQ key rotation (`docs/PQ-KEY-ROTATION.md`): an account keeps its address
and changes the ML-DSA key stored on it, by its current key after
`RotationDelayBlocks` (15,300, about a day) or at once by its guardians;
watchtowers can cancel. From H:

- `MsgRotateKey`, `MsgCancelPendingRotation`, `MsgSetRecovery`,
  `MsgRecoverKey`, `MsgGuardianCancel`, `MsgGuardianSetRecovery` and
  `MsgWatchtowerCancel` are accepted at a transaction's top level (never
  inside authz, an authenticator or a proposal);
- the ante chain lets an account sign with its stored key;
- due changes apply at the end of each block.

## How this upgrade differs from 511,920

**It adds no store, so no node halts at H and nobody restarts there.**

**An old binary agrees until the first rotation message after H.**
`1080a98` and the new build agree on every block before H, across it,
and after it, as long as no block carries a rotation message. The first
block that does splits every node still on the old binary: that node
can't decode the message, while the new build executes it. Before H,
both binaries refuse rotation messages: the old one can't decode them,
and the new one refuses them in `CheckTx` (`ErrRotationNotActive`).
Neither puts one in a proposal.

**Checked on two devnets (2026-10-29), with a test build of main whose
height was 200:**

- A validator ran `1080a98` to block 114, then the new build on the same
  data. One full node ran the new build from genesis, and another ran
  `1080a98` throughout.
- Bank sends and fee-paying transactions ran throughout, along with
  `--gas auto` simulations against each node. Accounts sent their first
  transaction (which puts their key on chain) before and after H.
- Blocks 2 to 313 (31 with transactions) matched on app hash and results
  hash on all three nodes.
- On an `aether-rc-` chain (rotation on from block 1), the same three
  nodes agreed until a `wallet rotate-key` landed in block 71. There the
  `1080a98` full node halted with "wrong Block.Header.AppHash", and the
  new build went on with the rotation pending.

So the rule is: **every node on the new binary before anyone sends a
rotation message**, and H is the latest that can happen. **Don't send
one until all four are confirmed by sha256**, even after H.

## 1. Build once, on a workstation (never on a validator), with Go 1.26.10

From the commit that merged H (`main`):

```bash
git fetch origin && git checkout origin/main
grep -n "RotationActivationHeight int64 = " x/accountauth/rotation.go   # must show 518_940
grep -n "InflowCapActivationHeight int64 = 511_920" app/inflowcap.go     # still there
go version                             # go1.26.10
go test ./x/accountauth/... ./app
bash scripts/prebuild.sh               # dist/prebuilt-<commit>/, with SHA256SUMS
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o dist/aetherd-$(git rev-parse --short HEAD).exe ./cmd/aetherd
sha256sum dist/prebuilt-*/aetherd dist/aetherd-*.exe
git log -1 --format='%h %s'
```

Copy the Linux `aetherd` to sync3, sync4 and the seed, and stage the
`.exe` on the seed for Joshua with its sha256. Keep
`dist/prebuilt-<commit>`: its agentmcp, paywall and facilitator are the
ones to put on the seed after H (step 4).

## 2. Swap, one node at a time, before the abort line

Order: **sync3, sync4, seed** (the seed after Joshua's OK), with peer-1
(step 3) any time in between. With peer-1 at about 27%, no single
restart stops blocks. If the query node runs, swap it too (unit
`aether-query`, no signing check). For each validator:

**a. Check four validators signed the latest block:**

```bash
curl -s localhost:26657/block | jq -r '.result.block.last_commit.signatures[] | select(.block_id_flag==2) | .validator_address'
```

**b. Swap the binary:**

```bash
BIN=$(systemctl show -p ExecStart aetherd | grep -o '/[^ ;]*aetherd[^ ;]*' | head -1)
sha256sum "$BIN"                       # the 1080a98 build
sudo cp "$BIN" "$BIN.pre-rotation"
sudo install -m 0755 <prebuilt folder>/aetherd "$BIN.new" && sudo mv "$BIN.new" "$BIN"
sudo systemctl restart aetherd         # if it hangs: sudo systemctl kill -s KILL aetherd && sudo systemctl start aetherd
sha256sum /proc/$(systemctl show -p MainPID --value aetherd)/exe   # matches step 1
```

No data backup is needed: nothing changes on disk at the swap, and the
old binary can be put back on the same data at any time before a
rotation message lands.

**c. Wait until it's back and signing** before the next node:

```bash
MYADDR=$(curl -s localhost:26657/status | jq -r .result.validator_info.address)
curl -s localhost:26657/block | jq -r '.result.block.last_commit.signatures[].validator_address' | grep -c "$MYADDR"   # 1
```

## 3. peer-1 (Joshua, on DardenPC, elevated PowerShell)

```powershell
Get-FileHash C:\aether-data\aetherd-<commit>.exe     # must match Gitty's sum
Copy-Item C:\aether-data\peer1-ensure.ps1 C:\aether-data\peer1-ensure.ps1.pre-rotation
```

1. In `C:\aether-data\peer1-ensure.ps1`, change the binary from
   `aetherd-1080a98.exe` to `aetherd-<commit>.exe`. Keep the old `.exe`.
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

**Abort rule.** If all four aren't on the new binary by **518,340**, put
every swapped node back on `.pre-rotation` (`aetherd-1080a98.exe` on
DardenPC), tell Joshua, and H moves in a new PR. That's safe at any point
before a rotation message lands, H included.

## 4. After H

Once all four have been confirmed by sha256 and the chain is past H:

```bash
P=<prebuilt folder>
$P/wallet rotation <any address> --grpc localhost:9090 --chain-id aether-testnet-1   # "Rotation: active (delay 15300 blocks)"
```

- **First rotation (Gitty, a throwaway test key):** run `wallet
  rotate-key <key> <new key>`. Check that all four still sign the block
  it lands in and the next, and that `wallet rotation <key>` shows the
  pending key with its block. `wallet cancel-rotation <key>` then shows
  the cancel path. Or leave it to apply in about a day, and check
  `wallet sign-for` and a send from the account.
- **The seed's services** (agentmcp, paywalls, facilitator, test wallet)
  are from `1080a98` and check only that a key hashes to its address.
  After a rotation they would accept the account's retired key and refuse
  its new one. Before any account the services deal with rotates,
  redeploy them from `$P` (`install.sh`, `sandbox.sh`, and agentmcp
  over `/root/agentmcp`), so they apply the stored-key rule. aetherd stays
  untouched.
- `v0.2.17-testnet` waits until the service keys (AET-26) are done and a
  rotation has applied on the testnet (Joshua, 2026-10-29; on 10-30 he
  counted the scan cache, #189, as meeting the query-node condition,
  AET-18). Until then a node on the `v0.2.16-testnet` release binary
  splits at the first rotation message after H.

## If something goes wrong

- **A node won't start on the new binary:** put `.pre-rotation` back,
  start it, and send the log (`journalctl -u aetherd -n 100 --no-pager`).
- **A node still on `1080a98` split at a rotation message:** stop it,
  run `aetherd rollback` with the old binary (one height), install the
  new binary and start it. If it got further than one block, restore its
  data from a synced node.
