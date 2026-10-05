# October 2026 upgrade: everything on at block 205,000

**For:** the four validators (seed, sync3, sync4, peer-1), plus any other
node running `aetherd`. **Deadline:** every node on the new binary before
the chain reaches **block 205,000**, about 21:50 CT on 4 October 2026 at
~7 s blocks (check the tip and the time left before each step).

**Moved from 225,000 to 205,000 on 4 October 2026** (tip ~203,800), so
Joshua can watch the activation. **Every node swaps, sync4 included:** a
binary built for 225,000 (for example the one sync4 runs) disagrees with
this one from 205,000 and falls off the chain, and so does anything older
(`ece0049`, `f665d22`). Earlier versions of this runbook said 500,000,
350,000 and 225,000.

**Abort rule.** If all four aren't on the 205,000 binary by block
**204,700**, put every node already swapped back on the binary it ran
before, and tell Claude. Nothing has activated before 205,000, so the old
binaries still agree with everyone; a split at 205,000 with two nodes on
each side would stop the chain.

## What switches on at 205,000

`October2026UpgradeHeight` (`x/pow/types.go`) turns on every gated change
made since the 161,000 cutover, together, after governance proposals #3
and #4 passed (2026-10-04 15:58 CT):

- **Smooth difficulty retarget** (`x/pow/retarget.go`). The old rule
  multiplies difficulty by about ten for a share one block after the
  last; on 3 October it went from 7,278 to about 21,000,000 in hours, and
  validators lost their seats for want of a share per epoch. From
  205,000 each share moves it by `2^((60 s − elapsed) / 1800 s)`, about 2%
  for a fast one. At 205,000 difficulty is also capped once at 285,960
  so the new rule starts from a sane value.
- **Randomness beacon** (`beacon.go`). Validator selection draws from
  all qualified miners, weighted by work, with a seed built over the
  previous epoch. While there are no more than 21 qualified miners
  (`top_k_size`), it picks every one of them, the same set as today. Its
  design still awaits the external review
  `aether-randomness-beacon-design.md` requires before any mainnet use.
- **Merged mining** (`merged_mining.go`, `auxpow.go`): a Litecoin pool
  running `cmd/auxpowd` can merge-mine Aether. AuxPoW gets its own
  difficulty and slot, and while both kinds are mining the reward splits
  75% native, 25% merged (`merged_mining_reward_share_bps`). Merged work
  pays but never counts toward validator selection. Nothing changes for
  native miners until a pool actually sends work.

A node without the new binary computes difficulty, selection and AuxPoW
acceptance differently from 205,000 and falls off the chain; if two of
the four validators do, the chain stops.

## Miners: update now, no coordination

`powminer` changed too, and it isn't consensus code, so every miner can
take it today, in any order: it now refreshes its header before the
chain would refuse it as stale, and with `--loop` it no longer exits
after an unlucky round. Rebuild and restart each validator's miner
(`go build -o <its path>/powminer ./cmd/powminer` from `main`), and
check its address shows up in `aetherd query pow miner-leaderboard`
afterwards. Restarting a miner costs nothing: work already recorded this
epoch stays.

## What's different from the last cutover

- **No halt and no restart height.** Everything that changed in chain
  code since the 161,000 cutover binary is in `x/pow`, and all of it
  behaves exactly as before below 205,000. Old and new binaries agree on
  every block until then, so nodes can be swapped one at a time,
  whenever, in any order.
- **One at a time, though.** With four equal validators the chain needs
  three signing. Never have two validators down at once.
- **The seed goes last** until peer-1 has its own path to sync3 and sync4
  (section 2): while peer-1 reaches them only through the seed, a seed
  restart takes two validators out.

## 1. Build once

On a machine with Go 1.25, from `main`:

```bash
git fetch origin && git checkout origin/main
grep -n "October2026UpgradeHeight int64 = 205_000" x/pow/types.go
#   must show: a binary without it isn't the upgrade
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

Order: **sync3, sync4, peer-1, seed.** All four have moved already and
swap in place below; sync4's binary was built for 225,000, so it swaps
like the rest. Swap while `aetherd query pow active-validators` shows
four. If an epoch boundary (the next is block 204,479) leaves three,
keep going: each swap then pauses blocks for as long as that node is
down, so have the new binary checked before stopping the old one. For
each node:

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
it). The new binary refuses that field below 205,000 anyway.

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
- **Running out of time:** a node must not reach 205,000 on an older
  binary. If a validator can't be swapped by then (for example the set
  isn't back to four in time), tell Claude **before** the chain gets
  near 205,000 (the abort rule above): the height can still be moved later with
  another binary, but not once the chain passes it.
