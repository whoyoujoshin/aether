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
	if ctx != nil {
		ctx.Logger().Info("Treasury funded", "amount", amount.String())
	}
	if k.storeKey == nil || ctx == nil {
		return
	}
	store := ctx.KVStore(k.storeKey)
	bz, _ := amount.MarshalAmino()
	store.Set([]byte("treasury_balance"), bz)
}

func (k Keeper) GetTreasuryBalance(ctx sdk.Context) math.Int {
	if k.storeKey == nil || ctx == nil {
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
