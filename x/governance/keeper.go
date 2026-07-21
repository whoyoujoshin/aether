package governance

import (
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"cosmossdk.io/store/types"
	storetypes "cosmossdk.io/store/types"
)

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

func (k Keeper) SetMinDeposit(ctx sdk.Context, minDeposit int64) {
	if k.storeKey == nil {
		return
	}
	store := ctx.KVStore(k.storeKey)
	store.Set([]byte("min_deposit"), sdk.Uint64ToBigEndian(uint64(minDeposit)))
}

func (k Keeper) GetMinDeposit(ctx sdk.Context) int64 {
	if k.storeKey == nil {
		return 0
	}
	store := ctx.KVStore(k.storeKey)
	bz := store.Get([]byte("min_deposit"))
	if bz == nil {
		return 0
	}
	return int64(sdk.BigEndianToUint64(bz))
}

func (k Keeper) SetDepositPeriod(ctx sdk.Context, depositPeriod int64) {
	if k.storeKey == nil {
		return
	}
	store := ctx.KVStore(k.storeKey)
	store.Set([]byte("deposit_period"), sdk.Uint64ToBigEndian(uint64(depositPeriod)))
}

func (k Keeper) GetDepositPeriod(ctx sdk.Context) int64 {
	if k.storeKey == nil {
		return 0
	}
	store := ctx.KVStore(k.storeKey)
	bz := store.Get([]byte("deposit_period"))
	if bz == nil {
		return 0
	}
	return int64(sdk.BigEndianToUint64(bz))
}

func (k Keeper) nextProposalID(ctx sdk.Context) uint64 {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get(KeyNextProposalID)
	var id uint64 = 1
	if bz != nil {
		id = sdk.BigEndianToUint64(bz)
	}
	store.Set(KeyNextProposalID, sdk.Uint64ToBigEndian(id+1))
	return id
}

func proposalKey(id uint64) []byte {
	return append(KeyProposalPrefix, sdk.Uint64ToBigEndian(id)...)
}

func (k Keeper) SetProposal(ctx sdk.Context, proposal Proposal) {
	bz := k.cdc.MustMarshal(&proposal)
	ctx.KVStore(k.storeKey).Set(proposalKey(proposal.Id), bz)
}

func (k Keeper) GetProposal(ctx sdk.Context, id uint64) (Proposal, bool) {
	bz := ctx.KVStore(k.storeKey).Get(proposalKey(id))
	if bz == nil {
		return Proposal{}, false
	}
	var proposal Proposal
	k.cdc.MustUnmarshal(bz, &proposal)
	return proposal, true
}

func (k Keeper) IterateProposals(ctx sdk.Context) []Proposal {
	store := ctx.KVStore(k.storeKey)
	iterator := storetypes.KVStorePrefixIterator(store, KeyProposalPrefix)
	defer iterator.Close()

	var proposals []Proposal
	for ; iterator.Valid(); iterator.Next() {
		var proposal Proposal
		k.cdc.MustUnmarshal(iterator.Value(), &proposal)
		proposals = append(proposals, proposal)
	}
	return proposals
}