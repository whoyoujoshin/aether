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

func (k Keeper) SetVotingPeriod(ctx sdk.Context, votingPeriod int64) {
	if k.storeKey == nil {
		return
	}
	store := ctx.KVStore(k.storeKey)
	bz := sdk.Uint64ToBigEndian(uint64(votingPeriod))
	store.Set([]byte("voting_period"), bz)
}

func (k Keeper) GetVotingPeriod(ctx sdk.Context) int64 {
	if k.storeKey == nil {
		return 0
	}
	store := ctx.KVStore(k.storeKey)
	bz := store.Get([]byte("voting_period"))
	if bz == nil {
		return 0
	}
	return int64(sdk.BigEndianToUint64(bz))
}

func (k Keeper) Heartbeat(ctx sdk.Context) {
	if k.storeKey == nil {
		return
	}
	ctx.KVStore(k.storeKey).Set([]byte("last_seen_height"), sdk.Uint64ToBigEndian(uint64(ctx.BlockHeight())))
}