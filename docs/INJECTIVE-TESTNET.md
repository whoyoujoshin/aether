# Connecting `aether-testnet-1` to Injective testnet (`injective-888`)

Runbook for a direct IBC channel from Aether's live testnet to Injective's
public testnet, so Circle's testnet USDC on Injective
(`erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d`) reaches Aether in
one hop. Written 2026-10-07 for **DardenPC (peer-1)**: Gitty is away, so
everything here runs from Joshua's PC, with no step on the seed.

**Status:** channel open since 2026-10-08 (steps 0 to 4); `outbound.exe`
running on DardenPC (step 5); Helicase on peer-1 following both clients,
on `aetherd-12b2d15.exe` (step 6). First USDC transfer (step 7) still to
do.

| | Aether (`aether-testnet-1`) | Injective (`injective-888`) |
|---|---|---|
| Client | `07-tendermint-2` (of Injective; Helicase on peer-1 updates it) | `07-tendermint-510` (of Aether; `outbound.exe` updates it) |
| Connection | `connection-2` | `connection-416` |
| Channel | `transfer/channel-2` | `transfer/channel-77152` |
| Unbonding | 97h44m (bond cooldown × block time, at opening) | 504h (21 days) |

- **Opened by `relayer.exe`** from DardenPC, Aether tip 241,761, with a
  12,345 uaeth round trip.
- **AETH on Injective:** `ibc/9A6A7B57762D2D5AA66FA261ED02BB721CA0BB05331900BE6CCA2BD7D6590678`.
- **Injective's USDC on Aether:** `ibc/064D82A67318DD30F54A4E17B6E487C2864E8B075A5871D3ACAD7A7129F32C5C`
  (path `transfer/channel-2`, base `erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d`).
- **peer-1's ports:** RPC `127.0.0.1:26667`, gRPC `localhost:9091`, not
  the defaults. The commands below use them.

## Who does what, once it's open

- **Opening it:** `relayer.exe`, once, from DardenPC. It creates a client
  on each chain, the connection and an `ics20-1` channel, then sends
  12,345 uaeth out and back. It signs on Aether with a new ML-DSA
  `relayer` key and on Injective with a new eth_secp256k1 `relayer` key.
- **Onto Aether:** Helicase on peer-1. peer-1 relays Injective's packets
  in the blocks it proposes, about one block in four, so a transfer lands
  within a minute or so. The other three validators don't need anything
  for this; they check and run peer-1's relay transactions like any
  other.
- **Onto Injective:** `outbound.exe` on DardenPC, signing with the
  Injective `relayer` key. It also keeps Injective's client of Aether
  fresh. **If DardenPC is off for longer than that client's trusting
  period (printed in step 4; expect about 3 days), the client expires and
  the channel is dead**, which is what happened to Injective's route
  through Osmosis. Short outages are fine.

