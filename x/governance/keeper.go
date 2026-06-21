package governance

import (
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
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

func (k Keeper) SubmitProposal(ctx sdk.Context, title string) {
	ctx.Logger().Info("Proposal submitted", "title", title)
}