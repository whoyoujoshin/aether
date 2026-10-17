# AETH pool on Osmosis testnet

A real AETH pool on `osmo-test-5`, so AETH has a market price an agent
can quote and, in a later step, swap against. It uses the IBC path that
reopened on 2026-10-17 (Aether `channel-3` ↔ Osmosis `channel-11852`,
[OSMOSIS-TESTNET.md](OSMOSIS-TESTNET.md)) and needs no change to
Aether's chain code. The `dex/` desk is the reference model for the
tools; this is the real liquidity.

**Status:** probed 2026-10-17 on sync3; the pool is next (step 2). Nothing
has been created on Osmosis yet.

## The pair: AETH / OSMO

| | Denom on Osmosis |
|---|---|
| AETH | `ibc/8277E1557EB379889BA54DFD0F19EDCFF907E4B3414E6B99D098F241CAED3BE1` |
| OSMO | `uosmo` |

**Not USDC:**
- **Aether's testnet USDC** comes from Injective over `channel-2`. It isn't on Osmosis, and Injective's clients of Osmosis have expired.
- **Noble's testnet USDC** is going away with Noble's testnet.

OSMO is on every Osmosis testnet account and comes from Osmosis's faucet. An AETH/USDC pool can follow once a USDC reaches Osmosis that Aether also holds.

The price is a testnet price: whatever ratio the first deposit sets.

Every step that talks to Osmosis runs on **sync3**: since 2026-10-16
Osmosis's testnet endpoints refuse the seed, and sync3 is where
`cmd/outbound` runs. sync3 has `osmosisd` v31.0.3 at `/root/osmosisd`
(not on `PATH`), and reaches Osmosis at `https://rpc.testnet.osmosis.zone:443`.
The AETH send in step 3 is an Aether transaction, so it runs on the seed,
where `faucet-v3`'s key is.

**What the probe found (sync3, 2026-10-17):**
- **Osmosis:** `v31.0.0-rc1`.
- **Pool creation fee:** 1 OSMO (`1000000uosmo`). An outside survey (Grok,
  2026-10-16) had said 10.
- **Concentrated-liquidity pools:** anyone may create one.
- **CosmWasm:** upload and instantiate are open to everybody.
- **AETH on Osmosis:** none yet (supply 0 of the new denom).
- **The relayer** (`osmo1mlv24mz4ygnfnp4x4345kpjyx2mqu99x2ag3rm`) holds
  about 195 OSMO; keep at least 100 there for `cmd/outbound`'s gas.
- **ibc-hooks is on.** Gitty sent 1000uaeth from Aether over `channel-3`
  with a wasm memo to an address with no contract (Aether tx `95798716…EC0C`,
  height 325,271). Osmosis ran the memo, acknowledged with the error "no
  such contract", and Aether refunded the transfer within about 15 s. That
  is the behaviour step 5 relies on: a failed swap refunds the AETH.

## 1. Probe (read-only, on sync3)

It needs only `curl` and `jq`, so sync3 doesn't need the repo:

```bash
curl -fsSL https://raw.githubusercontent.com/whoyoujoshin/aether/main/scripts/osmosis-pool/probe.sh -o /root/osmosis-probe.sh
bash /root/osmosis-probe.sh 2>&1 | tee /root/osmosis-probe-$(date +%Y%m%d).log
```

**Send Claude the whole output.** It shows what decides the next steps:
- **The pool creation fee:** it may be more OSMO than we hold.
- **Who may upload contracts:** this decides the swap path in step 5.
- **AETH already on Osmosis.**
- **The relayer's OSMO:** `cmd/outbound` pays its gas from this, so keep at least 100 OSMO there.

## 2. A key for the pool

Keep the pool's funds apart from the relayer's gas money:

```bash
/root/osmosisd keys add aether-pool --keyring-backend test --home /root/.osmosisd-pool 2>&1 | tee /root/.osmosisd-pool-mnemonic.txt
chmod 600 /root/.osmosisd-pool-mnemonic.txt
/root/osmosisd keys show aether-pool -a --keyring-backend test --home /root/.osmosisd-pool
```

## 3. Fund it

**AETH, 100 AETH, on the seed** from the faucet's key, over the live
channel. Check `faucet-v3` first and keep most of it for the faucet:

