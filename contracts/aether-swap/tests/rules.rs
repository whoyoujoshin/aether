//! Unit tests for the rules that do not need a live Osmosis app.
//! The swap itself is covered in tests/swap.rs with osmosis-test-tube.

use cosmwasm_std::{
    coin,
    testing::{message_info, mock_env, MockApi, MockQuerier, MockStorage},
    Addr, CosmosMsg, IbcMsg, OwnedDeps, Uint128,
};

use aether_swap::contract::{execute, instantiate};
use aether_swap::error::ContractError;
use aether_swap::msg::{ExecuteMsg, InstantiateMsg};
use aether_swap::state::{Inflight, CONFIG, INFLIGHT, OWED};
use aether_swap::sudo::handle_lifecycle;
use aether_swap::msg::IBCLifecycleComplete;

const AETH: &str = "ibc/8277E1557EB379889BA54DFD0F19EDCFF907E4B3414E6B99D098F241CAED3BE1";
const CHANNEL: &str = "channel-11852";
/// Valid checksum, HRP aether.
const RECIPIENT: &str = "aether1qqqsyqcyq5rqwzqfpg9scrgwpugpzysn8pzh53";
/// Valid checksum, wrong HRP.
const OSMO: &str = "osmo1qyqszqgpqyqszqgpqyqszqgpqyqszqgp6gjwmw";

fn setup() -> OwnedDeps<MockStorage, MockApi, MockQuerier> {
    let mut deps = OwnedDeps {
        storage: MockStorage::default(),
        api: MockApi::default().with_prefix("osmo"),
        querier: MockQuerier::default(),
        custom_query_type: std::marker::PhantomData,
    };
    instantiate(
        deps.as_mut(),
        mock_env(),
        message_info(&Addr::unchecked(OSMO), &[]),
        InstantiateMsg {
            aeth_denom: AETH.to_string(),
            channel_to_aether: CHANNEL.to_string(),
            pool_id: 1,
            admin: OSMO.to_string(),
            default_timeout_seconds: Some(600),
        },
    )
    .unwrap();
    deps
}

#[test]
fn wrong_denom_is_refused() {
    let mut deps = setup();
    let err = execute(
        deps.as_mut(),
        mock_env(),
        message_info(&Addr::unchecked("anyone"), &[coin(10, "uosmo")]),
        ExecuteMsg::SwapAndReturn {
            min_output: Uint128::new(1),
            recipient: RECIPIENT.to_string(),
            timeout_seconds: None,
        },
    )
    .unwrap_err();
    assert!(matches!(err, ContractError::WrongDenom { .. }));
}

#[test]
fn two_coins_are_refused() {
    let mut deps = setup();
    let err = execute(
        deps.as_mut(),
        mock_env(),
        message_info(&Addr::unchecked("anyone"), &[coin(10, AETH), coin(1, "uosmo")]),
        ExecuteMsg::SwapAndReturn {
            min_output: Uint128::new(1),
            recipient: RECIPIENT.to_string(),
            timeout_seconds: None,
        },
    )
    .unwrap_err();
    assert!(matches!(err, ContractError::WrongCoinCount));
}

#[test]
fn non_aether_recipient_is_refused() {
    let mut deps = setup();
    let err = execute(
        deps.as_mut(),
        mock_env(),
        message_info(&Addr::unchecked("anyone"), &[coin(10, AETH)]),
        ExecuteMsg::SwapAndReturn {
            min_output: Uint128::new(1),
            recipient: OSMO.to_string(),
            timeout_seconds: None,
        },
    )
    .unwrap_err();
    assert!(matches!(err, ContractError::InvalidRecipient { .. }));
}

