# Connecting `aether-testnet-1` to Osmosis testnet (`osmo-test-5`)

Runbook for opening a real IBC connection from Aether's live testnet to
Osmosis's public testnet, using this repo's own `cmd/relayer` (no
off-the-shelf relayer works here -- see docs/IBC.md's "Real relayer
test" section for why). Written 2026-09-28, not yet executed.

## Status: blocked on a governance precondition -- read this first

docs/IBC.md's live-verification note ends with:

> Until both proposals pass on `aether-testnet-1`, don't open an IBC
> connection that's meant to stay up.

The two proposals (raising `x/consensus` evidence `max_age_num_blocks`
100,000 -> 2,880, and `x/pow`'s `bond_cooldown` 100 -> 51,840) close a
real gap: at live block times, the old values let a validator withdraw
before misbehavior evidence against them expires, which undermines the
light client's safety assumption. **Check both have actually passed
before doing anything below** -- e.g. from the live explorer's
Governance page, or:

```
aetherd query gov proposals --chain-id aether-testnet-1 --node https://rpc.157-245-252-221.sslip.io
```

If they haven't passed, that's the actual next step, not this runbook.
Given the testnet's own governance quorum has been as low as 1-2 votes
recently, this may just mean voting them through.

## What this connects

| | Aether | Osmosis testnet |
|---|---|---|
| Chain ID | `aether-testnet-1` | `osmo-test-5` |
| RPC | `https://rpc.157-245-252-221.sslip.io` (plain `http://157.245.252.221:26657` also works) | `https://rpc.testnet.osmosis.zone` |
| gRPC | `grpc.157-245-252-221.sslip.io:443` (plain `157.245.252.221:9090`) | `https://grpc.testnet.osmosis.zone` |
| REST/LCD | -- | `https://lcd.testnet.osmosis.zone` |
| Bech32 prefix | `aether` | `osmo` |
| Signing | ML-DSA-44 (`PostQuantumDecorator` rejects anything else) | standard secp256k1 |
| Faucet | `https://faucet.157-245-252-221.sslip.io/request` | `https://faucet.testnet.osmosis.zone` (~100 OSMO/day/address) |
| Unbonding period | governed by `x/pow`'s `bond_cooldown` (see gate above) | 24h (`86400s`) -- short; see risks below |

## Why this doesn't need new relayer code

`cmd/relayer`'s counterparty side (`counterparty.MakeEncodingConfig`)
only ever registered standard Cosmos SDK + ibc-go modules
(auth/bank/staking/capability/ibc/transfer) -- nothing specific to the
repo's own local test chain except a hardcoded `"cparty"` bech32
prefix. That's now a flag (`-cparty-bech32-prefix`, commit `9dd4fc4`),
so pointing the same binary at Osmosis instead of the local
`cmd/counterpartyd` is a config change, not a code change.

## Where this has to run

**Not from a sandboxed Claude Code session.** This runbook was written
in one whose outbound network is allowlisted to package registries and
GitHub only -- it gets a hard `403` connecting to either Osmosis's
endpoints or Aether's own seed. It has to run somewhere with real
internet access to both sides: the seed itself (same box that already
runs `aether-explorer`, and where Gitty already ran `cmd/relayer` once
for the local-counterparty live verification), or any operator machine
that can reach both RPC endpoints.

Because this opens a real light client on the live chain and needs a
persistent process to avoid it expiring (see risks below), **the seed
is the right place to run it**, not a one-off from a laptop.

## Step by step

### 1. Confirm the governance gate (above) has actually passed

Don't proceed otherwise.

### 2. Get a funded relayer key on Aether

Gitty already has a working, funded ML-DSA `relayer` key on
`aether-testnet-1` from the local-counterparty live verification run
(docs/IBC.md, "Live-verified on `aether-testnet-1`"). Reuse it if it's
still there and still funded -- check balance first:

```
aetherd query bank balances <relayer address> --chain-id aether-testnet-1 --node https://rpc.157-245-252-221.sslip.io
```

If it needs topping up, or a fresh key is preferred, fund it the normal
way (`README.md`'s faucet section): the key must be ML-DSA
(`aetherd keys add relayer --algo ml-dsa ...`), since
`PostQuantumDecorator` rejects anything else at the AnteHandler.

### 3. Create and fund a relayer key on Osmosis testnet

Standard secp256k1, standard `osmosisd`/`osmosis` keyring flow:

```
osmosisd keys add relayer --keyring-backend test
# fund it:
curl -X POST https://faucet.testnet.osmosis.zone/request \
  -H 'Content-Type: application/json' -d '{"address":"osmo1..."}'
```

The faucet is rate-limited (~100 OSMO/day/address) -- request early,
this may take a couple of days to accumulate enough for client
creation plus several handshake/relay transactions.

### 4. Build `cmd/relayer` from this branch (or `main` once merged)

```
go build -o relayer ./cmd/relayer
```

### 5. Run it

```
./relayer \
  -aether-rpc https://rpc.157-245-252-221.sslip.io \
  -aether-grpc grpc.157-245-252-221.sslip.io:443 \
  -aether-chain-id aether-testnet-1 \
  -aether-key relayer -aether-home <path to Aether keyring> \
  -aether-gas-prices 0.0001uaeth \
  -cparty-rpc https://rpc.testnet.osmosis.zone \
  -cparty-grpc https://grpc.testnet.osmosis.zone \
  -cparty-chain-id osmo-test-5 \
  -cparty-bech32-prefix osmo \
  -cparty-key relayer -cparty-home <path to Osmosis keyring> \
  -cparty-gas-prices 0.025uosmo \
  -keyring-backend test
```

(`test` keyring shown to match the local-verification flow; use
`file` or `os` for anything meant to stay funded unattended.)

One run does the whole path, per docs/IBC.md: 07-tendermint client
each direction (trusting/unbonding periods read live from each chain),
connection handshake, an unordered `ics20-1` channel, then a small
transfer out and back, checking escrow returns to exactly 0.

### 6. Verify

- `cmd/relayer`'s own output should end `round trip complete`.
- The explorer's `/ibc` page (this repo's own, live at
  `https://explorer.157-245-252-221.sslip.io/ibc`) should show a second
  channel card with `counterpartyChainId: osmo-test-5`.
- Independently, from Osmosis's side: `osmosisd query ibc channel channels
  --node https://rpc.testnet.osmosis.zone` should show the matching
  `OPEN` channel back to `aether-testnet-1`.

### 7. Keep it running -- don't let it become a one-shot

Osmosis testnet's unbonding period is 24h, much shorter than a real
mainnet's. The client Aether holds for Osmosis needs a trusting period
comfortably under that (12-16h is the usual margin), which means more
than roughly half a day of relayer downtime risks that client
expiring -- recoverable only via a governance-gated client update on
Aether's side, not automatically.

Run it as a persistent service on the seed, mirroring
`aether-explorer.service`'s pattern (see `scripts/deploy-explorer.sh`):

```ini
[Unit]
Description=Aether <-> Osmosis testnet IBC relayer
After=network.target

[Service]
ExecStart=/root/aether-relayer-osmosis \
  -aether-rpc https://rpc.157-245-252-221.sslip.io \
  -aether-grpc grpc.157-245-252-221.sslip.io:443 \
  -aether-chain-id aether-testnet-1 \
  -aether-key relayer -aether-home /root/.aether \
  -aether-gas-prices 0.0001uaeth \
  -cparty-rpc https://rpc.testnet.osmosis.zone \
  -cparty-grpc https://grpc.testnet.osmosis.zone \
  -cparty-chain-id osmo-test-5 -cparty-bech32-prefix osmo \
  -cparty-key relayer -cparty-home /root/.osmosisd-relayer \
  -cparty-gas-prices 0.025uosmo -keyring-backend file
Restart=on-failure
RestartSec=10
User=root

[Install]
WantedBy=multi-user.target
```

`cmd/relayer` currently does one handshake-and-relay pass and exits
(see docs/IBC.md) -- it is not yet a long-running daemon that relays
every subsequent packet on its own. Running it under `Restart=on-failure`
keeps the *client* alive (each run refreshes it) but does not yet give
continuous packet relay; that's a real gap to close before this
connection is useful for more than a demo round trip, not before it's
opened.

## Operational risks specific to this pairing

- **Public testnets reset.** Osmosis testnet has been wiped/upgraded
  before without much notice; if it resets, the connection breaks and
  needs to be rebuilt from step 2. Normal for a testnet, not a sign
  anything on Aether's side is wrong.
- **Client expiry is the main failure mode**, per the trusting-period
  math above -- this is actually a reasonable place to deliberately
  exercise Aether's client-recovery governance path once, since nothing
  of real value is at stake.
- **This explorer only reads Aether's own chain state** (see the `/ibc`
  page's own footer note) -- it can't confirm Osmosis's side or its
  relayer process are healthy. Checking Osmosis's own explorer/CLI for
  the channel state is the only way to see the other half.

## Who to loop in

Gitty operates the seed (root access, already ran `cmd/relayer` there
once for the local-counterparty verification -- see docs/IBC.md and
docs/TLS.md). Steps 2, 4-7 need to happen on or with access to that
box; loop Gitty in for those rather than trying to do them from a
laptop or a sandboxed session.
