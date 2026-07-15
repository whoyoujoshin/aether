package pow

import (
	"crypto/sha256"
	"encoding/json"
	"math/big"
	"bytes"
	"sort"

	"cosmossdk.io/log"
	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/whoyoujoshin/aether/x/pow/types"
	cometcrypto "github.com/cometbft/cometbft/crypto"
	cometed25519 "github.com/cometbft/cometbft/crypto/ed25519"
	cometencoding "github.com/cometbft/cometbft/crypto/encoding"
	abci "github.com/cometbft/cometbft/abci/types"
)

type Keeper struct {
	cdc      codec.BinaryCodec
	storeKey storetypes.StoreKey
	logger   log.Logger
	bankKeeper types.BankKeeper
}

func NewKeeper(cdc codec.BinaryCodec, storeKey storetypes.StoreKey, logger log.Logger, bankKeeper types.BankKeeper,) Keeper {
	return Keeper{cdc: cdc, storeKey: storeKey, logger: logger, bankKeeper: bankKeeper,}
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

func (k Keeper) SetMinDifficulty(ctx sdk.Context, minDifficulty int64) {
	bz, _ := json.Marshal(minDifficulty)
	ctx.KVStore(k.storeKey).Set(KeyMinDifficulty, bz)
}

func (k Keeper) GetMinDifficulty(ctx sdk.Context) math.Int {
	bz := ctx.KVStore(k.storeKey).Get(KeyMinDifficulty)
	if bz == nil {
		return math.NewInt(int64(DefaultGenesisState().Params.MinDifficulty))
	}
	var d int64
	_ = json.Unmarshal(bz, &d)
	return math.NewInt(d)
}

func (k Keeper) SetMaxDifficulty(ctx sdk.Context, maxDifficulty int64) {
	bz, _ := json.Marshal(maxDifficulty)
	ctx.KVStore(k.storeKey).Set(KeyMaxDifficulty, bz)
}

func (k Keeper) GetMaxDifficulty(ctx sdk.Context) math.Int {
	bz := ctx.KVStore(k.storeKey).Get(KeyMaxDifficulty)
	if bz == nil {
		return math.NewInt(int64(DefaultGenesisState().Params.MaxDifficulty))
	}
	var d int64
	_ = json.Unmarshal(bz, &d)
	return math.NewInt(d)
}

func (k Keeper) SetTargetBlockTime(ctx sdk.Context, targetBlockTime int64) {
	bz, _ := json.Marshal(targetBlockTime)
	ctx.KVStore(k.storeKey).Set(KeyTargetBlockTime, bz)
}

func (k Keeper) GetTargetBlockTime(ctx sdk.Context) int64 {
	bz := ctx.KVStore(k.storeKey).Get(KeyTargetBlockTime)
	if bz == nil {
		return DefaultGenesisState().Params.TargetBlockTime
	}
	var t int64
	_ = json.Unmarshal(bz, &t)
	return t
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

func validatorPubkeyKey(minerAddr sdk.AccAddress) []byte {
	return append(KeyValidatorPubkeyPrefix, minerAddr.Bytes()...)
}

func (k Keeper) SetValidatorPubkey(ctx sdk.Context, minerAddr sdk.AccAddress, consensusPubkey []byte) {
	ctx.KVStore(k.storeKey).Set(validatorPubkeyKey(minerAddr), consensusPubkey)
}

func (k Keeper) GetValidatorPubkey(ctx sdk.Context, minerAddr sdk.AccAddress) ([]byte, bool) {
	bz := ctx.KVStore(k.storeKey).Get(validatorPubkeyKey(minerAddr))
	if bz == nil {
		return nil, false
	}
	return bz, true
}

func (k Keeper) SetEpochLength(ctx sdk.Context, epochLength int64) {
	bz, _ := json.Marshal(epochLength)
	ctx.KVStore(k.storeKey).Set(KeyEpochLength, bz)
}

func (k Keeper) GetEpochLength(ctx sdk.Context) int64 {
	bz := ctx.KVStore(k.storeKey).Get(KeyEpochLength)
	if bz == nil {
		return DefaultGenesisState().Params.EpochLength
	}
	var e int64
	_ = json.Unmarshal(bz, &e)
	return e
}

// CurrentEpoch derives the epoch number from real chain height
// (ctx.BlockHeight()), never from user-supplied message fields -- those
// aren't validated against actual chain state and can't be trusted here.
func (k Keeper) CurrentEpoch(ctx sdk.Context) int64 {
	epochLength := k.GetEpochLength(ctx)
	if epochLength <= 0 {
		epochLength = 1 // defensive: avoid divide-by-zero if misconfigured
	}
	return ctx.BlockHeight() / epochLength
}

func epochWorkKey(epoch int64, minerAddr sdk.AccAddress) []byte {
	epochBytes := sdk.Uint64ToBigEndian(uint64(epoch))
	key := make([]byte, 0, len(KeyEpochWorkPrefix)+len(epochBytes)+len(minerAddr.Bytes()))
	key = append(key, KeyEpochWorkPrefix...)
	key = append(key, epochBytes...)
	key = append(key, minerAddr.Bytes()...)
	return key
}

// AddMiningWork increments recorded work for minerAddr in the given epoch.
// Called from SubmitPoW's success path -- this is the raw input Top-K
// validator selection will later rank addresses by. Starts as a simple
// raw submission count; difficulty-weighting is an explicitly deferred
// decision (see design doc open questions), not decided here.
func (k Keeper) AddMiningWork(ctx sdk.Context, epoch int64, minerAddr sdk.AccAddress, amount uint64) {
	newTotal := k.GetMiningWork(ctx, epoch, minerAddr) + amount
	ctx.KVStore(k.storeKey).Set(epochWorkKey(epoch, minerAddr), sdk.Uint64ToBigEndian(newTotal))
}

func (k Keeper) GetMiningWork(ctx sdk.Context, epoch int64, minerAddr sdk.AccAddress) uint64 {
	bz := ctx.KVStore(k.storeKey).Get(epochWorkKey(epoch, minerAddr))
	if bz == nil {
		return 0
	}
	return sdk.BigEndianToUint64(bz)
}

// MiningWorkEntry pairs a miner address with accumulated work in an epoch.
type MiningWorkEntry struct {
	MinerAddr sdk.AccAddress
	Work      uint64
}

// IterateEpochWork returns every address with recorded work in the given
// epoch. Not used yet -- exists now so the epoch_work/ key layout gets
// verified against real iteration today, rather than assumed correct
// until Top-K selection (component 4) is built on top of it.
func (k Keeper) IterateEpochWork(ctx sdk.Context, epoch int64) []MiningWorkEntry {
	epochBytes := sdk.Uint64ToBigEndian(uint64(epoch))
	prefix := append(append([]byte{}, KeyEpochWorkPrefix...), epochBytes...)

	store := ctx.KVStore(k.storeKey)
	iterator := store.Iterator(prefix, storetypes.PrefixEndBytes(prefix))
	defer iterator.Close()

	var entries []MiningWorkEntry
	for ; iterator.Valid(); iterator.Next() {
		addrBytes := iterator.Key()[len(prefix):]
		entries = append(entries, MiningWorkEntry{
			MinerAddr: sdk.AccAddress(addrBytes),
			Work:      sdk.BigEndianToUint64(iterator.Value()),
		})
	}
	return entries
}

// SetActiveValidator/RemoveActiveValidator/IterateActiveValidators track
// which addresses are *currently* validators (i.e., were granted power in
// the last emitted ValidatorUpdates), so the next epoch's computation knows
// who needs an explicit power=0 removal update if they fall out of Top-K.
func (k Keeper) SetActiveValidator(ctx sdk.Context, minerAddr sdk.AccAddress) {
	ctx.KVStore(k.storeKey).Set(append(KeyActiveValidatorPrefix, minerAddr.Bytes()...), []byte{1})
}

func (k Keeper) RemoveActiveValidator(ctx sdk.Context, minerAddr sdk.AccAddress) {
	ctx.KVStore(k.storeKey).Delete(append(KeyActiveValidatorPrefix, minerAddr.Bytes()...))
}

func (k Keeper) IsActiveValidator(ctx sdk.Context, minerAddr sdk.AccAddress) bool {
	return ctx.KVStore(k.storeKey).Has(append(KeyActiveValidatorPrefix, minerAddr.Bytes()...))
}

func (k Keeper) IterateActiveValidators(ctx sdk.Context) []sdk.AccAddress {
	store := ctx.KVStore(k.storeKey)
	iterator := store.Iterator(KeyActiveValidatorPrefix, storetypes.PrefixEndBytes(KeyActiveValidatorPrefix))
	defer iterator.Close()

	var addrs []sdk.AccAddress
	for ; iterator.Valid(); iterator.Next() {
		addrBytes := iterator.Key()[len(KeyActiveValidatorPrefix):]
		addrs = append(addrs, sdk.AccAddress(addrBytes))
	}
	return addrs
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

func (k Keeper) Heartbeat(ctx sdk.Context) {
	ctx.KVStore(k.storeKey).Set([]byte("last_seen_height"), sdk.Uint64ToBigEndian(uint64(ctx.BlockHeight())))
}

// --- PoW logic (placeholder verification — see note below) ---

func (k Keeper) VerifyMiningHeader(ctx sdk.Context, header MiningHeader) bool {
	if header.Difficulty == 0 {
		return false
	}

	data := headerToBytes(header)
	hash := sha256.Sum256(data)

	// maxTarget is the easiest possible target (difficulty == 1). Higher
	// difficulty divides it into a smaller (harder) target, matching the
	// multiplicative retargeting used in AdjustDifficulty and the large
	// difficulty values used in Params/DefaultGenesisState.
	maxTarget := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	difficulty := new(big.Int).SetUint64(header.Difficulty)
	target := new(big.Int).Div(maxTarget, difficulty)

	return new(big.Int).SetBytes(hash[:]).Cmp(target) < 0
}

func (k Keeper) AdjustDifficulty(ctx sdk.Context) math.Int {
	current := k.GetDifficulty(ctx)

	lastTime, ok := k.GetLastBlockTime(ctx)
	if !ok {
		return current
	}

	elapsed := ctx.BlockTime().Unix() - lastTime
	if elapsed <= 0 {
		return current
	}

	target := k.GetTargetBlockTime(ctx)
	adjusted := current.MulRaw(target).QuoRaw(elapsed)

	minD := k.GetMinDifficulty(ctx)
	maxD := k.GetMaxDifficulty(ctx)
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
	if reward.IsZero() {
		return nil
	}

	coins := sdk.NewCoins(sdk.NewCoin("aeth", reward))

	// Mint new reward coins to the pow module account
	if err := k.bankKeeper.MintCoins(ctx, ModuleName, coins); err != nil {
		k.logger.Error("failed to mint block reward", "error", err)
		return err
	}

	// Calculate splits (15% to treasury / fee collector)
	treasuryCut := math.LegacyNewDecFromInt(reward).
		Mul(math.LegacyMustNewDecFromStr("0.15")).
		TruncateInt()
	minerAmount := reward.Sub(treasuryCut)

	minerCoins := sdk.NewCoins(sdk.NewCoin("aeth", minerAmount))
	treasuryCoins := sdk.NewCoins(sdk.NewCoin("aeth", treasuryCut))

	// Send miner's share
	if err := k.bankKeeper.SendCoinsFromModuleToAccount(ctx, ModuleName, miner, minerCoins); err != nil {
		return err
	}

	// Send treasury cut to fee collector (we can route this to x/treasury later)
	if !treasuryCut.IsZero() {
		if err := k.bankKeeper.SendCoinsFromModuleToModule(ctx, ModuleName, authtypes.FeeCollectorName, treasuryCoins); err != nil {
			return err
		}
	}

	k.logger.Info("block reward distributed",
		"miner", miner.String(),
		"miner_amount", minerAmount.String(),
		"treasury_amount", treasuryCut.String(),
	)

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

var (
	KeyTopKSize = []byte("top_k_size")
)

func (k Keeper) SetTopKSize(ctx sdk.Context, topK int64) {
	bz, _ := json.Marshal(topK)
	ctx.KVStore(k.storeKey).Set(KeyTopKSize, bz)
}

func (k Keeper) GetTopKSize(ctx sdk.Context) int64 {
	bz := ctx.KVStore(k.storeKey).Get(KeyTopKSize)
	if bz == nil {
		return DefaultGenesisState().Params.TopKSize
	}
	var v int64
	_ = json.Unmarshal(bz, &v)
	return v
}

// ValidatorVotingPower defines fixed voting power granted to every selected
// validator this phase. Proportional-to-work weighting is an explicitly
// deferred decision (see randomness-beacon design doc), not made here --
// starting with equal power per selected validator keeps this phase's
// logic simple and testable, matching the "ships a working multi-validator
// devnet" goal for Phase 1 specifically.
const ValidatorVotingPower = 1_000_000

// toValidatorUpdate converts a raw 32-byte ed25519 pubkey and a power value
// into an abci.ValidatorUpdate, handling the proto encoding step explicitly
// so a malformed pubkey is caught here (logged, skipped) rather than
// silently producing an update CometBFT rejects at the ABCI boundary.
func (k Keeper) toValidatorUpdate(rawPubkey []byte, power int64, minerAddr sdk.AccAddress) (abci.ValidatorUpdate, bool) {
	var cometPubkey cometcrypto.PubKey = cometed25519.PubKey(rawPubkey)

	protoPubkey, err := cometencoding.PubKeyToProto(cometPubkey)
	if err != nil {
		k.logger.Error("failed to encode validator pubkey to proto, skipping",
			"miner", minerAddr.String(), "error", err)
		return abci.ValidatorUpdate{}, false
	}

	return abci.ValidatorUpdate{
		PubKey: protoPubkey,
		Power:  power,
	}, true
}

func (k Keeper) ComputeValidatorUpdates(ctx sdk.Context, epoch int64) []abci.ValidatorUpdate {
	workEntries := k.IterateEpochWork(ctx, epoch)

	type qualifiedEntry struct {
		MinerAddr sdk.AccAddress
		Pubkey    []byte
		Work      uint64
	}
	var qualified []qualifiedEntry
	for _, entry := range workEntries {
		pubkey, ok := k.GetValidatorPubkey(ctx, entry.MinerAddr)
		if !ok {
			continue // mined, but never registered a consensus pubkey -- not eligible
		}
		qualified = append(qualified, qualifiedEntry{
			MinerAddr: entry.MinerAddr,
			Pubkey:    pubkey,
			Work:      entry.Work,
		})
	}

	if len(qualified) == 0 {
		k.logger.Info("no qualified validator candidates this epoch, leaving validator set unchanged", "epoch", epoch)
		return nil
	}

	sort.Slice(qualified, func(i, j int) bool {
		if qualified[i].Work != qualified[j].Work {
			return qualified[i].Work > qualified[j].Work
		}
		return bytes.Compare(qualified[i].MinerAddr.Bytes(), qualified[j].MinerAddr.Bytes()) < 0
	})

	topK := k.GetTopKSize(ctx)
	if int64(len(qualified)) > topK {
		qualified = qualified[:topK]
	}

	selected := make(map[string]qualifiedEntry, len(qualified))
	for _, q := range qualified {
		selected[q.MinerAddr.String()] = q
	}

	var updates []abci.ValidatorUpdate

	// Removals: anyone currently active who didn't make this epoch's cut.
	for _, activeAddr := range k.IterateActiveValidators(ctx) {
		if _, stillSelected := selected[activeAddr.String()]; !stillSelected {
			pubkey, ok := k.GetValidatorPubkey(ctx, activeAddr)
			if !ok {
				continue // shouldn't happen, but don't panic on it
			}
			if update, ok := k.toValidatorUpdate(pubkey, 0, activeAddr); ok {
				updates = append(updates, update)
			}
			k.RemoveActiveValidator(ctx, activeAddr)
		}
	}

	// Additions/unchanged: everyone selected this epoch (re-)gets power.
	for _, q := range qualified {
		if update, ok := k.toValidatorUpdate(q.Pubkey, ValidatorVotingPower, q.MinerAddr); ok {
			updates = append(updates, update)
			k.SetActiveValidator(ctx, q.MinerAddr)
		}
	}

	return updates
}