#[test]
fn bad_checksum_is_refused() {
    let mut deps = setup();
    let err = execute(
        deps.as_mut(),
        mock_env(),
        message_info(&Addr::unchecked("anyone"), &[coin(10, AETH)]),
        ExecuteMsg::SwapAndReturn {
            min_output: Uint128::new(1),
            recipient: "aether1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq".to_string(),
            timeout_seconds: None,
        },
    )
    .unwrap_err();
    assert!(matches!(err, ContractError::InvalidRecipient { .. }));
}

#[test]
fn zero_min_output_is_refused() {
    let mut deps = setup();
    let err = execute(
        deps.as_mut(),
        mock_env(),
        message_info(&Addr::unchecked("anyone"), &[coin(10, AETH)]),
        ExecuteMsg::SwapAndReturn {
            min_output: Uint128::zero(),
            recipient: RECIPIENT.to_string(),
            timeout_seconds: None,
        },
    )
    .unwrap_err();
    assert!(matches!(err, ContractError::ZeroMinOutput));
}

#[test]
fn timeout_then_retry_sends_only_to_that_recipient() {
    let mut deps = setup();
    INFLIGHT
        .save(
            deps.as_mut().storage,
            (CHANNEL, 7),
            &Inflight {
                recipient: RECIPIENT.to_string(),
                amount: Uint128::new(50),
            },
        )
        .unwrap();

    handle_lifecycle(
        deps.as_mut(),
        IBCLifecycleComplete::IBCTimeout {
            channel: CHANNEL.to_string(),
            sequence: 7,
        },
    )
    .unwrap();

    // A different recipient is refused, even by the admin.
    let err = execute(
        deps.as_mut(),
        mock_env(),
        message_info(&Addr::unchecked(OSMO), &[]),
        ExecuteMsg::RetryReturn {
            recipient: OSMO.to_string(),
        },
    )
    .unwrap_err();
    assert!(matches!(err, ContractError::InvalidRecipient { .. }));

    let err = execute(
        deps.as_mut(),
        mock_env(),
        message_info(&Addr::unchecked(OSMO), &[]),
        ExecuteMsg::RetryReturn {
            recipient: "aether1qqqsyqcyq5rqwzqfpg9scrgwpugpzysn8pzh54".to_string(),
        },
    )
    .unwrap_err();
    assert!(matches!(
        err,
        ContractError::NothingOwed { .. } | ContractError::InvalidRecipient { .. }
    ));

    let res = execute(
        deps.as_mut(),
        mock_env(),
        message_info(&Addr::unchecked("anyone"), &[]),
        ExecuteMsg::RetryReturn {
            recipient: RECIPIENT.to_string(),
        },
    )
    .unwrap();

    let msg = &res.messages[0].msg;
    match msg {
        CosmosMsg::Ibc(IbcMsg::Transfer {
            channel_id,
            to_address,
            amount,
            memo,
            ..
        }) => {
            assert_eq!(channel_id, CHANNEL);
            assert_eq!(to_address, RECIPIENT);
            assert_eq!(amount.denom, "uosmo");
            assert_eq!(amount.amount, Uint128::new(50));
            assert!(memo.as_deref().unwrap_or("").contains("ibc_callback"));
        }
        other => panic!("expected IBC transfer, got {other:?}"),
    }
}

#[test]
fn admin_cannot_move_owed_funds() {
    let mut deps = setup();
    OWED
        .save(deps.as_mut().storage, RECIPIENT, &Uint128::new(50))
        .unwrap();

    // Admin can change the pool and the timeout. That is the whole privilege.
    execute(
        deps.as_mut(),
        mock_env(),
        message_info(&Addr::unchecked(OSMO), &[]),
        ExecuteMsg::UpdateConfig {
            pool_id: Some(9),
            default_timeout_seconds: Some(120),
        },
    )
    .unwrap();

    let owed = OWED.load(deps.as_ref().storage, RECIPIENT).unwrap();
    assert_eq!(owed, Uint128::new(50));

    let cfg = CONFIG.load(deps.as_ref().storage).unwrap();
    assert_eq!(cfg.pool_id, 9);
    assert_eq!(cfg.admin, Addr::unchecked(OSMO));

    // A non-admin cannot even do that.
    let err = execute(
        deps.as_mut(),
        mock_env(),
        message_info(&Addr::unchecked("osmo1other"), &[]),
        ExecuteMsg::UpdateConfig {
            pool_id: Some(1),
            default_timeout_seconds: None,
        },
    )
    .unwrap_err();
    assert!(matches!(err, ContractError::Unauthorized));
}

