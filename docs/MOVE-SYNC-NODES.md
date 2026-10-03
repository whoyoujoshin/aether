# Moving sync3 and sync4 onto their own servers

**For:** Gitty. **Why:** sync3 and sync4 run on the same computer, a
container whose process 1 is `tini`, with no systemd. That computer
restarts when its own software updates, and every restart takes two of the
four validators down at once, which stops the chain: 11 hours 48 minutes on
2 October (no block between 188623 at 10:12 and 188624 at 22:00 CT).
The 10-minute check that restarts them is a stopgap. The fix
is one server per node, with systemd bringing it back after any crash or
reboot.

This also does sync3's and sync4's part of the rolling upgrade
([UPGRADE-2026-10.md](UPGRADE-2026-10.md)): each starts on its new server
with the new binary.

**Do the nodes one at a time.** Finish sync3 completely, including the
signing check in step 4, before touching sync4. With four validators the
chain needs three signing, so only one may be down at any moment.

## The one rule: a validator key runs in exactly one place

If the same `priv_validator_key.json` signs from two machines, even for a
few seconds, the chain bans that validator permanently and burns its
escrow. So at every step below the old copy is **stopped and made
unstartable before** the new one starts, and the new one never starts
from a copy taken while the old one was still running.

## 0. Phone alerts first

So the move itself is watched. Joshua subscribes to this topic in the
ntfy app: **`aether-alerts-3in7b028jso6`**. Anyone who knows the name
can read it; the alerts hold only public chain data.

On the seed:

```bash
cd /root/aether && git fetch origin && git checkout origin/main
go build -o /root/minerwatch ./cmd/minerwatch
```

`/etc/systemd/system/aether-minerwatch.service`:

```ini
[Unit]
Description=Aether halt and validator alerts (minerwatch -> ntfy)
After=network-online.target aetherd.service

[Service]
ExecStart=/root/minerwatch --grpc 127.0.0.1:9090 \
  --webhook https://ntfy.sh/aether-alerts-3in7b028jso6 \
  --address <the four validators' miner addresses, comma-separated> \
  --stall-after 3m --state /root/minerwatch.state
Restart=always
RestartSec=10
User=root

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload && sudo systemctl enable --now aether-minerwatch
journalctl -u aether-minerwatch -n 20
```

Check the phone end of it once:

```bash
curl -d "Aether alerts: test from the seed" https://ntfy.sh/aether-alerts-3in7b028jso6
```

Joshua's phone should show it within seconds. The service alerts on a stalled chain (no new block for 3
minutes), the seed's node not answering, and a validator leaving the
set. It runs on the seed, so it can't report the seed itself going
down; a second copy on one of the new servers covers that later.

## 1. Create the two servers

One per node, in **different regions** from each other (and ideally from
the seed), so one data-centre problem can't take two validators:

- Ubuntu 24.04 LTS, at least 2 vCPU / 4 GB RAM.
- Disk: at least three times the node's current data. On the old
  computer, `du -sh <sync3 home>` says how much that is.
- SSH key login only.

On each:

```bash
sudo apt-get update && sudo apt-get install -y jq rsync build-essential
# Go 1.25 (same as the seed), then:
git clone https://github.com/whoyoujoshin/aether /root/aether
cd /root/aether && go build -o /usr/local/bin/aetherd ./cmd/aetherd
sha256sum /usr/local/bin/aetherd     # same as the rolling-upgrade binary
sudo ufw allow 22/tcp && sudo ufw allow 26656/tcp && sudo ufw --force enable
```

`/etc/systemd/system/aetherd.service` (written now, **not started**):

```ini
[Unit]
Description=Aether validator
After=network-online.target
Wants=network-online.target

[Service]
User=root
ExecStart=/usr/local/bin/aetherd start --home /root/.aether
Restart=always
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload      # don't enable or start yet
```

## 2. Find what to move (old computer)

```bash
ps -eo pid,args | grep [a]etherd      # each node's --home and its restart loop
du -sh <sync3 home>
```

## 2a. Miners, before anything is stopped

Two chain rules (`x/pow`) decide whether a moved validator stays in the
set:

- **Epoch selection.** At the last block of every epoch
  (`epoch_length` blocks, 1440 at genesis), the set becomes the
  addresses with accepted native work (`MsgSubmitPoW`) in that epoch.
  Any active validator with no work that epoch is **removed**. The one
  exception: if nobody with a registered consensus key has work, the set
  is left unchanged.
- **Liveness.** A validator that misses more than 30 of the last 60
  blocks is removed at once (no ban, no escrow loss). It comes back only
  at an epoch boundary where it has work.

So a miner that isn't running is a validator waiting to be dropped, and
turning miners on for **some** of the four can drop the others at the
next boundary. Check first, on the seed:

```bash
aetherd query pow params                       # epoch_length
aetherd query pow current-epoch
aetherd query pow active-validators
aetherd query pow miner-leaderboard            # this epoch's work so far
aetherd query pow miner-leaderboard <current epoch - 1>
```