| | Aether (`aether-testnet-1`) | Injective testnet (`injective-888`) |
|---|---|---|
| RPC | peer-1's own, `http://127.0.0.1:26667` (check in step 0) | `https://testnet.sentry.tm.injective.network:443` |
| gRPC | peer-1's own, `127.0.0.1:9091` (check in step 0) | `testnet.sentry.chain.grpc.injective.network:443` (TLS) |
| Bech32 prefix | `aether` | `inj` |
| Signing | ML-DSA-44 | eth_secp256k1 (coin type 60) |
| Gas price | `0.0001uaeth` | `160000000inj` (Injective's default; 1 INJ = 10^18 inj) |

The Injective endpoints are the ones Injective's own Go SDK uses.

All commands are PowerShell, run from the repo, `C:\aether-data`. Use
`curl.exe` (not `curl`, which is PowerShell's `Invoke-WebRequest`).
Relayer keys go under `C:\aether-relayer`.

## 0. Check peer-1's ports and indexing

```powershell
Select-String -Path C:\aether-peer1\config\config.toml -Pattern '^laddr|^indexer'
Select-String -Path C:\aether-peer1\config\app.toml -Pattern '^\[grpc\]' -Context 0,6
Select-String -Path C:\aether-peer1\config\app.toml -Pattern 'helicase'
```

- The RPC `laddr` (in `[rpc]`) gives the Aether RPC port, usually
  `tcp://127.0.0.1:26667` on peer-1. If it's different, use that port
  everywhere below instead of 26667.
- `indexer` must be `"kv"`: `outbound.exe` finds packets with
  `tx_search`.
- `[grpc]` must have `enable = true`. Its `address` gives the gRPC port,
  `localhost:9091` on peer-1.
- The last command should print nothing, since peer-1 has no Helicase
  yet. If it prints a `[helicase]` section, send it to Claude before
  step 6.

## 1. Check Injective's endpoints

```powershell
(Invoke-RestMethod https://testnet.sentry.tm.injective.network/status).result.node_info.network
(Invoke-RestMethod 'https://testnet.sentry.tm.injective.network/tx_search?query="tx.height>1"&per_page=1').result.total_count
```

The first must print `injective-888`. The second must print a number,
not an error: Helicase finds packets with `tx_search`. If either fails,
stop and tell Claude; there are other testnet RPCs to try.

## 2. Build from main

```powershell
cd C:\aether-data
git fetch origin; git checkout origin/main
New-Item -ItemType Directory -Force C:\aether-bin, C:\aether-relayer | Out-Null
go build -o C:\aether-bin\relayer.exe .\cmd\relayer
go build -o C:\aether-bin\outbound.exe .\cmd\outbound
go build -o C:\aether-bin\counterpartyd.exe .\cmd\counterpartyd
go build -o C:\aether-bin\aetherd-new.exe .\cmd\aetherd
```

`aetherd-new.exe` is for step 6. It changes nothing in consensus since
peer-1's current build (`2c32e6a`): the only difference in `app/` is the
`--gas auto` fix, which runs only in simulation and which the seed
already runs.

## 3. Make and fund the two relayer keys

```powershell
C:\aether-bin\aetherd-new.exe keys add relayer --keyring-backend test --home C:\aether-relayer\aether
C:\aether-bin\counterpartyd.exe keys add relayer --algo eth_secp256k1 --coin-type 60 --keyring-backend test --home C:\aether-relayer\injective
C:\aether-bin\relayer.exe -show-addresses -aether-home C:\aether-relayer\aether -cparty-home C:\aether-relayer\injective -cparty-bech32-prefix inj
```

Write down both mnemonics. The last command prints the Aether address,
the `inj1…` address and its `0x…` form. (`counterpartyd` shows the
Injective key with a `cparty1…` prefix. It's the same key; use the
`inj1…` address from `relayer.exe`.)

- **Aether:** send the relayer **5 AETH** from your wallet. The
  handshake costs a fraction of that; 12,345 uaeth of it goes out and
  back.
- **INJ:** use Injective's testnet faucet, or Injective Hub on testnet,
  to send testnet INJ to the `inj1…` address. 1 INJ covers thousands of
  relay transactions at the default gas price.
- **USDC:** at Circle's faucet, `faucet.circle.com`, pick USDC on
  Injective Testnet and paste the `inj1…` address (or the `0x…` form, if
  it asks for one). It's for step 7; 10 USDC is plenty.

Keep both keys in the `test` keyring backend: `outbound.exe` runs
unattended and can't type a passphrase. They hold only testnet funds.

## 4. Open the path

```powershell
C:\aether-bin\relayer.exe `
  -aether-rpc http://127.0.0.1:26667 -aether-grpc 127.0.0.1:9091 `
  -aether-chain-id aether-testnet-1 `
  -aether-key relayer -aether-home C:\aether-relayer\aether -aether-gas-prices 0.0001uaeth `
  -cparty-rpc https://testnet.sentry.tm.injective.network:443 `
  -cparty-grpc testnet.sentry.chain.grpc.injective.network:443 `
  -cparty-chain-id injective-888 -cparty-bech32-prefix inj `
  -cparty-key relayer -cparty-home C:\aether-relayer\injective `
  -cparty-gas-prices 160000000inj `
  -keyring-backend test 2>&1 | Tee-Object C:\aether-relayer\injective-handshake.log
```

It prints each client, connection and channel it creates, and ends with
`round trip complete`. **Send the log to Claude.** From it you need:

- **Aether's client of Injective** (`created client 07-tendermint-N on
  aether`; live: `07-tendermint-2`): Helicase's `client-id` in step 6.
- **Injective's client of Aether** (`created client 07-tendermint-M on
  counterparty`): `outbound.exe`'s `-client-id` in step 5.
- **Both channels** (`channel transfer/channel-… on aether` and `… on
  counterparty`). Injective's side is the one USDC is sent over in
  step 7.

If it stops partway, don't rerun it; send the log to Claude first. A
rerun opens a second, separate path.

## 5. Relay onto Injective: `outbound.exe`

```powershell
C:\aether-bin\outbound.exe `
  -aether-rpc http://127.0.0.1:26667 `
  -cparty-rpc https://testnet.sentry.tm.injective.network:443 `
  -cparty-grpc testnet.sentry.chain.grpc.injective.network:443 `
  -cparty-chain-id injective-888 -cparty-bech32-prefix inj `
  -cparty-key relayer -cparty-home C:\aether-relayer\injective `
  -cparty-gas-prices 160000000inj `
  -client-id 07-tendermint-M `
  -refresh-after 1h -listen 127.0.0.1:8096
```

`07-tendermint-M` is Injective's client of Aether from step 4. Leave it
running in its own window. To have it start with Windows, make a Task
Scheduler task that runs this command at logon and restarts on failure.
Check it from another window:

```powershell
Invoke-RestMethod http://127.0.0.1:8096/healthz
```

It should answer, with the time the client expires about three days
out, moving forward each hour.

## 6. Relay onto Aether: Helicase on peer-1

This restarts peer-1, so the validator rule applies: restart only while
the other three are signing, and never run two copies.

**a. Check the other three are signing.** This must print 4:

```powershell
((Invoke-RestMethod http://127.0.0.1:26667/block).result.block.last_commit.signatures | Where-Object block_id_flag -eq 2).Count
```

**b. Add Helicase to `C:\aether-peer1\config\app.toml`.** Put this at
the end of the file. It follows both chains: Osmosis (Aether's client
`07-tendermint-1`) and Injective (`07-tendermint-N` from step 4), in the
same order on both lines:

```toml
[helicase]
counterparty-rpc = "https://rpc.osmotest5.osmosis.zone,https://testnet.sentry.tm.injective.network:443"
client-id = "07-tendermint-1,07-tendermint-N"
aether-rpc = "http://127.0.0.1:26667"
```

`aether-rpc` is peer-1's own RPC. Without it Helicase uses the default
port 26657, and on peer-1 every cycle fails with `connection refused`.

**c. Swap the binary and restart.** peer-1 is started by the scheduled
task "Aether Peer-1", which runs `C:\aether-data\peer1-ensure.ps1` every 5
minutes and at boot. That script names the binary, so put the new one
beside the old under its commit (`aetherd-12b2d15.exe`) and change `$exe`
in the script. The script counts peer-1 as running when port 26667 is in
use or any `aetherd*.exe` runs on `C:\aether-peer1`. (Until 2026-10-08 it
only knew the names `aetherd-205k.exe` and `aetherd.exe`, so while peer-1
ran as `aetherd-2c32e6a.exe` it tried every 5 minutes to start a second
copy, which died on the database lock.) Disable the task, stop peer-1
the way you normally do, and wait until no `aetherd` process is left (`Get-Process aetherd`
shows nothing). Then put `C:\aether-bin\aetherd-new.exe` where the old
binary was (keep the old one as `aetherd-old.exe`) and start peer-1 the
usual way, with `--home C:\aether-peer1`. If peer-1 runs through
`go run` or `aether.ps1`, it builds from `C:\aether-data`, which is
already on the new code; just start it.

**d. Check it's back and signing.** Expect `catching_up` False, the
height moving, and then a count of 1:

```powershell
(Invoke-RestMethod http://127.0.0.1:26667/status).result.sync_info | Select-Object latest_block_height, catching_up
$me = (Invoke-RestMethod http://127.0.0.1:26667/status).result.validator_info.address
((Invoke-RestMethod http://127.0.0.1:26667/block).result.block.last_commit.signatures | Where-Object validator_address -eq $me).Count
```

Its log should show `helicase started` twice, once per client. A few
`helicase cycle failed … connection refused` lines right at startup are
normal: the worker starts before the node's RPC. Repeated failures after
that are not; send them to Claude.

## 7. The first USDC transfer

```powershell
C:\aether-bin\relayer.exe `
  -cparty-rpc https://testnet.sentry.tm.injective.network:443 `
  -cparty-grpc testnet.sentry.chain.grpc.injective.network:443 `
  -cparty-chain-id injective-888 -cparty-bech32-prefix inj `
  -cparty-key relayer -cparty-home C:\aether-relayer\injective `
  -cparty-gas-prices 160000000inj `
  -aether-home C:\aether-relayer\aether -keyring-backend test `
  -send 1000000erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d `
  -send-channel channel-K -send-to <your aether1… wallet address>
```

- `channel-K` is **Injective's** channel to Aether from step 4.
- `1000000` is 1 USDC: it has 6 decimals.
- Copy the denom exactly; it's case-sensitive.
- Leave out `-send-to` to send to the Aether relayer key instead.

It prints `sent packet …` and exits. Helicase on peer-1 brings it onto
Aether the next time peer-1 proposes; `outbound.exe` takes the
acknowledgement back to Injective. Then check that it arrived:

```powershell
C:\aether-bin\aetherd-new.exe query bank balances <the aether1… address> --node tcp://127.0.0.1:26667
```

It shows as an `ibc/…` denom. **Send Claude** the `sent packet` line,
the balance, and the handshake log. Claude then switches the wallet's
USDC default to this one-hop route (`transfer/channel-<Aether's side>`,
base `erc20:0x0C38…`) for the next release.

## If something goes wrong

- **Step 4 fails on Injective with an insufficient-fee error:** the gas
  price changed. Raise `-cparty-gas-prices` (try `500000000inj`) and tell
  Claude before rerunning.
- **`outbound.exe` stops:** restart it. If `/healthz` says the client
  expires within a day, keep it running and tell Claude.
- **peer-1 doesn't come back signing in step 6d:** stop it, put back
  `aetherd-old.exe` and the old `app.toml` (remove the `[helicase]`
  section), start it again, and tell Claude. The other three keep the
  chain going meanwhile.
