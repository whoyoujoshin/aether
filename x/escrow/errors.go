package escrow

import (
	sdkerrors "cosmossdk.io/errors"
)

var (
	ErrInvalidEscrow   = sdkerrors.Register(ModuleName, 1, "invalid escrow")
	ErrInvalidDeadline = sdkerrors.Register(ModuleName, 2, "invalid escrow deadline")
	ErrTooManyOpen     = sdkerrors.Register(ModuleName, 3, "too many open escrows for this payer")
	ErrNotFound        = sdkerrors.Register(ModuleName, 4, "escrow not found (it may already be settled)")
	ErrNotAllowed      = sdkerrors.Register(ModuleName, 5, "this account may not settle this escrow that way")
	ErrInvalidAddress  = sdkerrors.Register(ModuleName, 6, "invalid address")
)
