package accountauth

import (
	sdkerrors "cosmossdk.io/errors"
)

var (
	ErrInvalidAccount        = sdkerrors.Register(ModuleName, 1, "invalid account address")
	ErrInvalidAuthenticator  = sdkerrors.Register(ModuleName, 2, "invalid authenticator")
	ErrAuthenticatorNotFound = sdkerrors.Register(ModuleName, 3, "authenticator not found")
	ErrNotSessionKey         = sdkerrors.Register(ModuleName, 4, "authenticator is not a session key")
	ErrNotGuardianThreshold  = sdkerrors.Register(ModuleName, 5, "authenticator is not a guardian threshold")
	ErrSessionKeyExpired     = sdkerrors.Register(ModuleName, 6, "session key has expired")
	ErrMsgTypeNotAllowed     = sdkerrors.Register(ModuleName, 7, "message type not allowed for this session key")
	ErrSpendLimitExceeded    = sdkerrors.Register(ModuleName, 8, "session key spend limit exceeded")
	ErrWrongSigner           = sdkerrors.Register(ModuleName, 9, "signer does not match this authenticator")
	ErrInvalidSignature      = sdkerrors.Register(ModuleName, 10, "invalid guardian signature")
	ErrThresholdNotMet       = sdkerrors.Register(ModuleName, 11, "not enough valid, distinct guardian signatures")
	ErrWrongSequence         = sdkerrors.Register(ModuleName, 12, "guardian signatures do not cover the expected sequence")
	ErrWrongMsgSigner        = sdkerrors.Register(ModuleName, 13, "an inner message is not signed as the authorizing account")
)