Before the move, **all four validators' miner addresses** should be on
this epoch's leaderboard, so whichever of them are missing get their
miners started. A miner is only a client: it signs with its miner
account key, not the consensus key, and can run anywhere that reaches a
node's gRPC, so restarting it from its old start script is fine. Run
each miner account in one place only. If it's unclear which addresses
belong to which validator, stop and ask Claude rather than starting a
subset.

## 3. Move sync3

**a. Copy while it's still running** (most of the data, without
downtime). This copy is never started; step d replaces it:

```bash
rsync -a <sync3 home>/ root@<new sync3 IP>:/root/.aether/
```

**b. Check the other three are signing**, as in UPGRADE-2026-10.md step
3a (four addresses in the latest block's commit).

**From step c to step g is the downtime, and it has a budget:** under
30 blocks (about 3 minutes at today's speed), or the liveness rule
removes sync3 until the next epoch boundary, and the chain runs on the
other three with all three needed. Have everything ready before c:
the new server's `config.toml` and `app.toml` edits (step f) prepared
as files to copy in, the commands typed out.

**c. Stop sync3 for good on the old computer, in this order:**

1. Take sync3 **out of its restart loop and out of the 10-minute
   check** first, or they'll start it again.
2. Stop the process, and confirm it's gone: `ps -eo pid,args | grep [a]etherd`
   no longer shows sync3's `--home`.
3. Stop sync3's miner, if it runs here.

**d. Final copy, now that it's stopped**, so the new server gets
exactly the state the old node ended with, including
`data/priv_validator_state.json`:

```bash
rsync -a --delete <sync3 home>/ root@<new sync3 IP>:/root/.aether/
```

This overwrites `config.toml` and `app.toml` with the old computer's, so
the step f edits go in **after** it (copy the prepared files over),
never before.

**e. Make the old copy unstartable:**

```bash
mv <sync3 home>/config/priv_validator_key.json <sync3 home>/config/priv_validator_key.json.MOVED-DO-NOT-START
```

**f. On the new server, before starting it**, fix the network settings
in `/root/.aether/config/config.toml`:

```toml
[p2p]
laddr = "tcp://0.0.0.0:26656"
external_address = "<new sync3 IP>:26656"
persistent_peers = "dfa6aae4b7bfd5b0eb1e22fabbae3e83a475b938@157.245.252.221:26656,<sync4 id>@157.245.252.221:26676"

[rpc]
laddr = "tcp://127.0.0.1:26657"
```

In `app.toml`, set `[grpc] address = "127.0.0.1:9090"` (sync4's copy
says 9092). sync4's address in `persistent_peers` is whatever reaches
it from outside today (the tunnel on the seed, `157.245.252.221:26676`,
until sync4 moves), not its `127.0.0.1` entry from the shared computer.

Its node ID doesn't change (it's in the copied `node_key.json`). Check
that `config/priv_validator_key.json` is there and that
`data/priv_validator_state.json`'s height is at least the height the old
node stopped at.

**g. Start it:**

```bash
sudo systemctl enable --now aetherd
curl -s localhost:26657/status | jq '.result.sync_info | {latest_block_height, catching_up}'
```

Wait for `catching_up: false`, then the signing check (UPGRADE-2026-10.md
step 3c). Start sync3's miner here if it moved. Then reboot the new
server once (`sudo reboot`) and confirm the node comes back and signs on
its own.

## 4. Tell the network where sync3 is now

The node ID is unchanged; only the address moved.

- **peer-1** and the **seed**: in `persistent_peers`, replace sync3's old
  address with `<sync3 id>@<new sync3 IP>:26656`. It takes effect at
  their next restart (peer-1's upgrade restart, the seed's last), and
  they find it through peer exchange before then.
- From **peer-1**, `nc -vz -w5 <new sync3 IP> 26656` should succeed: the
  new server accepts inbound connections, so this also gives peer-1 its
  direct path.

## 5. Move sync4

Repeat steps 3 and 4 for sync4 with the second server, only after sync3
has been signing on its new server for a while and the network is
healthy (four addresses in the commit).

## 6. Finish

- Delete sync3's and sync4's old homes from the old computer, after the
  new servers have run cleanly for a day. Until then, keep them with the
  key renamed (step 3e). **Never** rename it back.
- Remove the 10-minute check and restart loops on the old computer.
- Tell Claude: both new IPs, each node's height when it started signing
  on its new server, and any errors.

## If something goes wrong

- **The new node won't start:** look at `journalctl -u aetherd -n 100`.
  Fix it there. **Don't restart the old copy:** its key is renamed for
  a reason, and the chain runs on the other three meanwhile.
- **The chain stalls while one node is moved:** a second validator went
  down. Bring that one back (it's the problem, not the moved node).
- **Not sure whether the old process is really stopped:** don't start
  the new one. Check `ps` again, and ask Claude.
