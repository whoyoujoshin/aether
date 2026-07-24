package treasury

import (
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"cosmossdk.io/math"
	"cosmossdk.io/store/types"
)

type Keeper struct {
	cdc      codec.BinaryCodec
	storeKey types.StoreKey
}

func NewKeeper(cdc codec.BinaryCodec, storeKey types.StoreKey) Keeper {
	return Keeper{
		cdc:      cdc,
		storeKey: storeKey,
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