#[test]
fn swap_reply_emits_return_transfer() {
    let mut deps = setup();
    aether_swap::state::PENDING_SWAP
        .save(
            deps.as_mut().storage,
            &aether_swap::state::PendingSwap {
                recipient: RECIPIENT.to_string(),
                timeout_seconds: 600,
            },
        )
        .unwrap();

    // Build a reply whose data is a MsgSwapExactAmountInResponse.
    // We only need the events/attributes path here; the real proto path is
    // covered by the integration test. For the unit test we construct the
    // transfer message directly by calling the same helper the reply uses.
    let cfg = CONFIG.load(deps.as_ref().storage).unwrap();
    let env = mock_env();
    let msg = aether_swap::execute::return_transfer(
        &cfg,
        &env,
        RECIPIENT,
        Uint128::new(42),
        600,
    );
    match msg {
        CosmosMsg::Ibc(IbcMsg::Transfer {
            channel_id,
            to_address,
            amount,
            timeout,
            memo,
        }) => {
            assert_eq!(channel_id, CHANNEL);
            assert_eq!(to_address, RECIPIENT);
            assert_eq!(amount.denom, "uosmo");
            assert_eq!(amount.amount, Uint128::new(42));
            assert!(memo.unwrap_or_default().contains("ibc_callback"));
            // 10-minute default when the caller does not override.
            let expected = env.block.time.plus_seconds(600);
            assert_eq!(timeout.timestamp(), Some(expected));
        }
        other => panic!("expected IBC transfer, got {other:?}"),
    }
}

#[test]
fn return_transfer_failure_fails_the_swap() {
    use aether_swap::contract::reply;
    use aether_swap::state::{PendingSwap, PENDING_SWAP};
    use cosmwasm_std::{Binary, MsgResponse, Reply, ReplyOn, SubMsgResponse, SubMsgResult};
    use osmosis_std::types::osmosis::poolmanager::v1beta1::MsgSwapExactAmountInResponse;

    let mut deps = setup();
    PENDING_SWAP
        .save(
            deps.as_mut().storage,
            &PendingSwap {
                recipient: RECIPIENT.to_string(),
                timeout_seconds: 600,
            },
        )
        .unwrap();
    let out: Binary = MsgSwapExactAmountInResponse {
        token_out_amount: "98".to_string(),
    }
    .into();
    #[allow(deprecated)]
    let res = reply(
        deps.as_mut(),
        mock_env(),
        Reply {
            id: aether_swap::contract::SWAP_REPLY_ID,
            payload: Binary::default(),
            gas_used: 0,
            result: SubMsgResult::Ok(SubMsgResponse {
                events: vec![],
                data: None,
                msg_responses: vec![MsgResponse {
                    type_url: "/osmosis.poolmanager.v1beta1.MsgSwapExactAmountInResponse".to_string(),
                    value: out,
                }],
            }),
        },
    )
    .unwrap();
    // The output is read from msg_responses, and the return transfer replies
    // only on success: a send that fails at once fails the whole execute, so
    // Aether refunds the AETH instead of the OSMO sitting here as owed.
    assert_eq!(res.messages.len(), 1);
    assert_eq!(res.messages[0].reply_on, ReplyOn::Success);
    match &res.messages[0].msg {
        CosmosMsg::Ibc(IbcMsg::Transfer { amount, .. }) => assert_eq!(amount.amount, Uint128::new(98)),
        other => panic!("expected IBC transfer, got {other:?}"),
    }
}