```bash
aetherd q bank balances $(aetherd keys show faucet-v3 -a --keyring-backend test --home /root/.aether) --node tcp://127.0.0.1:26657
aetherd tx ibc-transfer transfer transfer channel-3 <osmo1… pool address> 100000000uaeth \
  --from faucet-v3 --keyring-backend test --home /root/.aether \
  --chain-id aether-testnet-1 --fees 0uaeth --gas auto -y
```

Helicase relays it onto Osmosis within a minute or two.

**OSMO, 50 OSMO, on sync3**, from the relayer, which keeps about 145 (or
from Osmosis's testnet faucet in a browser):

```bash
/root/osmosisd tx bank send relayer <osmo1… pool address> 50000000uosmo \
  --keyring-backend test --home /root/.osmosisd-relayer \
  --chain-id osmo-test-5 --node https://rpc.testnet.osmosis.zone:443 \
  --gas auto --gas-adjustment 1.4 --gas-prices 0.05uosmo -y
```

Then check both arrived:

```bash
/root/osmosisd q bank balances <osmo1… pool address> --node https://rpc.testnet.osmosis.zone:443
```

## 4. Create the pool

A balancer pool with equal weights and a 0.3% swap fee, the same curve and
fee as the `dex/` desk. The first deposit sets the price: 100 AETH against
40 OSMO makes 1 AETH = 0.4 OSMO, and leaves the pool key about 10 OSMO
for the 1 OSMO creation fee and gas. `osmosisd` takes the amounts in base
units:

```bash
cat > /root/aether-pool.json <<'JSON'
{
  "weights": "1ibc/8277E1557EB379889BA54DFD0F19EDCFF907E4B3414E6B99D098F241CAED3BE1,1uosmo",
  "initial-deposit": "100000000ibc/8277E1557EB379889BA54DFD0F19EDCFF907E4B3414E6B99D098F241CAED3BE1,40000000uosmo",
  "swap-fee": "0.003",
  "exit-fee": "0.000",
  "future-governor": ""
}
JSON
/root/osmosisd tx gamm create-pool --pool-file /root/aether-pool.json \
  --from aether-pool --keyring-backend test --home /root/.osmosisd-pool \
  --chain-id osmo-test-5 --node https://rpc.testnet.osmosis.zone:443 \
  --gas auto --gas-adjustment 1.4 --gas-prices 0.05uosmo -y
```

Then find the pool ID and check it quotes:

```bash
/root/osmosisd q tx <txhash> --node https://rpc.testnet.osmosis.zone:443 -o json \
  | jq -r '.events[] | select(.type=="pool_created") | .attributes[] | select(.key=="pool_id") | .value'
PATH=/root:$PATH POOL_ID=<n> bash /root/osmosis-probe.sh
```

**Send Claude the pool ID and the probe's last two sections.** If
`create-pool` is refused because new balancer pools are closed, stop
there. The fallback is a concentrated-liquidity pool with one
full-range position, and Claude will write those commands.

## 5. Agent access

Two parts, in this order:

1. **Quote (read-only).** A tool on the public `/mcp`, the test wallet and
   `agentmcp`. It reads the pool's reserves and Osmosis's own swap estimate
   from Osmosis's API. Nothing to sign, so this ships first, built against
   the real responses from step 4.
2. **Swap from Aether.** The agent sends AETH over `channel-3` with a memo.
   On Osmosis, a contract swaps it in the pool and sends the OSMO back to
   the agent's Aether address. If the swap fails, the transfer fails and
   the AETH is refunded on Aether. Osmosis runs such memos through its
   IBC hooks. The contract has to be one we deploy:
   - **Osmosis's own crosschain-swaps contract won't work for us.** It
     looks up channels and address prefixes in a registry that Osmosis
     maintains, and Aether isn't in it.
   - **We can deploy our own:** the probe found upload and instantiate
     open to everybody, and the ibc-hooks test above showed Osmosis runs
     the memo and Aether refunds a failed one.

   Agents spend AETH through this path, so it gets the same caps as every
   other spend (owner-set, per payment and per day). It also gets a
   minimum-to-receive in the memo, so a moved price fails the swap instead
   of filling it badly.

## Risks

- **Testnet resets:** if Osmosis testnet is wiped, the pool goes with it, and the channel is rebuilt first ([OSMOSIS-TESTNET.md](OSMOSIS-TESTNET.md)).
- **Liquidity is ours alone:** the price moves with every trade, so a large swap gets a poor fill. That's the reason for the minimum-to-receive.
- **The path depends on the relayers:** both directions depend on Helicase and `cmd/outbound`. A stalled relay delays a swap's return; it doesn't lose it, because the transfer times out and refunds.
