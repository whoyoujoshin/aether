package types

import (
	sdkerrors "cosmossdk.io/errors"
)

var (
	ErrInvalidCreator         = sdkerrors.Register(ModuleName, 1, "invalid creator address")
	ErrInvalidPoW             = sdkerrors.Register(ModuleName, 2, "invalid proof of work")
	ErrInvalidConsensusPubkey = sdkerrors.Register(ModuleName, 3, "invalid consensus public key")
	ErrInvalidProofOfPossession = sdkerrors.Register(ModuleName, 4, "signature does not prove possession of the consensus private key")
	ErrUnknownAncestor = sdkerrors.Register(ModuleName, 5, "prevHash does not match any known recent block")
	ErrStaleAncestor   = sdkerrors.Register(ModuleName, 6, "claimed ancestor height is outside the recency window")
		ErrDuplicateWork   = sdkerrors.Register(ModuleName, 7, "this exact mining header has already been accepted")
	ErrTooManySubmissionsThisBlock = sdkerrors.Register(ModuleName, 8, "a PoW submission has already been accepted at this block height")
	ErrBannedMiner                 = sdkerrors.Register(ModuleName, 9, "this miner is permanently banned and may not submit further work")
	ErrInvalidAuthority            = sdkerrors.Register(ModuleName, 10, "invalid authority for MsgUpdateParams")
	ErrInvalidParamValue           = sdkerrors.Register(ModuleName, 11, "invalid parameter value in MsgUpdateParams")
)