#[test]
fn sequence_comes_from_send_packet_only() {
    use aether_swap::execute::sequence_from_reply;
    use cosmwasm_std::{Binary, Event, Reply, SubMsgResponse, SubMsgResult};

    let reply_with = |events: Vec<Event>| {
        #[allow(deprecated)]
        Reply {
            id: 2,
            payload: Binary::default(),
            gas_used: 0,
            result: SubMsgResult::Ok(SubMsgResponse { events, data: None, msg_responses: vec![] }),
        }
    };
    let other = Event::new("acc_seq").add_attribute("sequence", "3");
    let send = Event::new("send_packet")
        .add_attribute("packet_src_channel", CHANNEL)
        .add_attribute("packet_sequence", "41");
    assert_eq!(sequence_from_reply(&reply_with(vec![other.clone(), send])), Some(41));
    assert_eq!(sequence_from_reply(&reply_with(vec![other])), None);
}

#[test]
fn timeouts_are_bounded() {
    let mut deps = setup();
    for seconds in [0u64, 59, 86_401, u64::MAX] {
        let err = execute(
            deps.as_mut(),
            mock_env(),
            message_info(&Addr::unchecked(OSMO), &[coin(100, AETH)]),
            ExecuteMsg::SwapAndReturn {
                min_output: Uint128::new(1),
                recipient: RECIPIENT.to_string(),
                timeout_seconds: Some(seconds),
            },
        )
        .unwrap_err();
        assert!(matches!(err, ContractError::BadTimeout { .. }), "{seconds}: {err:?}");
    }
    let err = execute(
        deps.as_mut(),
        mock_env(),
        message_info(&Addr::unchecked(OSMO), &[]),
        ExecuteMsg::UpdateConfig { pool_id: None, default_timeout_seconds: Some(0) },
    )
    .unwrap_err();
    assert!(matches!(err, ContractError::BadTimeout { .. }));
}

#[test]
fn migrate_preserves_config_and_owed() {
    let mut deps = setup();
    OWED.save(deps.as_mut().storage, RECIPIENT, &Uint128::new(50))
        .unwrap();
    let before = CONFIG.load(deps.as_ref().storage).unwrap();

    // Instantiate recorded 0.1.0. Simulate an older stored version so the
    // upgrade check passes, then migrate.
    cw2::set_contract_version(
        deps.as_mut().storage,
        aether_swap::contract::CONTRACT_NAME,
        "0.0.1",
    )
    .unwrap();

    aether_swap::contract::migrate(
        deps.as_mut(),
        mock_env(),
        aether_swap::msg::MigrateMsg {},
    )
    .unwrap();

    let after = CONFIG.load(deps.as_ref().storage).unwrap();
    assert_eq!(after.aeth_denom, before.aeth_denom);
    assert_eq!(after.channel_to_aether, before.channel_to_aether);
    assert_eq!(after.pool_id, before.pool_id);
    assert_eq!(after.admin, before.admin);
    assert_eq!(after.default_timeout_seconds, before.default_timeout_seconds);
    assert_eq!(
        OWED.load(deps.as_ref().storage, RECIPIENT).unwrap(),
        Uint128::new(50)
    );

    let version = cw2::get_contract_version(deps.as_ref().storage).unwrap();
    assert_eq!(version.contract, aether_swap::contract::CONTRACT_NAME);
    assert_eq!(version.version, aether_swap::contract::CONTRACT_VERSION);

    // A different contract name is refused.
    cw2::set_contract_version(deps.as_mut().storage, "other", "0.0.1").unwrap();
    let err = aether_swap::contract::migrate(
        deps.as_mut(),
        mock_env(),
        aether_swap::msg::MigrateMsg {},
    )
    .unwrap_err();
    assert!(matches!(err, ContractError::Std(_)));
}
