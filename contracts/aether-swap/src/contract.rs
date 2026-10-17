use cosmwasm_std::{
    entry_point, to_json_binary, Binary, Deps, DepsMut, Env, MessageInfo, Reply, Response,
    StdResult,
};

use crate::error::ContractError;
use crate::execute::{retry_return, swap_and_return, update_config};
use crate::msg::{ConfigResponse, ExecuteMsg, InstantiateMsg, OwedResponse, QueryMsg, SudoMsg};
use crate::state::{Config, CONFIG, DEFAULT_TIMEOUT_SECONDS, OWED};
use crate::sudo::handle_lifecycle;

pub const SWAP_REPLY_ID: u64 = 1;
pub const TRANSFER_REPLY_ID: u64 = 2;

#[entry_point]
pub fn instantiate(
    deps: DepsMut,
    _env: Env,
    _info: MessageInfo,
    msg: InstantiateMsg,
) -> Result<Response, ContractError> {
    if msg.aeth_denom.is_empty() || msg.channel_to_aether.is_empty() {
        return Err(ContractError::Std(cosmwasm_std::StdError::generic_err(
            "aeth_denom and channel_to_aether are required",
        )));
    }
    let admin = deps.api.addr_validate(&msg.admin)?;
    CONFIG.save(
        deps.storage,
        &Config {
            aeth_denom: msg.aeth_denom,
            channel_to_aether: msg.channel_to_aether,
            pool_id: msg.pool_id,
            admin,
            default_timeout_seconds: msg
                .default_timeout_seconds
                .unwrap_or(DEFAULT_TIMEOUT_SECONDS),
        },
    )?;
    Ok(Response::new().add_attribute("action", "instantiate"))
}

#[entry_point]
pub fn execute(
    deps: DepsMut,
    env: Env,
    info: MessageInfo,
    msg: ExecuteMsg,
) -> Result<Response, ContractError> {
    match msg {
        ExecuteMsg::SwapAndReturn {
            min_output,
            recipient,
            timeout_seconds,
        } => swap_and_return(deps, env, info, min_output, recipient, timeout_seconds),
        ExecuteMsg::RetryReturn { recipient } => retry_return(deps, env, recipient),
        ExecuteMsg::UpdateConfig {
            pool_id,
            default_timeout_seconds,
        } => update_config(deps, info, pool_id, default_timeout_seconds),
    }
}

#[entry_point]
pub fn reply(deps: DepsMut, env: Env, msg: Reply) -> Result<Response, ContractError> {
    match msg.id {
        SWAP_REPLY_ID => crate::execute::handle_swap_reply(deps, env, msg),
        TRANSFER_REPLY_ID => crate::execute::handle_transfer_reply(deps, msg),
        id => Err(ContractError::Std(cosmwasm_std::StdError::generic_err(
            format!("unknown reply id {id}"),
        ))),
    }
}

#[entry_point]
pub fn sudo(deps: DepsMut, _env: Env, msg: SudoMsg) -> Result<Response, ContractError> {
    match msg {
        SudoMsg::IBCLifecycleComplete(event) => handle_lifecycle(deps, event),
    }
}

#[entry_point]
pub fn query(deps: Deps, _env: Env, msg: QueryMsg) -> StdResult<Binary> {
    match msg {
        QueryMsg::Config {} => {
            let cfg = CONFIG.load(deps.storage)?;
            to_json_binary(&ConfigResponse {
                aeth_denom: cfg.aeth_denom,
                channel_to_aether: cfg.channel_to_aether,
                pool_id: cfg.pool_id,
                admin: cfg.admin,
                default_timeout_seconds: cfg.default_timeout_seconds,
            })
        }
        QueryMsg::Owed { recipient } => {
            let amount = OWED.may_load(deps.storage, &recipient)?.unwrap_or_default();
            to_json_binary(&OwedResponse { recipient, amount })
        }
    }
}
