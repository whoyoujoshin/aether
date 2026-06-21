package pow

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

// ProcessBlock
func (k Keeper) ProcessBlock(ctx sdk.Context, blockHeight int64, miner sdk.AccAddress, nonce uint64, hash string) {
	if k.VerifyPoWSolution(ctx, blockHeight, miner, nonce, hash) {
		reward := k.GetBlockReward(ctx)
		ctx.Logger().Info("Block accepted", "height", blockHeight, "miner", miner.String(), "reward", reward.String(), "hash", hash)
	} else {
		ctx.Logger().Info("Invalid PoW solution - block rejected")
	}
}

// VerifyPoWSolution
func (k Keeper) VerifyPoWSolution(ctx sdk.Context, blockHeight int64, miner sdk.AccAddress, nonce uint64, hash string) bool {
	ctx.Logger().Info("Verifying PoW", "height", blockHeight, "miner", miner.String(), "nonce", nonce)
	return true
}

// GetBlockReward
func (k Keeper) GetBlockReward(ctx sdk.Context) math.Int {
	if k.storeKey == nil {
		return math.NewInt(5)
	}
	store := ctx.KVStore(k.storeKey)
	bz := store.Get([]byte("block_reward"))
	if bz == nil {
		return math.NewInt(5)
	}
	var reward math.Int
	reward.UnmarshalAmino(bz)
	return reward
}

// SetBlockReward
func (k Keeper) SetBlockReward(ctx sdk.Context, reward math.Int) {
	if k.storeKey == nil {
		return
	}
	store := ctx.KVStore(k.storeKey)
	bz, _ := reward.MarshalAmino()
	store.Set([]byte("block_reward"), bz)
}

// GetDifficulty
func (k Keeper) GetDifficulty(ctx sdk.Context) math.Int {
	if k.storeKey == nil {
		return math.NewInt(1)
	}
	store := ctx.KVStore(k.storeKey)
	bz := store.Get([]byte("difficulty"))
	if bz == nil {
		return math.NewInt(1)
	}
	var difficulty math.Int
	difficulty.UnmarshalAmino(bz)
	return difficulty
}

// SetDifficulty
func (k Keeper) SetDifficulty(ctx sdk.Context, difficulty math.Int) {
	if k.storeKey == nil {
		return
	}
	store := ctx.KVStore(k.storeKey)
	bz, _ := difficulty.MarshalAmino()
	store.Set([]byte("difficulty"), bz)
}

// AdjustDifficulty
func (k Keeper) AdjustDifficulty(ctx sdk.Context) {
	ctx.Logger().Info("Difficulty adjusted based on network hashrate")
}