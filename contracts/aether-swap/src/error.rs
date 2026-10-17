use cosmwasm_std::{OverflowError, StdError};
use thiserror::Error;

#[derive(Error, Debug, PartialEq)]
pub enum ContractError {
    #[error("{0}")]
    Std(#[from] StdError),

    #[error("{0}")]
    Overflow(#[from] OverflowError),

    #[error("exactly one coin is required")]
    WrongCoinCount,

    #[error("expected denom {expected}, got {got}")]
    WrongDenom { expected: String, got: String },

    #[error("amount must be greater than zero")]
    ZeroAmount,

    #[error("min_output must be greater than zero")]
    ZeroMinOutput,

    #[error("recipient {recipient} is not a valid aether bech32 address: {reason}")]
    InvalidRecipient { recipient: String, reason: String },

    #[error("no swap reply in progress")]
    NoPendingSwap,

    #[error("no transfer reply in progress")]
    NoPendingTransfer,

    #[error("swap reply had no output amount")]
    MissingSwapOutput,

    #[error("return transfer reply did not include a packet sequence")]
    MissingSequence,

    #[error("nothing owed to {recipient}")]
    NothingOwed { recipient: String },

    #[error("timeout_seconds {seconds} is outside {min}..={max}")]
    BadTimeout { seconds: u64, min: u64, max: u64 },

    #[error("unauthorized")]
    Unauthorized,

    #[error("unknown inflight packet {channel}/{sequence}")]
    UnknownPacket { channel: String, sequence: u64 },
}
