use cosmwasm_std::{DepsMut, Response};

use crate::error::ContractError;
use crate::msg::IBCLifecycleComplete;
use crate::state::{INFLIGHT, OWED};

/// On timeout or an error ack, the OSMO has come back to this contract.
/// Record it as owed to the original recipient. Nobody else can take it.
pub fn handle_lifecycle(
    deps: DepsMut,
    event: IBCLifecycleComplete,
) -> Result<Response, ContractError> {
    let (channel, sequence, failed) = match event {
        IBCLifecycleComplete::IBCTimeout { channel, sequence } => (channel, sequence, true),
        IBCLifecycleComplete::IBCAck {
            channel,
            sequence,
            success,
            ..
        } => (channel, sequence, !success),
    };

    let Some(inflight) = INFLIGHT.may_load(deps.storage, (&channel, sequence))? else {
        // Not our packet. Ack success for a packet we never tracked is a no-op.
        return Ok(Response::new().add_attribute("action", "unknown_packet"));
    };
    INFLIGHT.remove(deps.storage, (&channel, sequence));

    if !failed {
        return Ok(Response::new()
            .add_attribute("action", "return_acked")
            .add_attribute("recipient", inflight.recipient));
    }

    let previous = OWED
        .may_load(deps.storage, &inflight.recipient)?
        .unwrap_or_default();
    let total = previous.checked_add(inflight.amount)?;
    OWED.save(deps.storage, &inflight.recipient, &total)?;

    Ok(Response::new()
        .add_attribute("action", "owed")
        .add_attribute("recipient", inflight.recipient)
        .add_attribute("amount", total))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::msg::IBCLifecycleComplete;
    use crate::state::Inflight;
    use cosmwasm_std::Uint128;
    use cosmwasm_std::testing::mock_dependencies;

    fn record(deps: &mut cosmwasm_std::OwnedDeps<cosmwasm_std::MemoryStorage, cosmwasm_std::testing::MockApi, cosmwasm_std::testing::MockQuerier>) {
        INFLIGHT
            .save(
                deps.as_mut().storage,
                ("channel-11852", 7),
                &Inflight {
                    recipient: "aether1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq0k8jte".to_string(),
                    amount: Uint128::new(50),
                },
            )
            .unwrap();
    }

    #[test]
    fn timeout_records_owed() {
        let mut deps = mock_dependencies();
        record(&mut deps);
        handle_lifecycle(
            deps.as_mut(),
            IBCLifecycleComplete::IBCTimeout {
                channel: "channel-11852".to_string(),
                sequence: 7,
            },
        )
        .unwrap();
        let owed = OWED
            .load(
                deps.as_ref().storage,
                "aether1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq0k8jte",
            )
            .unwrap();
        assert_eq!(owed, Uint128::new(50));
    }

    #[test]
    fn ack_error_records_owed() {
        let mut deps = mock_dependencies();
        record(&mut deps);
        handle_lifecycle(
            deps.as_mut(),
            IBCLifecycleComplete::IBCAck {
                channel: "channel-11852".to_string(),
                sequence: 7,
                ack: "error".to_string(),
                success: false,
            },
        )
        .unwrap();
        assert!(OWED
            .may_load(
                deps.as_ref().storage,
                "aether1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq0k8jte"
            )
            .unwrap()
            .is_some());
    }

    #[test]
    fn ack_success_does_not_owe() {
        let mut deps = mock_dependencies();
        record(&mut deps);
        handle_lifecycle(
            deps.as_mut(),
            IBCLifecycleComplete::IBCAck {
                channel: "channel-11852".to_string(),
                sequence: 7,
                ack: "ok".to_string(),
                success: true,
            },
        )
        .unwrap();
        assert!(OWED
            .may_load(
                deps.as_ref().storage,
                "aether1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq0k8jte"
            )
            .unwrap()
            .is_none());
    }
}
