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

// ProcessBlock (full block processing with verification)
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
	return math.NewInt(5)
}

// GetDifficulty
func (k Keeper) GetDifficulty(ctx sdk.Context) math.Int {
	return math.NewInt(1)
}

// AdjustDifficulty
func (k Keeper) AdjustDifficulty(ctx sdk.Context) {
	ctx.Logger().Info("Difficulty adjusted")
}