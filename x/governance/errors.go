package governance

import sdkerrors "cosmossdk.io/errors"

var (
	ErrInvalidProposer    = sdkerrors.Register(ModuleName, 1, "invalid proposer address")
	ErrInvalidRecipient   = sdkerrors.Register(ModuleName, 2, "invalid recipient address")
	ErrInvalidDeposit     = sdkerrors.Register(ModuleName, 3, "invalid deposit amount")
	ErrProposalNotFound   = sdkerrors.Register(ModuleName, 4, "proposal not found")
	ErrDepositPeriodEnded = sdkerrors.Register(ModuleName, 5, "proposal is not in its deposit period")
)