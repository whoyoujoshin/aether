package pow

import (
	"crypto/sha256"
	"encoding/json"
	"math/big"

	"cosmossdk.io/log"
	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

type Keeper struct {
	cdc      codec.BinaryCodec
	storeKey storetypes.StoreKey
	logger   log.Logger
}

func NewKeeper(cdc codec.BinaryCodec, storeKey storetypes.StoreKey, logger log.Logger) Keeper {
	return Keeper{cdc: cdc, storeKey: storeKey, logger: logger}
}

// --- Difficulty ---

func (k Keeper) SetDifficulty(ctx sdk.Context, difficulty math.Int) {
	bz, _ := json.Marshal(difficulty.Int64())
	ctx.KVStore(k.storeKey).Set(KeyDifficulty, bz)
}

func (k Keeper) GetDifficulty(ctx sdk.Context) math.Int {
	bz := ctx.KVStore(k.storeKey).Get(KeyDifficulty)
	if bz == nil {
		return math.NewInt(int64(DefaultGenesisState().Params.Difficulty))
	}
	var d int64
	_ = json.Unmarshal(bz, &d)
	return math.NewInt(d)
}

func (k Keeper) SetLastBlockTime(ctx sdk.Context, t int64) {
	bz, _ := json.Marshal(t)
	ctx.KVStore(k.storeKey).Set(KeyLastBlockTime, bz)
}

func (k Keeper) GetLastBlockTime(ctx sdk.Context) (int64, bool) {
	bz := ctx.KVStore(k.storeKey).Get(KeyLastBlockTime)
	if bz == nil {
		return 0, false
	}
	var t int64
	_ = json.Unmarshal(bz, &t)
	return t, true
}
// --- Block reward ---

func (k Keeper) SetBlockReward(ctx sdk.Context, reward math.Int) {
	bz, _ := json.Marshal(reward.Int64())
	ctx.KVStore(k.storeKey).Set(KeyBlockReward, bz)
}

func (k Keeper) GetBlockReward(ctx sdk.Context) math.Int {
	bz := ctx.KVStore(k.storeKey).Get(KeyBlockReward)
	if bz == nil {
		return math.NewInt(int64(DefaultGenesisState().Params.BlockReward))
	}
	var r int64
	_ = json.Unmarshal(bz, &r)
	return math.NewInt(r)
}

// --- PoW logic (placeholder verification — see note below) ---

func (k Keeper) VerifyMiningHeader(ctx sdk.Context, header MiningHeader) bool {
	if header.Difficulty == 0 || header.Difficulty >= 256 {
		return false
	}
	data := headerToBytes(header)
	hash := sha256.Sum256(data)
	target := new(big.Int).Lsh(big.NewInt(1), uint(256-header.Difficulty))
	return new(big.Int).SetBytes(hash[:]).Cmp(target) < 0
}

func (k Keeper) AdjustDifficulty(ctx sdk.Context) math.Int {
	current := k.GetDifficulty(ctx)
	defaults := DefaultGenesisState().Params

	lastTime, ok := k.GetLastBlockTime(ctx)
	if !ok {
		// first block since genesis/reset — nothing to compare against yet
		return current
	}

	elapsed := ctx.BlockTime().Unix() - lastTime
	if elapsed <= 0 {
		return current
	}

	target := defaults.TargetBlockTime
	adjusted := current.MulRaw(target).QuoRaw(elapsed)

	minD := math.NewInt(int64(defaults.MinDifficulty))
	maxD := math.NewInt(int64(defaults.MaxDifficulty))
	if adjusted.LT(minD) {
		adjusted = minD
	}
	if adjusted.GT(maxD) {
		adjusted = maxD
	}
	return adjusted
}

func (k Keeper) DistributeBlockReward(ctx sdk.Context, miner sdk.AccAddress) error {
	reward := k.GetBlockReward(ctx)
	treasuryCut := math.LegacyNewDecFromInt(reward).
		Mul(math.LegacyMustNewDecFromStr("0.15")).
		TruncateInt()
	minerAmount := reward.Sub(treasuryCut)

	k.logger.Info("block reward distributed",
		"miner", miner.String(),
		"miner_amount", minerAmount.String(),
		"treasury_amount", treasuryCut.String(),
	)
	// TODO: actually move coins via bankKeeper + hand treasuryCut to x/treasury
	return nil
}

func headerToBytes(h MiningHeader) []byte {
	buf := make([]byte, 0, 64)
	var tmp [8]byte
	putU64 := func(v uint64) {
		for i := 0; i < 8; i++ {
			tmp[i] = byte(v >> (8 * i))
		}
		buf = append(buf, tmp[:]...)
	}
	putU64(h.Height)
	putU64(uint64(h.Timestamp))
	buf = append(buf, h.PrevHash...)
	buf = append(buf, h.MerkleRoot...)
	putU64(h.Nonce)
	putU64(h.Difficulty)
	buf = append(buf, h.MinerAddress.Bytes()...)
	return buf
}