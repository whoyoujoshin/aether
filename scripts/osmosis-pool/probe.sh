#!/usr/bin/env bash
# Read-only. What Osmosis testnet allows, before creating the AETH pool
# (docs/OSMOSIS-POOL.md). Run on the seed, which can reach Osmosis, and
# send the whole output to Claude. POOL_ID=<n> also checks a pool's quote.
set -uo pipefail
LCD=${LCD:-https://lcd.osmotest5.osmosis.zone}
AETH=ibc/0D80A29BCBE8A38AAE75313264A0741092566D4EF5A7FEDAD7F86E12193C2328
RELAYER=osmo1mlv24mz4ygnfnp4x4345kpjyx2mqu99x2ag3rm
POOL_KEY_HOME=${POOL_KEY_HOME:-/root/.osmosisd-pool}

# q <label> <LCD path> <jq filter>: prints the filtered answer, or the raw
# body when the filter fails (an unknown route answers with an error JSON).
q() {
  echo "== $1"
  body=$(curl -sS -m 20 "$LCD$2" 2>&1)
  echo "$body" | jq -c "$3" 2>/dev/null || echo "$body" | head -c 600
  echo
}

echo "== osmosisd here: $(osmosisd version 2>&1 | head -1)"
q "chain" /cosmos/base/tendermint/v1beta1/node_info '{network: .default_node_info.network, version: .application_version.version}'
q "pool creation fee" /osmosis/poolmanager/v1beta1/Params '.params.pool_creation_fee'
q "concentrated liquidity params" /osmosis/concentratedliquidity/v1beta1/params '.params | {authorized_quote_denoms, unrestricted_pool_creator_whitelist, is_permissionless_pool_creation_enabled}'
q "cosmwasm upload and instantiate access" /cosmwasm/wasm/v1/codes/params '.params'
q "AETH supply on Osmosis (uaeth)" "/cosmos/bank/v1beta1/supply/by_denom?denom=$AETH" '.amount'
q "relayer balances (cmd/outbound pays gas from these: keep 100 OSMO)" "/cosmos/bank/v1beta1/balances/$RELAYER" '.balances'
if [ -d "$POOL_KEY_HOME" ]; then
  addr=$(osmosisd keys show aether-pool -a --keyring-backend test --home "$POOL_KEY_HOME" 2>/dev/null)
  q "pool key $addr balances" "/cosmos/bank/v1beta1/balances/$addr" '.balances'
fi
if [ -n "${POOL_ID:-}" ]; then
  q "pool $POOL_ID" "/osmosis/poolmanager/v1beta1/pools/$POOL_ID" '.'
  q "quote: 1 AETH into pool $POOL_ID" "/osmosis/poolmanager/v1beta1/$POOL_ID/estimate/single_pool_swap_exact_amount_in?token_in=1000000$AETH&token_out_denom=uosmo" '.'
fi
