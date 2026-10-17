use cosmwasm_std::{
    coin, Coin, CosmosMsg, DepsMut, Env, Event, IbcMsg, IbcTimeout, MessageInfo, Reply, Response,
    SubMsg, SubMsgResult, Uint128,
};
use osmosis_std::types::cosmos::base::v1beta1::Coin as ProtoCoin;
use osmosis_std::types::osmosis::poolmanager::v1beta1::{
    MsgSwapExactAmountIn, MsgSwapExactAmountInResponse, SwapAmountInRoute,
};

use crate::contract::{SWAP_REPLY_ID, TRANSFER_REPLY_ID};
use crate::error::ContractError;
use crate::state::{
    Config, Inflight, PendingSwap, PendingTransfer, CONFIG, INFLIGHT, OSMO_DENOM, OWED,
    PENDING_SWAP, PENDING_TRANSFER,
};

/// Accept exactly one AETH coin, swap it, and (in the reply) send the OSMO back.
pub fn swap_and_return(
    deps: DepsMut,
    env: Env,
    info: MessageInfo,
    min_output: Uint128,
    recipient: String,
    timeout_seconds: Option<u64>,
) -> Result<Response, ContractError> {
    let cfg = CONFIG.load(deps.storage)?;
    let input = exactly_one_aeth(&info.funds, &cfg.aeth_denom)?;
    if min_output.is_zero() {
        return Err(ContractError::ZeroMinOutput);
    }
    validate_aether_address(&recipient)?;

    let timeout_seconds = timeout_seconds.unwrap_or(cfg.default_timeout_seconds);
    PENDING_SWAP.save(
        deps.storage,
        &PendingSwap {
            recipient: recipient.clone(),
            timeout_seconds,
        },
    )?;

    let swap = MsgSwapExactAmountIn {
        sender: env.contract.address.to_string(),
        routes: vec![SwapAmountInRoute {
            pool_id: cfg.pool_id,
            token_out_denom: OSMO_DENOM.to_string(),
        }],
        token_in: Some(ProtoCoin {
            denom: input.denom,
            amount: input.amount.to_string(),
        }),
        token_out_min_amount: min_output.to_string(),
    };

    Ok(Response::new()
        .add_attribute("action", "swap_and_return")
        .add_attribute("recipient", recipient)
        .add_attribute("min_output", min_output)
        .add_submessage(SubMsg::reply_on_success(swap, SWAP_REPLY_ID)))
}

pub fn handle_swap_reply(
    deps: DepsMut,
    env: Env,
    reply: Reply,
) -> Result<Response, ContractError> {
    let pending = PENDING_SWAP
        .may_load(deps.storage)?
        .ok_or(ContractError::NoPendingSwap)?;
    PENDING_SWAP.remove(deps.storage);

    let output = parse_swap_output(&reply)?;
    if output.is_zero() {
        return Err(ContractError::MissingSwapOutput);
    }

    let cfg = CONFIG.load(deps.storage)?;
    PENDING_TRANSFER.save(
        deps.storage,
        &PendingTransfer {
            recipient: pending.recipient.clone(),
            amount: output,
        },
    )?;

    let transfer = return_transfer(
        &cfg,
        &env,
        &pending.recipient,
        output,
        pending.timeout_seconds,
    );

    Ok(Response::new()
        .add_attribute("action", "swap_reply")
        .add_attribute("output", output)
        .add_attribute("recipient", pending.recipient)
        .add_submessage(SubMsg::reply_always(transfer, TRANSFER_REPLY_ID)))
}

pub fn handle_transfer_reply(deps: DepsMut, reply: Reply) -> Result<Response, ContractError> {
    let pending = PENDING_TRANSFER
        .may_load(deps.storage)?
        .ok_or(ContractError::NoPendingTransfer)?;
    PENDING_TRANSFER.remove(deps.storage);

    // Immediate failure of the transfer submessage (channel missing, etc.).
    // The OSMO is already in the contract; record it as owed so only the
    // recipient can recover it. Timeouts and error acks arrive later via sudo.
    if !reply.result.is_ok() {
        let previous = OWED
            .may_load(deps.storage, &pending.recipient)?
            .unwrap_or_default();
        let total = previous.checked_add(pending.amount)?;
        OWED.save(deps.storage, &pending.recipient, &total)?;
        return Ok(Response::new()
            .add_attribute("action", "return_failed_owed")
            .add_attribute("recipient", pending.recipient)
            .add_attribute("amount", total));
    }

    let cfg = CONFIG.load(deps.storage)?;
    let sequence = sequence_from_reply(&reply).ok_or(ContractError::MissingSequence)?;

    INFLIGHT.save(
        deps.storage,
        (&cfg.channel_to_aether, sequence),
        &Inflight {
            recipient: pending.recipient.clone(),
            amount: pending.amount,
        },
    )?;

    Ok(Response::new()
        .add_attribute("action", "return_sent")
        .add_attribute("sequence", sequence.to_string())
        .add_attribute("recipient", pending.recipient)
        .add_attribute("amount", pending.amount))
}

