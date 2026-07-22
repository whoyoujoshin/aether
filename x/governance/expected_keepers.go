package governance

import (
	"context"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

type BankKeeper interface {
	MintCoins(ctx context.Context, moduleName string, amt sdk.Coins) error
	BurnCoins(ctx context.Context, moduleName string, amt sdk.Coins) error
	SendCoinsFromAccountToModule(ctx context.Context, senderAddr sdk.AccAddress, recipientModule string, amt sdk.Coins) error
	SendCoinsFromModuleToAccount(ctx context.Context, senderModule string, recipientAddr sdk.AccAddress, amt sdk.Coins) error
}

type PowKeeper interface {
	GetValidatorTenureRatio(ctx sdk.Context, minerAddr sdk.AccAddress) math.LegacyDec
	IsActiveValidator(ctx sdk.Context, minerAddr sdk.AccAddress) bool
}