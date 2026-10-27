# Explorer: IBC, account abstraction, and a refused counterparty RPC

The block explorer (`cmd/explorer`, `explorer-web`) has an IBC page, an
account-abstraction page, and a quieter failure mode when a counterparty
RPC refuses the host that runs the explorer.

This does not change consensus. Restart `aether-explorer` only. Never
build on a validator: `scripts/prebuild.sh` on a workstation, then
`PREBUILT=<that folder> bash scripts/deploy-explorer.sh` on the seed.

## What the pages show

**IBC** (`/ibc`, `GET /api/ibc`) is read from Aether:

- every light client except the localhost client, with status
  (`Active`, `Expired`, `Frozen`, `Unknown`), the trusting period, the
  latest consensus time, and when that trusting period ends
  (consensus time plus the trusting period);
- connections and channels, including escrow on `transfer`;
- relayers: signers of successful `MsgRecvPacket`, `MsgAcknowledgement`,
  `MsgTimeout` and `MsgTimeoutOnClose` in the indexed window;
- recent ICS-20 transfers (outbound `MsgTransfer`, inbound `recv_packet`);
- IBC denoms with a positive supply, joined to their denom trace.

A client is **expiring** in the last 24 hours of its trusting period,
**expired** when the chain says so or that time has passed, and
**frozen** when the chain says so. Those cards use the warning and
danger treatments already in the explorer. `ibcwatch` is still the
alert; this page is the place a person looks.

**Account abstraction** (`/accounts`, `GET /api/aa`) is `x/accountauth`.
An account registers a session key (ML-DSA-44, expiry, allowed message
types, lifetime uaeth spend) or a guardian threshold (M of N). There is
no list-all query, so the page discovers accounts from indexed
`MsgRegisterAuthenticator`, `MsgRevokeAuthenticator` and
`MsgExecAuthenticated`, then reads each account's authenticators live.
`?addr=aether1…` reads one account even with no history. If the node
does not serve the query, the page says the module is not available. It
does not invent a smart-contract wallet.

The helix still draws the counterparty. The open transfer channel it
labels is the one to that chain with the most packets sent (a higher
channel number breaks a tie), so the live Osmosis path is `channel-3`
when both `channel-1` and `channel-3` are open.

## The 403

Checked 2026-10-26 against the live explorer
(`https://explorer.157-245-252-221.sslip.io/api/helix`): `errors.ibc` was
an HTTP 403 from the Osmosis RPC, with the RPC client's "unexpected end
of JSON" after the empty body. The seed's address is `157.245.252.221`.

From a network that Osmosis does not block, both
`https://rpc.osmotest5.osmosis.zone/status` and
`https://rpc.testnet.osmosis.zone/status` returned HTTP 200 and the same
node id (`279ab0841c40a9be872aa6f7ee3f04171fb2d96a`, moniker
`public-testnet-fsn1-0`). Piping one of those hostnames to the other
does nothing for the seed if the provider blocks the address, not the
name. Polkachu's testnet RPC did not resolve, and other public names
failed TLS or 404 on that check. Re-check from the seed; this note is
not a list of endpoints that work there.

That matches the 2026-10-16 refusal in
[OSMOSIS-TESTNET.md](OSMOSIS-TESTNET.md): the public Osmosis RPC
provider refuses the seed. It is not evidence the chain or the client
is down. Injective's public sentry answered 200 from the same outside
network.

## What the explorer does now

`--ibc-rpc` is still a comma-separated list of chains. Inside one slot,
pipes are fallbacks, tried in order, eight seconds each:

```text
--ibc-rpc https://rpc.osmotest5.osmosis.zone|http://127.0.0.1:26658,https://testnet.sentry.tm.injective.network:443
--ibc-name Osmosis,Injective
```

No Osmosis URL is hardcoded. A single URL behaves as before.

When every endpoint for the drawn chain fails:

- the response is still HTTP 200;
- `degraded.ibc` is a short notice (a 403 says the provider refused this
  explorer; it does not paste the JSON-unmarshal error);
- the overview and blocks pages show that notice in the warning banner,
  and the missing strand is dashed and labelled "RPC unavailable";
- the last successful strand for that primary URL is kept for six hours
  (`stale`, `asOf`) and drawn dimmer, labelled cached. Stale packet
  events are not mixed into the current window;
- the bridge card still comes from Aether. The chain id remembered from
  an earlier `/status` is enough to pick the channel, so a 403 does not
  blank the bridge.

A successful fallback sets `viaFallback` and the legend says "fallback
RPC". That is not a banner.

`/api/ibc` does not call the counterparty at all.

## Deploy

1. Build on a workstation, from the merge commit, with Go 1.26.10:

   ```bash
   bash scripts/prebuild.sh
   ```

   `dist/prebuilt-<commit>` includes `explorer` and `explorer-web-dist`.
   Check `SHA256SUMS`.

2. On the seed, before changing the unit, see which RPCs answer **from
   that box**:

   ```bash
   curl -sS -o /dev/null -w '%{http_code}\n' --max-time 10 https://rpc.osmotest5.osmosis.zone/status
   curl -sS -o /dev/null -w '%{http_code}\n' --max-time 10 https://rpc.testnet.osmosis.zone/status
   ```

   A 403 on both is the same refusal. Use an endpoint that prints `200`
   from the seed as the primary or as the pipe fallback (a full node
   that already follows Osmosis, if one is reachable from the seed).
   Leave Injective as its own comma-separated slot.

3. Edit only `aether-explorer.service` (drop-in or the unit). Do not
   restart `aetherd`. Back up the unit next to the other
   `aether-explorer.service.bak-*` copies. `daemon-reload`, then:

   ```bash
   PREBUILT=/path/to/prebuilt-<commit> bash scripts/deploy-explorer.sh
   ```

   The script swaps the binary and `explorer-web/dist`, restarts
   `aether-explorer`, and rolls back if `/api/stats` or `/services`
   does not answer. The flag change is separate from that swap: a new
   binary with the old single URL still degrades cleanly, and it will
   not recover Osmosis until a fallback the seed can read is configured.

4. Check, on the seed:

   ```bash
   curl -fsS 'http://127.0.0.1:8081/api/ibc' | jq '{clients,connections,channels:(.channels|length),denoms:(.denoms|length),note:.activityNote,attention:[.clientStates[]|{id:.clientId,status,attention,expiresAt}]}'
   curl -fsS 'http://127.0.0.1:8081/api/aa' | jq '{supported,accounts:(.accounts|length),txs:(.transactions|length),note}'
   curl -fsS 'http://127.0.0.1:8081/api/helix?seconds=60&min=4' | jq '{degraded,errors,ibc:{height:.ibc.height,stale:.ibc.stale,viaFallback:.ibc.viaFallback,chain:.ibc.chainId},bridge}'
   ```

   `degraded.ibc` should be absent when an endpoint answered.
   `errors.ibc` should be absent either way. `/ibc` should list the
   live clients (Osmosis on `07-tendermint-3` / `channel-3`, Injective
   on `07-tendermint-2` / `channel-2`, and the older paths that are
   still open). `/accounts` may be an empty list; that is the chain,
   not a failed page. `supported` should be `true`.
