# Connecting `aether-testnet-1` to Osmosis testnet (`osmo-test-5`)

Runbook for opening a lasting IBC connection from Aether's live testnet
to Osmosis's public testnet and keeping it relayed in both directions.
Rewritten 2026-10-03 for how relaying works now (Helicase onto Aether,
`cmd/outbound` onto Osmosis). **Executed 4 to 5 October 2026; the path is
live.**

## Status: live since 2026-10-04 21:58 CT

Gitty ran steps 1 to 4 after the October upgrade activated at block
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
