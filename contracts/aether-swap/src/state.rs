use cosmwasm_schema::cw_serde;
use cosmwasm_std::{Addr, Uint128};
use cw_storage_plus::{Item, Map};

pub const CONFIG: Item<Config> = Item::new("config");

/// One in-flight swap. Cleared when the swap reply is handled.
pub const PENDING_SWAP: Item<PendingSwap> = Item::new("pending_swap");

/// One in-flight return transfer, waiting for its reply to learn the sequence.
pub const PENDING_TRANSFER: Item<PendingTransfer> = Item::new("pending_transfer");

/// Keyed by (channel, sequence). Moved to OWED on timeout or ack error.
pub const INFLIGHT: Map<(&str, u64), Inflight> = Map::new("inflight");

/// OSMO owed to an Aether recipient after a failed return transfer.
pub const OWED: Map<&str, Uint128> = Map::new("owed");

#[cw_serde]
pub struct Config {
    pub aeth_denom: String,
    pub channel_to_aether: String,
    pub pool_id: u64,
    pub admin: Addr,
    pub default_timeout_seconds: u64,
}

#[cw_serde]
pub struct PendingSwap {
    pub recipient: String,
    pub timeout_seconds: u64,
}

#[cw_serde]
pub struct PendingTransfer {
    pub recipient: String,
    pub amount: Uint128,
}

#[cw_serde]
pub struct Inflight {
    pub recipient: String,
    pub amount: Uint128,
}

pub const DEFAULT_TIMEOUT_SECONDS: u64 = 600;
pub const MIN_TIMEOUT_SECONDS: u64 = 60;
pub const MAX_TIMEOUT_SECONDS: u64 = 86_400;
pub const OSMO_DENOM: &str = "uosmo";
pub const AETHER_HRP: &str = "aether";
