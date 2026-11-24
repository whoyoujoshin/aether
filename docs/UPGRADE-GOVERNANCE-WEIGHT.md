# Governance counts power, not heads: on at block 884,880

**H = 884,880** (`PowerWeightedVotingHeight` in `x/governance/types.go`),
chosen 2026-11-18 at a tip of 805,534. At 5.96 s blocks it is about
**14:00 UTC on Monday 2026-11-23 (8:00 AM CST)**; every 0.1 s of block
time moves that by about 2 hours. It is 720 blocks from each seat
boundary. **H − 600 = 884,280** is the abort line.

**For:** the five validators (seed, sync3, sync4, Sydney, peer-1), plus
any other node running `aetherd`.

**Private until H has passed:** this fixes GHSA-3cvf-5vjr-2h49
([REVIEW.md](REVIEW.md) F12). Build from the advisory's private fork,
keep the binary and this note off public channels, and say only "a
consensus fix" until the advisory is published.

**After 812,880:** this build also carries the session-key fix and the
consensus-key rule ([UPGRADE-SESSION-KEY-CAP.md](UPGRADE-SESSION-KEY-CAP.md)),
with their height unchanged. Swap only once 812,880 has passed and its
checks are done.

## What switches on at H

- A governance vote weighs the validator's **current voting power times
  its tenure ratio** (0 to 1 over 30 days), both read when the votes are
  counted. Before H it weighed the tenure ratio alone, recorded when the
  vote was cast.
- **Quorum is weight, not heads:** the weight that voted, abstains
  included, must reach 60% of the active validators' total weight. Before
  H it was 60% of the validators counted by head. Nothing passes while the
  total weight is zero.
- **Only an active validator may vote.** Before H anyone could store a
  vote; it simply didn't count.
- Unchanged: a proposal passes with 2/3 of the non-abstain weight and is
  rejected by a 1/3 veto; deposits, periods and treasury spends are as
  before.
- `aetherd q governance tally <id>` reports `quorum_threshold` as the
  weight that must vote, in units of voting power (tens of millions on
  the testnet), instead of a number of validators.

## How this upgrade works

**It adds no store, so no node halts at H and nobody restarts there.**

**An old binary agrees until the first block after H that the new rules
treat differently:** any `MsgVote` (it costs a little more gas, and a
non-validator's is refused), or a proposal whose voting period ends after
H (it resolves by weight). So **all five must run the new binary before
H.**

## 1. Build once, on a workstation (never on a validator), with Go 1.26.10

From the GHSA-3cvf private fork's `fix` branch:

```bash
git fetch origin && git checkout origin/fix
grep -n "PowerWeightedVotingHeight int64 = " x/governance/types.go        # 884_880
grep -n "SessionKeyCapHeight int64 = " x/accountauth/session_cap.go       # 812_880
grep -n "ConsensusKeyOwnershipHeight int64 = " x/pow/consensus_key_owner.go # 812_880
grep -n "KeyChangeRevokesHeight int64 = " x/accountauth/keygen.go         # 786_960
go version                              # go1.26.10
go test ./x/governance/... ./x/pow/... ./x/accountauth/... ./app
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
[UPGRADE-SESSION-KEY-CAP.md](UPGRADE-SESSION-KEY-CAP.md) step 2, with the
backup named `.pre-govweight`:

```bash
curl -s localhost:26657/block | jq -r '.result.block.last_commit.signatures[] | select(.block_id_flag==2) | .validator_address'   # five
BIN=$(systemctl show -p ExecStart aetherd | grep -o '/[^ ;]*aetherd[^ ;]*' | head -1)
sha256sum "$BIN"                       # the session-key build
sudo cp "$BIN" "$BIN.pre-govweight"
sudo install -m 0755 <prebuilt folder>/aetherd "$BIN.new" && sudo mv "$BIN.new" "$BIN"
sudo systemctl restart aetherd         # if it hangs: sudo systemctl kill -s KILL aetherd && sudo systemctl start aetherd
sha256sum /proc/$(systemctl show -p MainPID --value aetherd)/exe   # matches step 1
MYADDR=$(curl -s localhost:26657/status | jq -r .result.validator_info.address)
curl -s localhost:26657/block | jq -r '.result.block.last_commit.signatures[].validator_address' | grep -c "$MYADDR"   # 1, before the next node
```

## 3. peer-1 (Joshua, on DardenPC, elevated PowerShell)

```powershell
Get-FileHash C:\aether-data\aetherd-<commit>.exe     # must match Gitty's sum
Copy-Item C:\aether-data\peer1-ensure.ps1 C:\aether-data\peer1-ensure.ps1.pre-govweight
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

**Abort rule.** If all five aren't on the new binary by **884,280**, put
every swapped node back on `.pre-govweight` (the session-key build), tell
Joshua, and H moves.

**Before H:** no governance votes or proposals from our own keys, so
nothing resolves while binaries are mixed.

## 4. After H

- All five signed 884,870 to 884,890.
- If a proposal exists, `aetherd q governance tally <id>` shows
  `quorum_threshold` in units of voting power.
- Joshua merges the fork's pull request from the advisory page and
  publishes the advisory. Then the explorer's governance page is updated
  publicly (it compares a head count with `quorum_threshold`), and the
  release follows.
