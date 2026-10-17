use cosmwasm_schema::{cw_serde, QueryResponses};
use cosmwasm_std::{Addr, Uint128};

#[cw_serde]
pub struct InstantiateMsg {
    /// IBC denom of AETH as it appears on Osmosis.
    pub aeth_denom: String,
    /// Osmosis channel back to Aether, e.g. "channel-11852".
    pub channel_to_aether: String,
    pub pool_id: u64,
    pub admin: String,
    /// Defaults to 600 (10 minutes) when omitted.
    pub default_timeout_seconds: Option<u64>,
}

#[cw_serde]
pub enum ExecuteMsg {
    /// Swap the attached AETH for OSMO and send the OSMO back to `recipient`.
    SwapAndReturn {
        min_output: Uint128,
        recipient: String,
        timeout_seconds: Option<u64>,
    },
    /// Re-send OSMO owed to `recipient`. Anyone may call it.
    RetryReturn { recipient: String },
    /// Admin only. May change the pool id and the default timeout. Nothing else.
    UpdateConfig {
        pool_id: Option<u64>,
        default_timeout_seconds: Option<u64>,
    },
}

#[cw_serde]
pub enum IBCLifecycleComplete {
    #[serde(rename = "ibc_ack")]
    IBCAck {
        channel: String,
        sequence: u64,
        ack: String,
        success: bool,
    },
    #[serde(rename = "ibc_timeout")]
    IBCTimeout { channel: String, sequence: u64 },
}

/// Migrations keep the stored config, owed amounts and in-flight returns.
#[cw_serde]
pub struct MigrateMsg {}

#[cw_serde]
pub enum SudoMsg {
    #[serde(rename = "ibc_lifecycle_complete")]
    IBCLifecycleComplete(IBCLifecycleComplete),
}

#[cw_serde]
#[derive(QueryResponses)]
pub enum QueryMsg {
    #[returns(ConfigResponse)]
    Config {},
    #[returns(OwedResponse)]
    Owed { recipient: String },
}

#[cw_serde]
pub struct ConfigResponse {
    pub aeth_denom: String,
    pub channel_to_aether: String,
    pub pool_id: u64,
    pub admin: Addr,
    pub default_timeout_seconds: u64,
}

#[cw_serde]
pub struct OwedResponse {
    pub recipient: String,
    pub amount: Uint128,
}
