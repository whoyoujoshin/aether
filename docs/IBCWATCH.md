# Client expiry alerts (`cmd/ibcwatch`)

`cmd/ibcwatch` posts to a Discord channel when an IBC light client:
- **expires in less than 24 hours,** while there's still time to act;
- **has expired:** the path then stops until the client is revived by governance or replaced;
- **can't be read** three times in a row;

and posts again when the client is fine. It checks each client every 10 minutes over the chain's RPC. A public RPC rate-limits anything that polls every few seconds.

Each IBC path has a client on both ends, and both expired in October 2026 with nobody told:
- **Aether's client of Injective** went stale because the RPC Helicase used had pruned the history it needed.
- **Osmosis's client of Aether** expired after Osmosis's RPC began refusing the seed, so `cmd/outbound` couldn't refresh it.

## Set it up (on the seed)

1. **A Discord webhook:** in the channel's settings, go to Integrations →
   Webhooks → New Webhook, then Copy Webhook URL. Anyone holding that URL
   can post to the channel, so keep it out of chats, commits and unit
   files.
2. **Build and store the URL:**
   ```bash
   cd /root/aether-src && git pull && go build -o /root/ibcwatch ./cmd/ibcwatch
   install -m 600 /dev/null /root/ibcwatch.env
   echo 'IBCWATCH_DISCORD_WEBHOOK=<the URL>' > /root/ibcwatch.env
   ```
3. **Check the clients once,** without posting:
   ```bash
   /root/ibcwatch --once \
     --watch "Aether's client of Osmosis,http://127.0.0.1:26657,07-tendermint-1" \
     --watch "Aether's client of Injective,http://127.0.0.1:26657,07-tendermint-2" \
     --watch "Injective's client of Aether,https://injective-testnet-rpc.polkachu.com:443,07-tendermint-510"
   ```
   Each line should say `expires in …`.
4. **The service,** `/etc/systemd/system/aether-ibcwatch.service`:
   ```ini
   [Unit]
   Description=IBC client expiry alerts to Discord (cmd/ibcwatch)
   After=network-online.target aetherd.service

   [Service]
   EnvironmentFile=/root/ibcwatch.env
   ExecStart=/root/ibcwatch \
     --watch "Aether's client of Osmosis,http://127.0.0.1:26657,07-tendermint-1" \
     --watch "Aether's client of Injective,http://127.0.0.1:26657,07-tendermint-2" \
     --watch "Injective's client of Aether,https://injective-testnet-rpc.polkachu.com:443,07-tendermint-510"
   Restart=always
   RestartSec=30
   User=root

   [Install]
   WantedBy=multi-user.target
   ```
   ```bash
   systemctl daemon-reload && systemctl enable --now aether-ibcwatch
   ```
   It posts one message at start, listing every client and when it
   expires. That message shows the webhook works.

**The Osmosis path reopened on 2026-10-17** ([OSMOSIS-TESTNET.md](OSMOSIS-TESTNET.md)), and both of its clients are watched:
- **On the seed,** Aether's client of Osmosis is now
  `--watch "Aether's client of Osmosis,http://127.0.0.1:26657,07-tendermint-3"`
  (changed 2026-10-17 once Helicase followed it; unit backup
  `aether-ibcwatch.service.bak-20261017`).
- **On sync3,** a second ibcwatch watches Osmosis's client of Aether alone,
  `--watch "Osmosis's client of Aether,https://rpc.testnet.osmosis.zone,07-tendermint-5287"`,
  since the seed can't read Osmosis. It has its own copy of
  `/root/ibcwatch.env` (copied with `scp`, then `chmod 600`).

Any new path gets the same two lines, one per end.

## Reading an alert

| Alert | What to do |
|---|---|
| ⚠️ expires in … | That client's relayer has stopped updating it. For Aether's clients that's Helicase on the validators: check `journalctl -u aetherd \| grep -i helicase`. For the other chains' clients it's `cmd/outbound`: check its `/healthz`. |
| 🔴 expired | The path is stopped. Transfers already sent are refunded once they time out, provided the other direction's client still works. To fix: a governance proposal on the chain that holds the client, or a new path ([OSMOSIS-TESTNET.md](OSMOSIS-TESTNET.md)). |
| ❔ can't read | The RPC in that line is down or refusing. Nothing has expired yet, but nothing can refresh the client either, so act before the ⚠️ alert. |
