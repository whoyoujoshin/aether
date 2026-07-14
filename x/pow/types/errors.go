package types

import (
	sdkerrors "cosmossdk.io/errors"
)

var (
	ErrInvalidCreator         = sdkerrors.Register(ModuleName, 1, "invalid creator address")
	ErrInvalidPoW             = sdkerrors.Register(ModuleName, 2, "invalid proof of work")
	ErrInvalidConsensusPubkey = sdkerrors.Register(ModuleName, 3, "invalid consensus public key")
)