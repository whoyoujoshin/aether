package treasury

import (
	"context"

	sdkerrors "cosmossdk.io/errors"
	"cosmossdk.io/math"
	"cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

var ErrInsufficientTreasuryBalance = sdkerrors.Register(ModuleName, 1, "treasury balance is insufficient for requested spend")

type BankKeeper interface {
	SendCoinsFromModuleToAccount(ctx context.Context, senderModule string, recipientAddr sdk.AccAddress, amt sdk.Coins) error
}

type Keeper struct {
	cdc        codec.BinaryCodec
	storeKey   types.StoreKey
	bankKeeper BankKeeper
}

func NewKeeper(cdc codec.BinaryCodec, storeKey types.StoreKey, bankKeeper BankKeeper) Keeper {
	return Keeper{
		cdc:        cdc,
		storeKey:   storeKey,
		bankKeeper: bankKeeper,
	}
}

func (k Keeper) FundTreasury(ctx sdk.Context, amount math.Int) {
	if amount.IsZero() {
		return
	}
	current := k.GetTreasuryBalance(ctx)
	newBalance := current.Add(amount)
	ctx.Logger().Info("Treasury funded", "amount", amount.String(), "new_balance", newBalance.String())
	if k.storeKey == nil {
		return
	}
	store := ctx.KVStore(k.storeKey)
	bz, _ := newBalance.MarshalAmino()
	store.Set([]byte("treasury_balance"), bz)
}

func (k Keeper) GetTreasuryBalance(ctx sdk.Context) math.Int {
	if k.storeKey == nil {
		return math.NewInt(0)
	}
	store := ctx.KVStore(k.storeKey)
	bz := store.Get([]byte("treasury_balance"))
	if bz == nil {
		return math.NewInt(0)
	}
	var balance math.Int
	balance.UnmarshalAmino(bz)
	return balance
}

func (k Keeper) Heartbeat(ctx sdk.Context) {
	if k.storeKey == nil {
		return
	}
	ctx.KVStore(k.storeKey).Set([]byte("last_seen_height"), sdk.Uint64ToBigEndian(uint64(ctx.BlockHeight())))
}

// Spend transfers amount from the treasury's own module account to
// recipient, decrementing the tracked balance to match. Returns
// ErrInsufficientTreasuryBalance if the tracked balance can't cover the
// request -- this is the real fix for a bug found live: governance
// previously paid treasury-spend proposals out of its OWN module
// account (the same pool holding pending deposits), which could leave
// insufficient funds for an unrelated deposit refund. Treasury is now
// the single source of truth for spendable funds; governance only
// authorizes spends, it never moves the money itself.
func (k Keeper) Spend(ctx sdk.Context, recipient sdk.AccAddress, amount math.Int) error {
	current := k.GetTreasuryBalance(ctx)
	if current.LT(amount) {
		return sdkerrors.Wrapf(ErrInsufficientTreasuryBalance, "treasury balance %s is less than requested %s", current.String(), amount.String())
	}

	coins := sdk.NewCoins(sdk.NewCoin("aeth", amount))
	if err := k.bankKeeper.SendCoinsFromModuleToAccount(ctx, ModuleName, recipient, coins); err != nil {
		return err
	}

	newBalance := current.Sub(amount)
	if k.storeKey != nil {
		store := ctx.KVStore(k.storeKey)
		bz, _ := newBalance.MarshalAmino()
		store.Set([]byte("treasury_balance"), bz)
	}
	ctx.Logger().Info("Treasury spend executed", "recipient", recipient.String(), "amount", amount.String(), "new_balance", newBalance.String())

	return nil
}