/// Anyone may call this. It re-sends only what is owed to this recipient.
pub fn retry_return(
    deps: DepsMut,
    env: Env,
    recipient: String,
) -> Result<Response, ContractError> {
    validate_aether_address(&recipient)?;
    let owed = OWED
        .may_load(deps.storage, &recipient)?
        .unwrap_or_default();
    if owed.is_zero() {
        return Err(ContractError::NothingOwed {
            recipient: recipient.clone(),
        });
    }
    OWED.remove(deps.storage, &recipient);

    let cfg = CONFIG.load(deps.storage)?;
    let transfer = return_transfer(&cfg, &env, &recipient, owed, cfg.default_timeout_seconds);
    PENDING_TRANSFER.save(
        deps.storage,
        &PendingTransfer {
            recipient: recipient.clone(),
            amount: owed,
        },
    )?;

    Ok(Response::new()
        .add_attribute("action", "retry_return")
        .add_attribute("recipient", recipient)
        .add_attribute("amount", owed)
        .add_submessage(SubMsg::reply_always(transfer, TRANSFER_REPLY_ID)))
}

pub fn update_config(
    deps: DepsMut,
    info: MessageInfo,
    pool_id: Option<u64>,
    default_timeout_seconds: Option<u64>,
) -> Result<Response, ContractError> {
    let mut cfg = CONFIG.load(deps.storage)?;
    if info.sender != cfg.admin {
        return Err(ContractError::Unauthorized);
    }
    if let Some(pool_id) = pool_id {
        cfg.pool_id = pool_id;
    }
    if let Some(seconds) = default_timeout_seconds {
        cfg.default_timeout_seconds = seconds;
    }
    CONFIG.save(deps.storage, &cfg)?;
    Ok(Response::new()
        .add_attribute("action", "update_config")
        .add_attribute("pool_id", cfg.pool_id.to_string())
        .add_attribute("default_timeout_seconds", cfg.default_timeout_seconds.to_string()))
}

pub fn return_transfer(
    cfg: &Config,
    env: &Env,
    recipient: &str,
    amount: Uint128,
    timeout_seconds: u64,
) -> CosmosMsg {
    CosmosMsg::Ibc(IbcMsg::Transfer {
        channel_id: cfg.channel_to_aether.clone(),
        to_address: recipient.to_string(),
        amount: coin(amount.u128(), OSMO_DENOM),
        timeout: IbcTimeout::with_timestamp(env.block.time.plus_seconds(timeout_seconds)),
        // Registers this contract for the ibc-hooks sudo callback.
        memo: Some(format!(
            r#"{{"ibc_callback":"{}"}}"#,
            env.contract.address
        )),
    })
}

fn exactly_one_aeth(funds: &[Coin], aeth_denom: &str) -> Result<Coin, ContractError> {
    if funds.len() != 1 {
        return Err(ContractError::WrongCoinCount);
    }
    let coin = &funds[0];
    if coin.denom != aeth_denom {
        return Err(ContractError::WrongDenom {
            expected: aeth_denom.to_string(),
            got: coin.denom.clone(),
        });
    }
    if coin.amount.is_zero() {
        return Err(ContractError::ZeroAmount);
    }
    Ok(coin.clone())
}

/// Checksum and HRP. `addr_validate` only accepts the local (osmo) prefix.
pub fn validate_aether_address(recipient: &str) -> Result<(), ContractError> {
    match bech32::decode(recipient) {
        Ok((hrp, _)) if hrp.as_str() == crate::state::AETHER_HRP => Ok(()),
        Ok((hrp, _)) => Err(ContractError::InvalidRecipient {
            recipient: recipient.to_string(),
            reason: format!("prefix is {hrp}, want aether"),
        }),
        Err(e) => Err(ContractError::InvalidRecipient {
            recipient: recipient.to_string(),
            reason: e.to_string(),
        }),
    }
}

fn parse_swap_output(reply: &Reply) -> Result<Uint128, ContractError> {
    let SubMsgResult::Ok(resp) = &reply.result else {
        return Err(ContractError::MissingSwapOutput);
    };
    let Some(data) = &resp.data else {
        return Err(ContractError::MissingSwapOutput);
    };
    let parsed: MsgSwapExactAmountInResponse = data
        .clone()
        .try_into()
        .map_err(|e: cosmwasm_std::StdError| ContractError::Std(e))?;
    parsed
        .token_out_amount
        .parse::<u128>()
        .map(Uint128::new)
        .map_err(|_| ContractError::MissingSwapOutput)
}

fn sequence_from_reply(reply: &Reply) -> Option<u64> {
    let SubMsgResult::Ok(resp) = &reply.result else {
        return None;
    };
    for event in &resp.events {
        if let Some(seq) = sequence_from_event(event) {
            return Some(seq);
        }
    }
    None
}

fn sequence_from_event(event: &Event) -> Option<u64> {
    for attr in &event.attributes {
        if matches!(
            attr.key.as_str(),
            "packet_sequence" | "sequence" | "packet_sequence_number"
        ) {
            if let Ok(n) = attr.value.parse::<u64>() {
                return Some(n);
            }
        }
    }
    None
}
