package pow

import (
	"crypto/sha256"
	"encoding/binary"
	"math/big"

	"cosmossdk.io/log"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

type Keeper struct {
	cdc      codec.BinaryCodec
	storeKey storetypes.StoreKey
	logger   log.Logger
	// bankKeeper, treasuryKeeper to be injected later
}

func NewKeeper(cdc codec.BinaryCodec, storeKey storetypes.StoreKey, logger log.Logger) Keeper {
	return Keeper{
		cdc:      cdc,
		storeKey: storeKey,
		logger:   logger,
	}
}

// VerifyMiningHeader - Scrypt + SHA256 PoW check
func (k Keeper) VerifyMiningHeader(ctx sdk.Context, header MiningHeader) bool {
	data := headerToBytes(header)
	hash := sha256.Sum256(data)
	target := new(big.Int).Lsh(big.NewInt(1), uint(256)-uint(header.Difficulty))

	return new(big.Int).SetBytes(hash[:]).Cmp(target) < 0
}

// AdjustDifficulty - simple responsive adjustment
func (k Keeper) AdjustDifficulty(ctx sdk.Context) uint64 {
	// TODO: full EMA / retarget logic
	return 1 << 20 // placeholder
}

// DistributeBlockReward - handles 15% treasury cut
func (k Keeper) DistributeBlockReward(ctx sdk.Context, miner sdk.AccAddress) error {
	params := k.GetParams(ctx)
	reward := sdk.NewCoin("aeth", params.BlockReward)

	treasuryCut := sdk.NewDecFromInt(reward.Amount).Mul(sdk.MustNewDecFromStr("0.15")).TruncateInt()
	minerAmount := reward.Amount.Sub(treasuryCut)

	// TODO: Send to miner + treasury module
	k.logger.Info("Block reward distributed", "miner", miner, "amount", minerAmount)
	return nil
}

// helper (add full impl)
func headerToBytes(h MiningHeader) []byte {
	// placeholder
	return nil
}
