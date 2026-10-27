package inflowcap

import (
	sdkerrors "cosmossdk.io/errors"
)

var (
	ErrInvalidAuthority = sdkerrors.Register(ModuleName, 1, "invalid authority for MsgUpdateParams")
	ErrInvalidParams    = sdkerrors.Register(ModuleName, 2, "invalid inflow cap params")
	ErrNotListed        = sdkerrors.Register(ModuleName, 3, "this token may not come in over this channel")
	ErrOverCap          = sdkerrors.Register(ModuleName, 4, "transfer would take the token over its inflow cap")
	ErrBadPacket        = sdkerrors.Register(ModuleName, 5, "transfer refused by the inflow cap")
	ErrSettlementGrew   = sdkerrors.Register(ModuleName, 6, "settling a send would leave more of its voucher than it held")
)
