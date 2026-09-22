package governance

import sdkerrors "cosmossdk.io/errors"

var (
	ErrInvalidProposer         = sdkerrors.Register(ModuleName, 1, "invalid proposer address")
	ErrInvalidRecipient        = sdkerrors.Register(ModuleName, 2, "invalid recipient address")
	ErrInvalidDeposit          = sdkerrors.Register(ModuleName, 3, "invalid deposit amount")
	ErrProposalNotFound        = sdkerrors.Register(ModuleName, 4, "proposal not found")
	ErrDepositPeriodEnded      = sdkerrors.Register(ModuleName, 5, "proposal is not in its deposit period")
	ErrNotInVotingPeriod       = sdkerrors.Register(ModuleName, 6, "proposal is not in its voting period")
	ErrInvalidVoteOption       = sdkerrors.Register(ModuleName, 7, "invalid or unspecified vote option")
	ErrInvalidAmount           = sdkerrors.Register(ModuleName, 8, "invalid treasury spend amount")
	ErrInvalidExecuteMsg       = sdkerrors.Register(ModuleName, 9, "invalid or unpackable execute_msg")
	ErrUnroutableProposalMsg   = sdkerrors.Register(ModuleName, 10, "execute_msg has no registered message handler")
	ErrInvalidExecuteMsgSigner = sdkerrors.Register(ModuleName, 11, "execute_msg must have exactly this module's own account as its sole signer")
)
