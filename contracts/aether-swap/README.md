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

The transfer's receiver on Osmosis must be this contract's address too:
ibc-hooks runs the memo only when the receiver and `wasm.contract` match.

`min_output` is required and must be greater than zero. `recipient` must be
a checksum-valid `aether` bech32 address. `timeout_seconds` (for the OSMO's
trip back) defaults to the config's and must be 60 to 86,400.

If anything fails before the OSMO leaves (the swap clears less than
`min_output`, the return transfer can't be sent), the execute fails, ibc-hooks
acknowledges the packet with an error, and Aether refunds the AETH. Once the
return transfer is sent, a timeout or error ack on it comes back through the
ibc-hooks callback and the OSMO is recorded as owed to `recipient`.

## Build

Never build on a validator. On 2026-10-17 rustc took about 3 GB compiling
osmosis-std on sync3 (3.9 GB, no swap); it stopped signing and, with
peer-1 out, the chain halted for 27 minutes. Build on a workstation and
copy the `.wasm` over.

The chain's wasm VM refuses the bulk-memory opcodes Rust 1.82 and later emit,
so build with Rust 1.81 (`Cargo.lock` is resolved for it) or the optimizer.

With the optimizer image (matches CosmWasm 2.x):

```bash
docker run --rm -v "$(pwd)":/code \
  --mount type=volume,source="$(basename "$(pwd)")_cache",target=/code/target \
  --mount type=volume,source=registry_cache,target=/usr/local/cargo/registry \
  cosmwasm/optimizer:0.16.1
```

The wasm is written to `artifacts/aether_swap.wasm`.

Or, for local tests (`cargo wasm` is an alias in `.cargo/config.toml`):

```bash
rustup toolchain install 1.81.0 --profile minimal --target wasm32-unknown-unknown
RUSTFLAGS='-C link-arg=-s' cargo +1.81.0 wasm
```

## Deploy on osmo-test-5

Upload and instantiate are open to everybody on osmo-test-5 (the probe of
2026-10-17). Create the pool first (`docs/OSMOSIS-POOL.md` step 4) and pass
its id at instantiate. Keep a wasm admin on testnet (`--admin`), so the code
can be migrated if Osmosis v31 needs a fix; mainnet would use `--no-admin`.

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
}' --label aether-swap --admin <osmo address> \
  --from <key> --chain-id osmo-test-5 \
  --node https://rpc.testnet.osmosis.zone:443 \
  --gas auto --gas-adjustment 1.4 --gas-prices 0.05uosmo -y
```

## Migrate

The wasm admin (the pool key on testnet) stores the new code, then
`osmosisd tx wasm migrate <contract> <new code id> '{}'`. `migrate` keeps
the config, owed amounts and in-flight returns. Code 13375 has no
`migrate`, which doesn't matter: a migration runs the new code's.

On osmo-test-5 the contract is
`osmo14qlp2rly8gy322slcs0tvuus5e5rc4390llet43mw23reg0vdnfs0nv5hn`, migrated
from code 13375 to 13376 on 2026-10-17; its first swap from Aether came
back in about 22 s (`docs/OSMOSIS-POOL.md`).

## What the admin can do

Change the pool id and the default timeout. Nothing else. Owed OSMO can
only be re-sent to the recipient it is owed to, by anyone calling
`retry_return`. The wasm admin (`--admin`) can migrate the code, which is
why it stays on testnet only.

## Tests

```bash
cargo test --locked --lib --test rules
WASM=target/wasm32-unknown-unknown/release/aether_swap.wasm cargo test --features integration --test swap
```

`tests/swap.rs` builds Osmosis from source, so it sits behind the
`integration` feature and CI (`.github/workflows/contract.yml`) skips it.
CI runs the rules tests, builds the wasm with Rust 1.81 and checks it with
`cosmwasm-check` 2.1.4, which rejects a bulk-memory build.

`tests/rules.rs` covers denom, coin count, recipient checksum and prefix,
the timeout-to-owed path, `retry_return` refusing a different recipient,
the admin being unable to move owed funds, timeout bounds, the sequence read
from `send_packet`, and a return transfer that replies only on success.
`tests/swap.rs` runs the swap against a real Osmosis app (test-tube 27):
a below-`min_output` swap fails, and a swap whose return can't be sent (the
test app has no channel) fails as a whole.

## Uncertainties

- **osmosis-std 0.27.0 vs Osmosis v31.** No published bindings exist for v31.
  `MsgSwapExactAmountIn` and its response in `poolmanager.v1beta1` have not
  changed in the proto through v31, so the 0.27.0 types are used. If the
  reply data encoding differs on the live chain, `parse_swap_output` will
  return `MissingSwapOutput` and the swap will fail (Aether refunds). It
  reads `msg_responses` (CosmWasm 2 chains) first, then the older `data`.
- **Sequence extraction.** The packet sequence is read from the
  `MsgTransferResponse` in the reply's `msg_responses` (then `data`).
  Never from events: wasmd 0.53 (Osmosis v31) gives a non-wasm
  submessage's reply no events. The first deploy (code 13375) read
  `send_packet`, so its first real swap failed with `MissingSequence`
  and Aether refunded the AETH.
- **ibc-hooks** is confirmed on: a memo to an address with no contract,
  sent from Aether on 2026-10-17, was acknowledged with "no such contract"
  and refunded on Aether within about 15 s.
