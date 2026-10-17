# aether-swap

CosmWasm contract for Osmosis testnet (`osmo-test-5`). Step 5 of
`docs/OSMOSIS-POOL.md`: an agent on Aether sends AETH over IBC with an
ibc-hooks wasm memo naming this contract. The contract swaps AETH for OSMO
in one GAMM pool and sends the OSMO back to the agent's Aether address.
If the swap fails, the execute returns an error, the IBC transfer fails,
and Aether refunds the AETH.

## Versions

The public LCD reports `osmosisd v31.0.0-rc1`
(`https://lcd.testnet.osmosis.zone/cosmos/base/tendermint/v1beta1/node_info`).

Pinned:

| Crate | Version | Why |
|---|---|---|
| `cosmwasm-std` | 2.1.4 | CosmWasm 2.x, what Osmosis v29+ runs |
| `osmosis-std` | 0.27.0 | Latest published bindings. No v31 crate exists. `MsgSwapExactAmountIn` in `poolmanager.v1beta1` is unchanged through v31 |
| `osmosis-test-tube` | 27.0.1 | Matches `osmosis-std` 0.27. The embedded app is Osmosis ~v27, not v31 |

## Incoming memo

The agent sends AETH to Osmosis with:

```json
{
  "wasm": {
    "contract": "<this contract>",
    "msg": {
      "swap_and_return": {
        "min_output": "1000",
        "recipient": "aether1...",
        "timeout_seconds": 600
      }
    }
  }
}
```

`min_output` is required and must be greater than zero. `recipient` must be
a checksum-valid `aether` bech32 address.

## Build

With the optimizer image (matches CosmWasm 2.x):

```bash
docker run --rm -v "$(pwd)":/code \
  --mount type=volume,source="$(basename "$(pwd)")_cache",target=/code/target \
  --mount type=volume,source=registry_cache,target=/usr/local/cargo/registry \
  cosmwasm/optimizer:0.16.1
```

The wasm is written to `artifacts/aether_swap.wasm`.

Or, for local tests:

```bash
rustup target add wasm32-unknown-unknown
cargo wasm
```

## Deploy on osmo-test-5

Upload is permissionless on testnet. The pool id does not exist yet; set it
at instantiate, or instantiate with a placeholder and have the admin call
`update_config`.

```bash
osmosisd tx wasm store artifacts/aether_swap.wasm \
  --from <key> --chain-id osmo-test-5 \
  --node https://rpc.testnet.osmosis.zone:443 \
  --gas auto --gas-adjustment 1.4 --gas-prices 0.05uosmo -y

osmosisd tx wasm instantiate <code_id> '{
  "aeth_denom": "ibc/8277E1557EB379889BA54DFD0F19EDCFF907E4B3414E6B99D098F241CAED3BE1",
  "channel_to_aether": "channel-11852",
  "pool_id": <pool_id>,
  "admin": "<osmo address>",
  "default_timeout_seconds": 600
}' --label aether-swap --no-admin \
  --from <key> --chain-id osmo-test-5 \
  --node https://rpc.testnet.osmosis.zone:443 \
  --gas auto --gas-adjustment 1.4 --gas-prices 0.05uosmo -y
```

## What the admin can do

Change the pool id and the default timeout. Nothing else. Owed OSMO can
only be re-sent to the recipient it is owed to, by anyone calling
`retry_return`.

## Tests

```bash
cargo test --test rules
WASM=artifacts/aether_swap.wasm cargo test --test swap
```

`tests/rules.rs` covers denom, coin count, recipient checksum and prefix,
the timeout-to-owed path, `retry_return` refusing a different recipient,
and the admin being unable to move owed funds. `tests/swap.rs` runs the
swap against a real Osmosis app and asserts a below-`min_output` swap fails.

## Uncertainties

- **osmosis-std 0.27.0 vs Osmosis v31.** No published bindings exist for v31.
  `MsgSwapExactAmountIn` and its response in `poolmanager.v1beta1` have not
  changed in the proto through v31, so the 0.27.0 types are used. If the
  reply data encoding differs on the live chain, `parse_swap_output` will
  return `MissingSwapOutput` and the swap will fail (Aether refunds).
- **Immediate transfer failure.** If the return `IbcMsg::Transfer` fails
  before it is sent (channel missing, packet filter, etc.), the reply
  handler records the OSMO as owed. A later timeout or error ack arrives
  through the ibc-hooks sudo callback and does the same. Both paths are
  covered.
- **Sequence extraction.** The packet sequence is read from the transfer
  reply events, looking for `packet_sequence`, `sequence`, or
  `packet_sequence_number`. If the live Osmosis event uses a different
  key, the reply returns `MissingSequence` and the OSMO is not yet marked
  owed. A follow-up can add the missing key once a real transfer is seen.
- **Pool id at instantiate.** The pool does not exist yet. Instantiate with
  a placeholder and have the admin call `update_config` after step 4, or
  instantiate after the pool is created.
- **ibc-hooks confirmation.** The survey noted that ibc-hooks is documented
  as enabled but was not confirmed through the public LCD. A test memo
  should be sent before this contract is relied on.
