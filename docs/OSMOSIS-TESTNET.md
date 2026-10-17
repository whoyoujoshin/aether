# Connecting `aether-testnet-1` to Osmosis testnet (`osmo-test-5`)

Runbook for opening a lasting IBC connection from Aether's live testnet
to Osmosis's public testnet and keeping it relayed in both directions.
Rewritten 2026-10-03 for how relaying works now (Helicase onto Aether,
`cmd/outbound` onto Osmosis). **Executed 4 to 5 October 2026; the path is
live.**

## Status: reopened 2026-10-17 from sync3; Helicase not yet switched

The second path was opened on 2026-10-17 (about 02:50 UTC, Aether tip
319,366) by `cmd/relayer` on sync3, after Osmosis kept refusing the
seed. The round trip of 12,345 uaeth went out and back.

| | Aether (`aether-testnet-1`) | Osmosis (`osmo-test-5`) |
|---|---|---|
| Client | `07-tendermint-3` (of Osmosis; Helicase is to update it) | `07-tendermint-5287` (of Aether; `cmd/outbound` on sync3 updates it) |
| Connection | `connection-3` | `connection-4615` |
| Channel | `transfer/channel-3` | `transfer/channel-11852` |
| Trusting period | 80 h (Osmosis unbonds in 120 h) | ~54 h (Aether's 80 h 39 min unbonding × 2/3) |

- **AETH on Osmosis:** `ibc/8277E1557EB379889BA54DFD0F19EDCFF907E4B3414E6B99D098F241CAED3BE1` (sha256 of `transfer/channel-11852/uaeth`).
- **Onto Osmosis:** `aether-outbound-osmosis` runs on sync3 (`--client-id 07-tendermint-5287`, gRPC `grpc.testnet.osmosis.zone:443`). It is disabled on the seed.
- **Onto Aether: not yet.** Helicase on the seed, sync3 and sync4 still follows `07-tendermint-1` over `rpc.osmotest5.osmosis.zone`. Until sync3 and sync4 follow `07-tendermint-3`, nothing relays Osmosis's packets or acknowledgements onto Aether, so don't send over `channel-3` yet. `07-tendermint-3` expires about 2026-10-20 10:00 UTC unless Helicase updates it first.
- **Relayer accounts and key homes:** as before (below), now on sync3.

## First path: 2026-10-04 to 2026-10-13 (closed)

**Live from 2026-10-04 21:58 CT.** Gitty ran steps 1 to 4 after the October upgrade activated at block
205,000, and steps 5 to 8 that night. Everything below is what was
actually used; the step text keeps the procedure for a rebuild.

| | Aether (`aether-testnet-1`) | Osmosis (`osmo-test-5`) |
|---|---|---|
| Client | `07-tendermint-1` (of Osmosis; Helicase updates it) | `07-tendermint-5277` (of Aether; `cmd/outbound` updates it) |
| Connection | `connection-1` | `connection-4605` |
| Channel | `transfer/channel-1` | `transfer/channel-11841` |
| Trusting period | 80 h (Osmosis testnet unbonds in 5 days) | ~53 h, from `bond_cooldown` × block time |

- **AETH on Osmosis:** `ibc/0D80A29BCBE8A38AAE75313264A0741092566D4EF5A7FEDAD7F86E12193C2328`.
- **Opened by `cmd/relayer`:** round trip of 12,345 uaeth at Aether 205,070 to 205,073.
- **End to end with the services only (step 8):** 1 AETH out at 206,917 and back at 206,931, each leg relayed in under a minute.
- **Relayer accounts:** Aether `aether1zva7at6leed498uw3rwm8xdxpnu2vdt4da7u633tz2333d4gtuvs5fyuwe`; Osmosis `osmo1mlv24mz4ygnfnp4x4345kpjyx2mqu99x2ag3rm`.
- **Key homes on the seed:** `/root/relayer-keys` (Aether) and `/root/.osmosisd-relayer` (Osmosis), both on the test keyring.
- **Helicase:** runs on sync3, sync4 and the seed; not yet on peer-1.

## 2026-10-16: Osmosis's client of Aether expired; reopening

`07-tendermint-5277` (Osmosis's client of Aether) expired around
2026-10-13. `rpc.osmotest5.osmosis.zone` had started answering the seed
with HTTP 403, so `cmd/outbound` couldn't refresh it, and nothing alerted
anyone. An expired client can't be updated, and reviving one on Osmosis
takes an Osmosis governance proposal, which isn't ours to pass. So the
path is reopened with a new client, connection and channel. **AETH gets
a new denom on Osmosis.** AETH already there through `channel-11841`
stays there.

On the seed, in this order:

1. **Settle what's in flight on the old channel first.** Helicase
   refunds transfers that time out on `channel-1` (the 400 AETH sent on
   2026-10-16) using Aether's client of Osmosis, `07-tendermint-1`.
   Leave Helicase on that client until faucet-v3 has its AETH back, or
   until `aetherd q ibc client status 07-tendermint-1` says `Expired`.
2. **Check the new endpoints:**
   ```bash
   curl -s https://rpc.testnet.osmosis.zone/status | jq -r .result.node_info.network   # osmo-test-5
   curl -s 'https://rpc.testnet.osmosis.zone/tx_search?query="tx.height>1"&per_page=1' | jq -r .result.total_count
   ```
3. **Open the path** with `cmd/relayer` built from main. The relayer reads
   both chains over gRPC before it creates anything, so a gRPC address
   that doesn't work fails cleanly. If it does, try
   `grpc.osmotest5.osmosis.zone:443` instead.
   ```bash
   cd /root/aether-src && git pull && go build -o /root/aether-relayer ./cmd/relayer
   /root/aether-relayer \
     -aether-rpc http://127.0.0.1:26657 -aether-grpc 127.0.0.1:9090 \
     -aether-chain-id aether-testnet-1 \
     -aether-key relayer -aether-home /root/relayer-keys -aether-gas-prices 0.0001uaeth \
     -cparty-rpc https://rpc.testnet.osmosis.zone \
     -cparty-grpc grpc.testnet.osmosis.zone:443 \
     -cparty-chain-id osmo-test-5 -cparty-bech32-prefix osmo \
     -cparty-key relayer -cparty-home /root/.osmosisd-relayer \
     -cparty-gas-prices 0.05uosmo \
     -keyring-backend test 2>&1 | tee /root/osmosis-handshake-20261016.log
   ```
   The relayer retries each request the RPC or gRPC gateway refuses
   (HTTP 403, 429, 502, 503, or a connection that never opens) for about
   four minutes, so an RPC that refuses some of its requests is no reason
   to wait; only one that refuses everything for four minutes stops it.
   It ends with `round trip complete`. If it fails partway, send the log
   before running it again (a rerun opens another set of clients).
4. **The new AETH denom on Osmosis**, from Osmosis's side of the new
   channel:
   ```bash
   printf 'transfer/<Osmosis channel>/uaeth' | sha256sum | awk '{print "ibc/" toupper($1)}'
   ```
5. **`cmd/outbound`** (`aether-outbound-osmosis.service`): set
   `--cparty-rpc https://rpc.testnet.osmosis.zone`, the gRPC address that
   worked in step 3, and `--client-id <Osmosis's new client of Aether>`.
   Then `systemctl daemon-reload && systemctl restart aether-outbound-osmosis`
   and `curl -s 127.0.0.1:8095/healthz | jq`.
6. **Helicase**, only after step 1: in `app.toml` on the seed, sync3
   and sync4 (back it up first), set
   `counterparty-rpc = "https://rpc.testnet.osmosis.zone"` and
   `client-id = "<Aether's new client of Osmosis>"`. Restart one node at
   a time, and only while the other three are signing. Each must show
   `helicase started` and no repeated `helicase cycle failed` before the
   next.
7. **Explorer:** in `aether-explorer.service`, change the Osmosis entry of
   `--ibc-rpc` to `https://rpc.testnet.osmosis.zone` and restart it.
8. **Send Claude** the log, both new client IDs, both channel IDs, the
   new denom and the `/healthz` output. Claude updates this page,
   [IBC.md](IBC.md), [OSMOSIS-POOL.md](OSMOSIS-POOL.md) and CLAUDE.md.
   After that, the probe takes `AETH_DENOM=<new denom>`.

### If Osmosis refuses the seed: open it from sync3

On 2026-10-16 (12:51 to 12:55 PM CT) the seed's handshake got HTTP 403 on
every try for the full four minutes of retries and stopped before
creating anything. The next morning sync3 and sync4 each got 24 of 24
answers from `rpc.testnet.osmosis.zone`, while the seed got none from
the third-party Osmosis RPCs either (Polkachu didn't connect, publicnode
answered 404). The clients belong to the chains, not to the machine that
opens them, so everything that talks to Osmosis moves to sync3: the
handshake, then `cmd/outbound`. sync3's own node serves the Aether side.

1. **Build on the seed from main** (`b10e31b` or later: the relayer
   retries refused requests and logs each try; outbound backs off), then
   copy the binaries and both relayer key homes to sync3:
   ```bash
   cd /root/aether-src && git pull
   go build -o /root/aether-relayer ./cmd/relayer && go build -o /root/aether-outbound ./cmd/outbound
   scp /root/aether-relayer /root/aether-outbound root@<sync3>:/root/
   scp -r /root/relayer-keys /root/.osmosisd-relayer root@<sync3>:/root/
   systemctl disable aether-outbound-osmosis   # stopped already; now it can't start again on the seed
   ```
   From here the Osmosis key signs only on sync3. Two machines signing
   with one key race its account sequence.
2. **Check sync3's node** is the one the relayer will use: chain
   `aether-testnet-1`, not catching up, gRPC on 9090, and the `kv` tx
   indexer, which the relayer needs to find its own transactions.
   ```bash
   curl -s 127.0.0.1:26657/status | jq -r '.result.node_info.network, .result.sync_info.catching_up'
   grep -A2 '^\[tx_index\]' /root/.aether/config/config.toml      # indexer = "kv"
   grep -A3 '^\[grpc\]' /root/.aether/config/app.toml              # enable = true, address ...:9090
   ```
   Use sync3's real home if it isn't `/root/.aether`, and its real ports
   below if they differ.
3. **Open the path, on sync3:** step 3's command as it stands, with the
   log at `/root/osmosis-handshake-$(date +%Y%m%d).log`. It ends with
   `round trip complete`. If it stops before `created client`, fix the
   cause and run it again. If it stops after that, don't rerun it: send
   the log to Claude.
4. **`cmd/outbound` on sync3:** copy the seed's
   `/etc/systemd/system/aether-outbound-osmosis.service` to sync3. Set
   `--client-id` to Osmosis's new client of Aether, the RPC and gRPC that
   worked in step 3, and the key homes from step 1. Then
   `systemctl daemon-reload && systemctl enable --now aether-outbound-osmosis`
   and `curl -s 127.0.0.1:8095/healthz | jq`. Do this the same day:
   Osmosis's new client of Aether expires in about two days without it.
5. **Helicase** (step 6), one node at a time, only while the other three
   are signing:
   - **sync3 and sync4:** point it at Aether's new client of Osmosis.
   - **The seed:** take Osmosis out of its `[helicase]`. Helicase asks
     Osmosis for updates every 2 seconds and doesn't back off when
     refused. If Osmosis's refusal resets with each request, it will
     never lift while the seed keeps asking. sync3 and sync4 cover
     Osmosis without it.
6. **ibcwatch** stays on the seed. Its line for Osmosis's client of
   Aether can't read Osmosis from there; Claude will settle where that
   line runs once the new client exists.

If sync3 is refused too, the same steps work from DardenPC against
peer-1's node (`http://127.0.0.1:26667`, `127.0.0.1:9091`), as the
Injective path was opened ([INJECTIVE-TESTNET.md](INJECTIVE-TESTNET.md)).

The gate (step 0) was governance proposals #3 and #4, both passed on
4 October: `max_age_num_blocks` = 2,880 and `bond_cooldown` = 51,840
(see docs/IBC.md).

| id | sets | voting ends |
|---|---|---|
| #3 | `consensus` evidence `max_age_num_blocks = 2880` | 2026-10-04 15:58:07 CT |
| #4 | `pow` `bond_cooldown = 51840` | 2026-10-04 15:58:17 CT |

Both had full turnout, all Yes, and both executed at the end of voting.

Why #4 matters here: `cmd/relayer` gives Osmosis's client of Aether a
trusting period derived from Aether's bond cooldown times its block time.
At today's 100 blocks that's about 8 minutes, so the client would expire
almost at once; at 51,840 blocks it's about 72 hours.

## What connects

| | Aether | Osmosis testnet |
|---|---|---|
| Chain ID | `aether-testnet-1` | `osmo-test-5` |
| RPC | on the seed: `http://127.0.0.1:26657` | `https://rpc.osmotest5.osmosis.zone` (older name: `rpc.testnet.osmosis.zone`) |
| gRPC | on the seed: `127.0.0.1:9090` | `grpc.osmotest5.osmosis.zone:443` (TLS) |
| Bech32 prefix | `aether` | `osmo` |
| Signing | ML-DSA-44 | secp256k1 |
| Unbonding | `bond_cooldown` × block time (~79h at 5.5 s blocks) | 5 days (the live client's trusting period is 80 h) |

Endpoints are from the Cosmos chain registry as of 2026-09-29; step 1
checks which answer. gRPC addresses ending `:443` or starting `https://`
are dialed with TLS (`relayer.NewChain`); anything else is plaintext.

## Who does what, once it's open

- **Opening it:** `cmd/relayer`, once. It creates a client on each chain,
  the connection and an `ics20-1` channel, then sends a small transfer
  out and back. It signs on Aether with the ML-DSA `relayer` key and on
  Osmosis with the Osmosis `relayer` key.
- **Onto Aether:** Helicase. Each validator started with
  `[helicase]` settings relays Osmosis's packets, acknowledgements,
  timeouts and client updates into the blocks it proposes, with no key
  ([HELICASE.md](HELICASE.md)).
- **Onto Osmosis:** `cmd/outbound`, a service on the seed, signing on
  Osmosis with the Osmosis `relayer` key (Osmosis charges gas for relay
  messages; the 200 OSMO there lasts a long time). It also refreshes
  Osmosis's client of Aether on a quiet channel.
- **Explorer:** `--ibc-rpc` draws Osmosis as the helix's second strand.

Everything here runs on the seed (Gitty), which can reach both chains.
A sandboxed Claude session can't reach Osmosis's endpoints or the seed.

## Step by step

### 0. Confirm the gate (after 2026-10-04 ~16:00 CT)

```bash
aetherd query governance proposal 3
aetherd query governance proposal 4
aetherd query pow params | grep -i bond_cooldown            # 51840
aetherd query consensus params | grep -i max_age_num_blocks  # 2880
```

Both proposals must show as passed and executed, and both values must
have changed. If not, stop and tell Claude.

### 1. Check Osmosis's endpoints from the seed

```bash
curl -s https://rpc.osmotest5.osmosis.zone/status | jq -r .result.node_info.network   # osmo-test-5
curl -s 'https://rpc.osmotest5.osmosis.zone/tx_search?query="tx.height>1"&per_page=1' | jq -r '.result.total_count'
```

The first must print `osmo-test-5`. The second must print a number, not
an error: Helicase and `cmd/outbound` find packets with `tx_search`, so
an RPC without transaction indexing won't do. If either fails, try
`rpc.testnet.osmosis.zone`, then the chain registry's other `osmo-test-5`
RPCs, and use the first that passes both.

### 2. Check both relayer keys are funded

```bash
aetherd keys show relayer -a --keyring-backend test          # then:
aetherd query bank balances <that address>                   # had 2 AETH on 2026-10-01
osmosisd keys show relayer -a --keyring-backend test --home ~/.osmosisd-relayer
curl -s https://lcd.osmotest5.osmosis.zone/cosmos/bank/v1beta1/balances/<osmo address> | jq   # had 200 OSMO
```

Keep the Osmosis key in the `test` keyring backend: the services below
run unattended and can't type a passphrase. It only holds testnet OSMO.

### 3. Build from main

```bash
git fetch origin && git checkout origin/main
go build -o /root/aether-relayer ./cmd/relayer
go build -o /root/aether-outbound ./cmd/outbound
```

(main must include the TLS fix in `relayer/chain.go`, `grpcDial`.)

### 4. Open the path

```bash
/root/aether-relayer \
  -aether-rpc http://127.0.0.1:26657 -aether-grpc 127.0.0.1:9090 \
  -aether-chain-id aether-testnet-1 \
  -aether-key relayer -aether-home /root/relayer-keys -aether-gas-prices 0.0001uaeth \
  -cparty-rpc https://rpc.osmotest5.osmosis.zone \
  -cparty-grpc grpc.osmotest5.osmosis.zone:443 \
  -cparty-chain-id osmo-test-5 -cparty-bech32-prefix osmo \
  -cparty-key relayer -cparty-home /root/.osmosisd-relayer \
  -cparty-gas-prices 0.05uosmo \
  -keyring-backend test 2>&1 | tee /root/osmosis-handshake.log
```

It prints each client, connection and channel it creates and ends with
`round trip complete`. **Save the log and send it to Claude.** Write down:

- **Osmosis's client of Aether**, from `created client 07-tendermint-<n>
  on counterparty, tracking aether`: `cmd/outbound` needs it.
- **Aether's client of Osmosis**, from `created client 07-tendermint-<n>
  on aether, tracking counterparty`. It won't be `07-tendermint-0`: that
  one is left from the local test in September. Helicase needs it.
- **The channel ID on each side**, from the two `channel transfer/...`
  lines: the transfer channel others will use.

If it fails partway, send Claude the log before running it again: a
second run opens a second set of clients rather than finishing the first.

`-aether-home` is wherever the Aether `relayer` key lives
(`/root/relayer-keys` on the seed). Osmosis testnet refused
`0.025uosmo` on 4 October (it wanted 60,000 uosmo where 50,000 was
offered), so use `0.05uosmo`; that first attempt created nothing.

### 5. Relay onto Osmosis: `cmd/outbound` as a service

`/etc/systemd/system/aether-outbound-osmosis.service`:

```ini
[Unit]
Description=Aether -> Osmosis testnet IBC relay (cmd/outbound)
After=network-online.target aetherd.service

[Service]
ExecStart=/root/aether-outbound \
  --aether-rpc http://127.0.0.1:26657 \
  --cparty-rpc https://rpc.osmotest5.osmosis.zone \
  --cparty-grpc grpc.osmotest5.osmosis.zone:443 \
  --cparty-chain-id osmo-test-5 --cparty-bech32-prefix osmo \
  --cparty-key relayer --cparty-home /root/.osmosisd-relayer \
  --cparty-gas-prices 0.05uosmo --keyring-backend test \
  --client-id 07-tendermint-<n: Osmosis's client of Aether, from step 4; live: 5277> \
  --refresh-after 1h \
  --listen 127.0.0.1:8095
Restart=always
RestartSec=10
User=root

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload && sudo systemctl enable --now aether-outbound-osmosis
curl -s 127.0.0.1:8095/healthz | jq     # HTTP 200, with when the client expires
```

### 6. Relay onto Aether: Helicase on the validators

Add to each validator's `config/app.toml`:

```toml
[helicase]
counterparty-rpc = "https://rpc.osmotest5.osmosis.zone"
client-id = "07-tendermint-<Aether's client of Osmosis, from step 4; live: 1>"
# refresh-after = "5m"   # the default; see below
```

`refresh-after` is how stale Aether's view of Osmosis may get on a quiet
channel. It matters to senders: a transfer from Aether with a relative
timeout (the CLI's default is 1,000 Osmosis blocks, ~12 minutes at
Osmosis's 0.7 s blocks) counts from that view. On 4 October, before this
setting existed, the view was refreshed only at a third of the 80 h
trusting period. It was a day behind, so a default transfer timed out
before it was sent (Helicase refunded it). `cmd/outbound`'s
`--refresh-after 1h` does the same for Osmosis's view of Aether, where
1,000 Aether blocks is about 1.5 hours.

It takes effect at the node's next restart. **Do it as part of the
rolling upgrade** ([UPGRADE-2026-10.md](UPGRADE-2026-10.md)): edit
`app.toml` just before each node's binary swap, so no node restarts
twice. A node already upgraded gets the setting at a separate restart,
one node at a time, with the same signing check as the upgrade.

Only proposers relay, so the more validators run it, the sooner packets
land; one is enough to keep the client fresh. After a node restarts with
it, its log should show `helicase started` and, once there's work,
`helicase: relay transactions ready`, with no repeated `helicase cycle
failed`:

```bash
journalctl -u aetherd --since -5min | grep -i helicase | tail
```

### 7. Show it on the explorer

Add `--ibc-rpc https://<Osmosis RPC from step 1>` to the
`aether-explorer` service's `ExecStart` and restart it. The overview's
helix then draws Osmosis as the second strand, and `/ibc` lists the new
channel.

### 8. Check it end to end

- **Osmosis's side:** its channel back to Aether is `OPEN`:
  `curl -s https://lcd.osmotest5.osmosis.zone/ibc/core/channel/v1/channels | jq '.channels[] | select(.counterparty.channel_id=="<Aether's channel>")'`.
- **Out and back:** send 1 AETH to an Osmosis address over the channel,
  then back, with the services from steps 5 and 6 doing the relaying
  (not `cmd/relayer`). Both legs should land within a minute or two.
- **Tell Claude:** the handshake log, the `/healthz` output, and the
  heights of the two transfers.

## USDC over this path

**Superseded 2026-10-08:** the testnet's USDC now comes straight from
Injective over Aether's own channel (`transfer/channel-2`; see
[INJECTIVE-TESTNET.md](INJECTIVE-TESTNET.md)). Both routes below are
dead: Injective's clients of Osmosis expired, and Noble's testnet is
shutting down. The settings and denom notes still apply. The tools accept exactly one USDC, named by its route
and its denom on the issuing chain. Any other token, even one called
USDC, shows by its bare denom and is never spent as USDC.

| | Noble's USDC through Osmosis | Circle's USDC on Injective, through Osmosis |
|--|--|--|
| Route | Noble `channel-22` → Osmosis `channel-4280` → Osmosis `channel-11841` → Aether `channel-1` | Injective `channel-77026` → Osmosis `channel-10092` → Osmosis `channel-11841` → Aether `channel-1` |
| `--usdc-path` | `transfer/channel-1/transfer/channel-4280` | `transfer/channel-1/transfer/channel-10092` |
| `--usdc-base-denom` | (default, `uusdc`) | `erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d` (case-sensitive) |
| `--usdc-issuer` | (default, `Noble`) | `Injective` |
| Denom on Aether | `ibc/9863BBE01215028DB708587E8899F8CA19A3459CABA8CF68130BB07060A35136` | `ibc/4F4C931B9AC39222C0EC5EB909F2BA8C0C615A1E343D8C1448ACDBBE6AF3743A` |
| Status | Going away: Noble's testnet is shutting down (around 2026-10-14) | **The testnet's USDC.** Needs a first transfer from Injective to Osmosis over `channel-77026`; none had crossed as of 2026-10-04 |

**The public testnet uses Injective's USDC** (`wallet.TestnetUSDC`), over
`transfer/channel-2` from `v0.2.8-testnet` on (through Osmosis in
`v0.2.7-testnet`). It's built in:
- **Claude Desktop bundle:** the agent knows it, with caps of 1 USDC per
  payment and 5 USDC a day. Both are settings in the bundle.
- **Desktop wallet:** it shows and sends it, unless `AETHER_USDC_CHANNEL`
  or `AETHER_USDC_PATH` is already set.

The explorer reads it from its service's flags. On the seed, add these to
`aether-explorer.service`'s `ExecStart`, then `systemctl daemon-reload`
and restart the explorer:

```
--usdc-path transfer/channel-2 --usdc-base-denom erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d --usdc-issuer Injective
```

The same settings work everywhere:
- **`agentmcp`, `cmd/paywall`, the explorer and `walletapi`:** these flags.
  `walletapi` also reads `AETHER_USDC_PATH`, `AETHER_USDC_BASE_DENOM` and
  `AETHER_USDC_ISSUER`.
- **TypeScript client:** `usdcPath`, `usdcBaseDenom` and `usdcIssuer`.
- **Python client:** `usdc_path`, `usdc_base_denom` and `usdc_issuer`.

`--usdc-channel` remains as shorthand for Noble's USDC over a direct
channel, if Aether ever opens one.

Switching from one USDC to the other is a settings change, but they're
different tokens. Balances in the old one stay where they are and show
by bare denom until they're sent back the way they came.

## Risks specific to this pairing

- **Client expiry is the main failure mode.** Aether's client of Osmosis
  trusts it for 80 h and Helicase refreshes it every few minutes, so it
  only expires if no validator running Helicase proposes for days, or
  Osmosis's RPC is unreachable that long. Osmosis's client of Aether
  (~53 h) depends on `cmd/outbound` the same way. Reviving an expired client needs a
  governance proposal on Aether.
- **Public testnets reset.** If Osmosis testnet is wiped, the path is
  rebuilt from step 4. That's normal for a testnet.
- **The explorer reads only Aether.** Osmosis's side is checked on
  Osmosis (step 8), and `cmd/outbound`'s health on `/healthz`.
