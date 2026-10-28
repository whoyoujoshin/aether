# The query node: the seed's services stop reading the seed's validator

**For:** Gitty (Joshua approves the new server's cost first). **Linear:** AET-18.

**Why.** On 2026-10-22 the seed's aetherd wedged near its memory cap and,
with peer-1 out, halted the chain (395,890). About 187 MB of its live heap
was decoding transaction results for searches our own services make: the
job board, reputation, the quest and the service directory
(`GetTxsEvent`). `GOMEMLIMIT` and a smaller IAVL cache went in at the
hardening swap. This is the lasting fix: those reads go to a full node
that holds no validator key, so a heavy query can slow or crash that node
but never a validator.

**What moves and what stays.**

| | Reads from |
|---|---|
| explorer, public agentmcp (`/mcp`, `/a2a`), test wallet, agentservices, facilitator, job board, every paywall | the query node (`point.sh services`) |
| `rpc.` and `grpc.157-245-252-221.sslip.io` (outside agents, wallets, the desktop app) | the query node (`point.sh public`) |
| aetherd and its Helicase, the powminer, the faucet, minerwatch, memwatch, ibcwatch, chainwatch | the seed's own node, as now |

The seed keeps running every service; only where they read from changes.
Nothing in this runbook changes consensus or touches a validator key, and
no binary is built: the query node runs the build the validators run.

## 1. The server

- Same **region** as the seed and in its **VPC**, so the services reach it
  over the private network. Note both private addresses (`ip -4 addr` on
  each; DigitalOcean's VPC range is usually `10.x`).
- Ubuntu 24.04, at least 2 vCPU / 4 GB RAM, SSH key login only.
- Disk: three times sync4's chain data (`du -sh /root/.aether/data` on
  sync4).
- `apt-get install -y jq rsync`.

## 2. Binary and settings to bring

- **The validators' aetherd**, the `3ad12a6` build: copy sync4's running
  binary and check it is sha256 `9a78e03a…97123`:
  ```bash
  # on sync4: the file name still carries an old commit, so go by the process
  sha256sum /proc/$(systemctl show -p MainPID --value aetherd)/exe
  ```
- **The seed's minimum gas price** (the query node admits transactions the
  seed's services broadcast exactly as the seed does):
  ```bash
  grep '^minimum-gas-prices' /root/.aether/config/app.toml
  ```
- **The seed's RPC CORS**, if it sets one (the public RPC moves too):
  `grep '^cors_allowed_origins' /root/.aether/config/config.toml`.
- **Peers:** each of the seed, sync3 and sync4's node ID
  (`curl -s localhost:26657/status | jq -r .result.node_info.id`) with
  its address. Use the seed's private address for the seed.

## 3. Set it up

On the query node, from a checkout of `main`:

```bash
AETHERD_BIN=/root/aetherd-3ad12a6 LISTEN_IP=<its private IP> ALLOW_FROM=<seed private IP> \
PEERS=<seed id>@<seed private IP>:26656,<sync3 id>@<sync3>:26656,<sync4 id>@<sync4>:26656 \
MIN_GAS_PRICES=<the seed's value> RPC_CORS='<the seed's value, if any>' \
  bash scripts/querynode/setup.sh
```

It refuses to run on a server that has a validator. It installs the
binary as `/usr/local/bin/aetherd-query` and makes `/root/.aether-query`
with a fresh key that is in no validator set. It sets the config:
- RPC and gRPC listen on the private IP only;
- the transaction index is `kv`;
- `timeout_commit` is 5 s;
- `iavl-cache-size` is 200,000.

It writes `aether-query.service` with `GOMEMLIMIT` at 70% of the memory
(enabled, not started) and opens RPC and gRPC to the seed's private IP
alone.

## 4. Copy the chain from sync4

The query node needs the whole transaction index: the directory, quest
and reputation search history back to the start. It also needs the
same `genesis.json` bytes, because CometBFT stores the genesis hash with
the data. So it starts from a copy of sync4's data. First check that
sync4 indexes transactions:

```bash
grep -A2 '^\[tx_index\]' /root/.aether/config/config.toml   # indexer = "kv"
```

**Never copy `priv_validator_key.json`, `node_key.json` or
`priv_validator_state.json`.** Only `data/` (less the state file) and
`config/genesis.json` move.

**First pass, sync4 still running** (from the query node; it can take a
while and costs sync4 nothing but disk reads):

```bash
rsync -a --delete --exclude priv_validator_state.json root@<sync4>:/root/.aether/data/ /root/.aether-query/data/
scp root@<sync4>:/root/.aether/config/genesis.json /root/.aether-query/config/genesis.json
```

**Second pass, sync4 stopped for a few minutes.** A copy of a running
LevelDB isn't consistent; this pass only sends what changed.
1. Check all four validators signed the latest block, and that sync4 is
   safe to stop. At 472,590 sync4 held 18.8%, so the others keep 81%. It
   isn't removed for the downtime: it is past the 10% cap, and removal
   takes about 68 minutes offline.
   ```bash
   curl -s localhost:26657/block | jq -r '.result.block.last_commit.signatures[] | select(.block_id_flag==2) | .validator_address'   # 4 lines
   /root/chainwatch --rpc http://127.0.0.1:26657 --safe-to-stop --validator BCEF78B5   # where chainwatch is installed
   ```
2. On sync4: `systemctl stop aetherd`.
3. On the query node: the same `rsync` again.
4. On sync4: `systemctl start aetherd`, then wait until it signs again:
   ```bash
   curl -s localhost:26657/block | jq -r '.result.block.last_commit.signatures[].validator_address' | grep -c BCEF78B5   # 1
   ```

Don't do this while peer-1 is out: then sync4 is needed for blocks.

## 5. Start it

```bash
systemctl start aether-query
journalctl -u aether-query -f            # replays from sync4's height, then follows
curl -s http://<its private IP>:26657/status | jq '.result.sync_info.catching_up, .result.sync_info.latest_block_height, .result.validator_info.voting_power'
# false, the tip, "0"
```

If it stops with `genesis doc hash in db does not match`, the
`genesis.json` isn't sync4's: copy it again.

## 6. Move the services (on the seed)

Before you start, note the seed's aetherd memory, for comparing later:

```bash
ps -o rss= -p $(systemctl show -p MainPID --value aetherd)
```

From `/root/aether-src` at `main`:

```bash
export QUERY_GRPC=<query private IP>:9090 QUERY_RPC=http://<query private IP>:26657
bash scripts/querynode/point.sh services --dry-run    # the unit diffs, nothing changed
bash scripts/querynode/point.sh services
```

**Before it changes anything**, it checks the query node:
- it is on `aether-testnet-1`;
- it isn't the seed itself;
- it isn't catching up and is within 5 blocks of the seed;
- it has no voting power;
- its gRPC port answers.

**Then it moves one unit at a time:** the job board, the facilitator,
agentservices, the paywalls, the test wallet, the public agentmcp, and
last the explorer. For each unit it:
- backs up the unit file and drop-ins as `<file>.bak-query-<stamp>`;
- rewrites `--grpc` (and `--rpc` for the explorer and agentmcp);
- restarts the unit;
- checks it. Where a service can, the check reads the chain: the explorer's `/api/stats`, the job board's and facilitator's `healthz`, and an agentmcp `find_services` call (a transaction search).

If a unit fails its check, the script puts that unit back and stops.

It also writes `/root/aether-query.env`, so later runs of `install.sh`
and `sandbox.sh` keep the query node. `point.sh status` shows where each
unit reads.

Then check by hand that `https://explorer.157-245-252-221.sslip.io` loads,
that `/svc/hello` answers 402, and that the public `/mcp`'s
`find_services` lists the six services.

## 7. Move the public RPC and gRPC

Once the services have run on it for a while (an hour is enough):

```bash
bash scripts/querynode/point.sh public --dry-run
bash scripts/querynode/point.sh public
curl -s https://rpc.157-245-252-221.sslip.io/status | jq -r .result.node_info.moniker   # aether-query
```

This rewrites the two `reverse_proxy` lines in `/etc/caddy/Caddyfile`
(with a backup), runs `caddy validate`, then `systemctl reload caddy`.

## 8. After

- **Seed memory:** a day later, compare the seed's aetherd memory with step
  6, and the memwatch daily summary. The `GetTxsEvent` frames should be
  gone from a fresh heap profile.
- **Query node memory:** copy memwatch to the query node (unit
  `aether-query` instead of `aetherd`) if you want its alerts too.
- **If the query node goes down:** the public services fail their reads,
  but no validator is affected. To move them back takes a minute:
  ```bash
  bash scripts/querynode/point.sh back-public
  bash scripts/querynode/point.sh back-services
  ```
  `back-services` renames the env file `.off`, so redeploys use the
  local node again.
- **Upgrades:** the query node takes each consensus binary like a
  validator does, at the same time, by the same runbook. It has no key,
  so the order doesn't matter.
  - For the inflow cap and fees swap (`docs/UPGRADE-INFLOWCAP-FEES.md`) it
    also halts at H and needs its one restart.
  - It has no voting power, so the chain resumes without it.

Send Joshua the query node's private IP and node ID, the seed's aetherd
memory before and after, and `point.sh status`.
