# AETH pool on Osmosis testnet

A real AETH pool on `osmo-test-5`, so AETH has a market price an agent
can quote and, in a later step, swap against. It uses the IBC path that
reopened on 2026-10-17 (Aether `channel-3` ↔ Osmosis `channel-11852`,
[OSMOSIS-TESTNET.md](OSMOSIS-TESTNET.md)) and needs no change to
Aether's chain code. The `dex/` desk is the reference model for the
tools; this is the real liquidity.

**Status:** planned. Step 1 is read-only; nothing has been created.

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
`cmd/outbound` runs. sync3 needs `osmosisd` for steps 2 and 4; copy the
seed's binary over if it has none.

**Expected, from an outside survey (Grok, 2026-10-16), for the probe to
confirm:**
- **Pool creation fee:** 10 OSMO, read from the live LCDs.
- **CosmWasm uploads:** open to anyone on testnet. Mainnet needs governance.
- **ibc-hooks:** documented as enabled on Osmosis, but not confirmed
  through the public LCD. Step 5 relies on it, so a test memo proves it
  before any contract goes up.
- **Endpoints:** only Osmosis's own, `rpc.testnet` and `rpc.osmotest5`,
  answer. They share a prune point (about 1.87 million blocks kept), so
  they're likely one backend under two names. Polkachu's testnet RPC no
  longer resolves, and Reliable Ninjas' is down.

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
osmosisd keys add aether-pool --keyring-backend test --home /root/.osmosisd-pool 2>&1 | tee /root/.osmosisd-pool-mnemonic.txt
chmod 600 /root/.osmosisd-pool-mnemonic.txt
osmosisd keys show aether-pool -a --keyring-backend test --home /root/.osmosisd-pool
```

## 3. Fund it

**AETH**, over the live channel from the faucet's key (1,000 AETH shown;
the amount is set once the probe is in):

```bash
aetherd tx ibc-transfer transfer transfer channel-3 <osmo1… pool address> 1000000000uaeth \
  --from faucet-v3 --keyring-backend test --home /root/.aether \
  --chain-id aether-testnet-1 --fees 0uaeth --gas auto -y
```

It lands on Osmosis within a minute or two (`cmd/outbound` relays it).

**OSMO:** from Osmosis's testnet faucet in a browser, to the pool
address. Fund the relayer only from what is above its 100 OSMO. Leave
room for the pool creation fee from step 1 and for gas.

## 4. Create the pool

A balancer pool with equal weights and a 0.3% swap fee, the same curve and
fee as the `dex/` desk. `osmosisd` takes the amounts in base units:

```bash
cat > /root/aether-pool.json <<'JSON'
{
  "weights": "1ibc/8277E1557EB379889BA54DFD0F19EDCFF907E4B3414E6B99D098F241CAED3BE1,1uosmo",
  "initial-deposit": "1000000000ibc/8277E1557EB379889BA54DFD0F19EDCFF907E4B3414E6B99D098F241CAED3BE1,<OSMO amount>uosmo",
  "swap-fee": "0.003",
  "exit-fee": "0.000",
  "future-governor": ""
}
JSON
osmosisd tx gamm create-pool --pool-file /root/aether-pool.json \
  --from aether-pool --keyring-backend test --home /root/.osmosisd-pool \
  --chain-id osmo-test-5 --node https://rpc.osmotest5.osmosis.zone:443 \
  --gas auto --gas-adjustment 1.4 --gas-prices 0.05uosmo -y
```

Then find the pool ID and check it quotes:

```bash
osmosisd q tx <txhash> --node https://rpc.osmotest5.osmosis.zone:443 -o json \
  | jq -r '.events[] | select(.type=="pool_created") | .attributes[] | select(.key=="pool_id") | .value'
POOL_ID=<n> bash scripts/osmosis-pool/probe.sh
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
   - **Step 1's upload access decides whether we can deploy our own.**
     Upload is either open to anyone or limited to approved addresses. If
     it's limited, ask Osmosis for an upload, or for Aether to be added
     to that registry.

   Agents spend AETH through this path, so it gets the same caps as every
   other spend (owner-set, per payment and per day). It also gets a
   minimum-to-receive in the memo, so a moved price fails the swap instead
   of filling it badly.

## Risks

- **Testnet resets:** if Osmosis testnet is wiped, the pool goes with it, and the channel is rebuilt first ([OSMOSIS-TESTNET.md](OSMOSIS-TESTNET.md)).
- **Liquidity is ours alone:** the price moves with every trade, so a large swap gets a poor fill. That's the reason for the minimum-to-receive.
- **The path depends on the relayers:** both directions depend on Helicase and `cmd/outbound`. A stalled relay delays a swap's return; it doesn't lose it, because the transfer times out and refunds.
