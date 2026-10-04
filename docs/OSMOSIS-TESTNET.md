# Connecting `aether-testnet-1` to Osmosis testnet (`osmo-test-5`)

Runbook for opening a lasting IBC connection from Aether's live testnet
to Osmosis's public testnet and keeping it relayed in both directions.
Rewritten 2026-10-03 for how relaying works now (Helicase onto Aether,
`cmd/outbound` onto Osmosis); not yet executed.

## Status: gate cleared 2026-10-04; start after the October binary swaps

Proposals #3 and #4 both passed. Gitty read the new values back from the
seed's public RPC at block 202,043 (16:05 CT): `max_age_num_blocks` =
2,880 and `bond_cooldown` = 51,840 (see docs/IBC.md). Step 0 below is
done. Begin at step 1 once the peer-1, sync3 and seed binary swaps for
block 225,000 are finished ([UPGRADE-2026-10.md](UPGRADE-2026-10.md)),
so the IBC setup and the swaps don't run on the same validators at once.

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
| Unbonding | `bond_cooldown` × block time (~72h after #4) | 24h |

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
  -aether-key relayer -aether-home /root/.aether -aether-gas-prices 0.0001uaeth \
  -cparty-rpc https://rpc.osmotest5.osmosis.zone \
  -cparty-grpc grpc.osmotest5.osmosis.zone:443 \
  -cparty-chain-id osmo-test-5 -cparty-bech32-prefix osmo \
  -cparty-key relayer -cparty-home /root/.osmosisd-relayer \
  -cparty-gas-prices 0.025uosmo \
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
  --cparty-gas-prices 0.025uosmo --keyring-backend test \
  --client-id 07-tendermint-<n: Osmosis's client of Aether, from step 4> \
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
counterparty-rpc = "https://rpc.osmosis-endpoint-from-step-1"
client-id = "07-tendermint-<Aether's client of Osmosis, from step 4>"
```

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

- **Client expiry is the main failure mode.** Osmosis's 24h unbonding
  means Aether's client of Osmosis trusts it for less than a day.
  Helicase refreshes it at a third of that, so it only expires if no
  validator running Helicase proposes for many hours, or Osmosis's RPC
  is unreachable that long. Reviving an expired client needs a
  governance proposal on Aether.
- **Public testnets reset.** If Osmosis testnet is wiped, the path is
  rebuilt from step 4. That's normal for a testnet.
- **The explorer reads only Aether.** Osmosis's side is checked on
  Osmosis (step 8), and `cmd/outbound`'s health on `/healthz`.
