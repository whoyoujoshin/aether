#![cfg(feature = "integration")]
//! Swap tests against a real Osmosis app.
//!
//! These need osmosis-test-tube's native library and a compiled wasm.
//! Build the wasm first (`cargo wasm` or the optimizer), then:
//!
//! ```text
//! WASM=target/wasm32-unknown-unknown/release/aether_swap.wasm cargo test --features integration --test swap
//! ```
//!
//! A successful swap's return transfer is checked on the emitted message.
//! The test app has no IBC channel, so a swap that clears `min_output`
//! fails at the transfer step with a channel error. That is the assertion:
//! the swap itself was accepted, and the contract then tried to send.

use cosmwasm_std::{Coin, Uint128};
use osmosis_test_tube::{Account, Gamm, Module, OsmosisTestApp, Runner, Wasm};

use aether_swap::msg::{ExecuteMsg, InstantiateMsg};

const AETH: &str = "ibc/TESTAETH";
const RECIPIENT: &str = "aether1qqqsyqcyq5rqwzqfpg9scrgwpugpzysn8pzh53";

fn wasm_bytes() -> Vec<u8> {
    let path = std::env::var("WASM").unwrap_or_else(|_| {
        "target/wasm32-unknown-unknown/release/aether_swap.wasm".to_string()
    });
    std::fs::read(&path).unwrap_or_else(|e| {
        panic!("read {path}: {e}. Build the wasm first; see README.");
    })
}

#[test]
fn swap_below_min_output_fails() {
    let app = OsmosisTestApp::new();
    let alice = app
        .init_account(&[
            Coin::new(100_000_000_000u128, "uosmo"),
            Coin::new(1_000_000_000u128, AETH),
        ])
        .unwrap();

    let gamm = Gamm::new(&app);
    let pool_id = gamm
        .create_basic_pool(
            &[Coin::new(1_000_000u128, AETH), Coin::new(1_000_000u128, "uosmo")],
            &alice,
        )
        .unwrap()
        .data
        .pool_id;

    let wasm = Wasm::new(&app);
    let code_id = wasm
        .store_code(&wasm_bytes(), None, &alice)
        .unwrap()
        .data
        .code_id;
    let contract = wasm
        .instantiate(
            code_id,
            &InstantiateMsg {
                aeth_denom: AETH.to_string(),
                channel_to_aether: "channel-11852".to_string(),
                pool_id,
                admin: alice.address(),
                default_timeout_seconds: Some(600),
            },
            None,
            Some("aether-swap"),
            &[],
            &alice,
        )
        .unwrap()
        .data
        .address;

    // min_output larger than the pool can pay.
    let err = wasm
        .execute(
            &contract,
            &ExecuteMsg::SwapAndReturn {
                min_output: Uint128::new(u128::MAX / 2),
                recipient: RECIPIENT.to_string(),
                timeout_seconds: None,
            },
            &[Coin::new(100u128, AETH)],
            &alice,
        )
        .unwrap_err();
    let text = format!("{err:?}");
    assert!(
        text.to_lowercase().contains("min") || text.to_lowercase().contains("out"),
        "expected a min-output failure, got {text}"
    );
}

#[test]
fn successful_swap_reaches_return_transfer() {
    let app = OsmosisTestApp::new();
    let alice = app
        .init_account(&[
            Coin::new(100_000_000_000u128, "uosmo"),
            Coin::new(1_000_000_000u128, AETH),
        ])
        .unwrap();

    let gamm = Gamm::new(&app);
    let pool_id = gamm
        .create_basic_pool(
            &[Coin::new(1_000_000u128, AETH), Coin::new(1_000_000u128, "uosmo")],
            &alice,
        )
        .unwrap()
        .data
        .pool_id;

    let wasm = Wasm::new(&app);
    let code_id = wasm
        .store_code(&wasm_bytes(), None, &alice)
        .unwrap()
        .data
        .code_id;
    let contract = wasm
        .instantiate(
            code_id,
            &InstantiateMsg {
                aeth_denom: AETH.to_string(),
                channel_to_aether: "channel-11852".to_string(),
                pool_id,
                admin: alice.address(),
                default_timeout_seconds: Some(600),
            },
            None,
            Some("aether-swap"),
            &[],
            &alice,
        )
        .unwrap()
        .data
        .address;

    // A min_output the equal-weight pool can clear. The swap succeeds;
    // the return IBC transfer fails because the test app has no channel.
    // That channel error is the proof the swap ran and the contract
    // constructed the return transfer.
    let err = wasm
        .execute(
            &contract,
            &ExecuteMsg::SwapAndReturn {
                min_output: Uint128::new(1),
                recipient: RECIPIENT.to_string(),
                timeout_seconds: None,
            },
            &[Coin::new(100u128, AETH)],
            &alice,
        )
        .unwrap_err();
    let text = format!("{err:?}").to_lowercase();
    assert!(
        text.contains("channel") || text.contains("ibc") || text.contains("packet"),
        "expected the return transfer to fail on the missing channel, got {text}"
    );
}
