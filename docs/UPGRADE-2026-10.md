# Rolling upgrade before block 500,000

**For:** the four validators (seed, sync3, sync4, peer-1), plus any other
node running `aetherd`. **Deadline:** every node on the new binary before
the chain reaches **block 500,000**, about 20 October 2026 at ~5 s
blocks. Aim to finish by **12 October**.

## Why

The binaries running now still carry two placeholder activation heights:

- **500,000:** validator selection would switch to the randomness beacon
  (`RandomnessBeaconActivationHeight`), code no one but its author has
  reviewed.
- **1,000,000:** the merged-mining rules would switch on
  (`MergedMiningActivationHeight`) before anything uses them.

`main` moves them to 10,000,000 and 20,000,000 (PR #71). A node still on
the old binary at 500,000 switches selection rules on its own and falls
off the chain; if two of the four validators do, the chain stops.

## What's different from the last cutover

- **No halt and no restart height.** Everything that changed in chain
  code since the 161,000 cutover binary is in `x/pow`, and all of it
  behaves exactly as before below its activation height (10,000,000 or
  20,000,000). Old and new binaries agree on every block until 500,000,
  so nodes can be swapped one at a time, whenever, in any order.
- **One at a time, though.** With four equal validators the chain needs
  three signing. Never have two validators down at once.
- **The seed goes last** until peer-1 has its own path to sync3 and sync4
  (section 2): while peer-1 reaches them only through the seed, a seed
  restart takes two validators out.
- **Don't do it around 15:58 CT on 4 October**, when governance
  proposals #3 and #4 execute. Before or after is fine; after is simpler.

## 1. Build once

On a machine with Go 1.25, from `main`:

```bash
git fetch origin && git checkout origin/main
grep -n "ActivationHeight int64 = 10_000_000\|ActivationHeight int64 = 20_000_000" x/pow/types.go
#   RandomnessBeaconActivationHeight int64 = 10_000_000
#   MergedMiningActivationHeight     int64 = 20_000_000   (both lines must show)
go build -o aetherd ./cmd/aetherd
sha256sum aetherd          # note it; every node gets this same file
git log -1 --format='%h %s'
```

Copy that one file to each node (`scp`). Building separately on each node
works too, but then compare the `sha256sum`s.

## 2. peer-1's direct path (before the upgrade, no restarts needed)

peer-1 is behind a home router, so it can only dial out. Its
`persistent_peers` already lists sync3 and sync4; their P2P ports just
need to accept connections. Firewall changes take effect without a
restart.

- **sync4** (AWS): in its EC2 security group, allow inbound **TCP 26676**
  (from anywhere, or just peer-1's public IP).
- **sync3**: allow inbound **TCP 26668** in its provider's firewall, and
  `sudo ufw allow 26668/tcp` if ufw is on.
- On both, check `config.toml`'s `[p2p] laddr` is `tcp://0.0.0.0:<that
  port>`, not `127.0.0.1` or a different port.

Check from **peer-1**:

```bash
nc -vz -w5 100.63.7.174 26668
nc -vz -w5 35.153.65.178 26676
```

Both must say `succeeded`/`open`. peer-1 connects to them on its own
within a minute or so, or at its restart in section 3. If either still
times out, the seed stays last in section 3 and peer-1's path is still
the open item.

## 3. Swap, one node at a time

Order: **sync3, sync4, peer-1, seed.** For each node:

**a. Before touching it,** check the other three are signing. Run this
anywhere with the RPC (it prints each validator address in the latest
block's commit; expect four):

```bash
curl -s localhost:26657/block | jq -r '.result.block.last_commit.signatures[] | select(.block_id_flag==2) | .validator_address'
```

**b. Swap the binary:**

```bash
sudo cp "$(which aetherd)" /usr/local/bin/aetherd.pre-500k     # keep the old one
sudo systemctl stop aetherd
sudo install -m 0755 ./aetherd "$(systemctl show -p ExecStart aetherd | grep -o '/[^ ;]*aetherd' | head -1)"
sudo systemctl start aetherd
sha256sum "$(which aetherd)"                                     # matches section 1
```

(If the unit's `ExecStart` path isn't where `which aetherd` points, use
the unit's path for both commands.)

**c. Wait until it's back and signing** before the next node:

```bash
curl -s localhost:26657/status | jq '.result.sync_info | {latest_block_height, catching_up}'
#   catching_up false, height moving
MYADDR=$(curl -s localhost:26657/status | jq -r .result.validator_info.address)
curl -s localhost:26657/block | jq -r '.result.block.last_commit.signatures[].validator_address' | grep -c "$MYADDR"
#   1 = this validator is in the latest block's commit
```

Only then move to the next node.

**peer-1** (third): after its restart, confirm it now sees sync3 and sync4
directly:

```bash
curl -s localhost:26657/net_info | jq -r '.result.peers[].node_info.moniker'
```

**seed** (last): it also runs the explorer and faucet, which keep working
across the restart; nothing else to do for them.

**If the Osmosis channel is already open** ([OSMOSIS-TESTNET.md](OSMOSIS-TESTNET.md)
step 6), add its `[helicase]` settings to each node's `app.toml` just
before that node's swap, so the restart picks them up and no node
restarts twice.

## 4. Done

- All four validators signing (step 3a shows four addresses).
- Every node reports the same `sha256sum`.
- Tell Claude: the commit (`git log -1` from section 1), the height when
  the last node finished, and peer-1's `net_info` peer list.

Until all four are done, **don't submit governance proposals that use the
new `merged_mining_reward_share_bps` field** (old binaries don't know
it). The new binary refuses that field below 20,000,000 anyway.

## If something goes wrong

- **A node won't start on the new binary:** put the old one back and
  start it. Nothing has changed on chain, so the old binary picks up
  where it stopped:
  ```bash
  sudo systemctl stop aetherd
  sudo install -m 0755 /usr/local/bin/aetherd.pre-500k "<the unit's ExecStart path>"
  sudo systemctl start aetherd
  ```
  Then send Claude `journalctl -u aetherd -n 100`.
- **The chain stalls during the upgrade:** two validators are down.
  Bring back whichever restarted last (old or new binary both work), and
  check the others with step 3a.
- **Running out of time:** a node must not reach 500,000 on the old
  binary. If the swap can't reach every validator by about 18 October,
  tell Claude.
