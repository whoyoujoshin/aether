package pow

import (
	"crypto/sha256"
	"encoding/json"
	"math/big"
	"bytes"
	"sort"
	"time"

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
	cryptoproto "github.com/cometbft/cometbft/proto/tendermint/crypto"
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
// TenureRampDuration is the real-time window over which a validator's
// voting-power weight (used by x/governance, not live consensus power --
// see the design discussion in the project's governance planning) ramps
// linearly from 0 to a full 1.0 ratio. Deliberately a constant for now,
// not a genesis param -- can become one later if tuning is needed.
const TenureRampDuration = 30 * 24 * time.Hour

func (k Keeper) SetActiveValidator(ctx sdk.Context, minerAddr sdk.AccAddress) {
	store := ctx.KVStore(k.storeKey)
	key := append(KeyActiveValidatorPrefix, minerAddr.Bytes()...)

	// Only stamp entry time if this validator isn't ALREADY active --
	// SetActiveValidator is called every epoch for every still-qualifying
	// validator (see ComputeValidatorUpdates), including ones who were
	// already active last epoch. Re-stamping on every call would reset
	// tenure to zero every single epoch, defeating the entire mechanism.
	if !store.Has(key) {
		entryKey := append(KeyValidatorEnteredAtPrefix, minerAddr.Bytes()...)
		timestampBz := sdk.Uint64ToBigEndian(uint64(ctx.BlockTime().UnixNano()))
		store.Set(entryKey, timestampBz)
	}

	store.Set(key, []byte{1})
}

func (k Keeper) RemoveActiveValidator(ctx sdk.Context, minerAddr sdk.AccAddress) {
	store := ctx.KVStore(k.storeKey)
	store.Delete(append(KeyActiveValidatorPrefix, minerAddr.Bytes()...))
	store.Delete(append(KeyValidatorEnteredAtPrefix, minerAddr.Bytes()...))
}

// GetValidatorTenureRatio returns how far through the tenure ramp this
// validator is, as a value from 0.0 to 1.0. Returns 0 for an address
// that isn't currently active (no entry timestamp recorded). Computed
// fresh from real elapsed time on every call -- not pre-computed or
// cached -- matching the same "derive from real chain state when
// needed" principle used throughout this project (e.g. CurrentEpoch).
func (k Keeper) GetValidatorTenureRatio(ctx sdk.Context, minerAddr sdk.AccAddress) math.LegacyDec {
	store := ctx.KVStore(k.storeKey)
	entryKey := append(KeyValidatorEnteredAtPrefix, minerAddr.Bytes()...)
	bz := store.Get(entryKey)
	if bz == nil {
		return math.LegacyZeroDec()
	}

	enteredAt := time.Unix(0, int64(sdk.BigEndianToUint64(bz)))
	elapsed := ctx.BlockTime().Sub(enteredAt)
	if elapsed <= 0 {
		return math.LegacyZeroDec()
	}

	ratio := math.LegacyNewDec(int64(elapsed)).QuoInt64(int64(TenureRampDuration))
	if ratio.GT(math.LegacyOneDec()) {
		return math.LegacyOneDec()
	}
	return ratio
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
	if err := k.bankKeeper.MintCoins(ctx, ModuleName, coins); err != nil {
		k.logger.Error("failed to mint block reward", "error", err)
		return err
	}
	treasuryCut := math.LegacyNewDecFromInt(reward).
		Mul(math.LegacyMustNewDecFromStr("0.15")).
		TruncateInt()
	minerAmount := reward.Sub(treasuryCut)
	minerCoins := sdk.NewCoins(sdk.NewCoin("aeth", minerAmount))
	if k.IsActiveValidator(ctx, miner) {
		k.AddEscrow(ctx, miner, minerAmount)
	} else {
		if err := k.bankKeeper.SendCoinsFromModuleToAccount(ctx, ModuleName, miner, minerCoins); err != nil {
			return err
		}
	}
	// Treasury cut (15% of reward) splits 13/2 between active validators
	// and the fee collector once validators exist; falls back to 100% fee
	// collector if nobody is currently an active validator.
	validatorShareTotal := treasuryCut.MulRaw(13).QuoRaw(15)
	actuallyCredited := k.CreditTreasuryShareToValidators(ctx, validatorShareTotal)
	feeCollectorAmount := treasuryCut.Sub(actuallyCredited)
	if !feeCollectorAmount.IsZero() {
		feeCollectorCoins := sdk.NewCoins(sdk.NewCoin("aeth", feeCollectorAmount))
		if err := k.bankKeeper.SendCoinsFromModuleToModule(ctx, ModuleName, authtypes.FeeCollectorName, feeCollectorCoins); err != nil {
			return err
		}
	}
	k.logger.Info("block reward distributed",
		"miner", miner.String(),
		"miner_amount", minerAmount.String(),
		"treasury_cut", treasuryCut.String(),
		"validator_share", actuallyCredited.String(),
		"fee_collector_amount", feeCollectorAmount.String(),
		"escrowed", k.IsActiveValidator(ctx, miner),
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
		if k.IsBanned(ctx, entry.MinerAddr) {
			continue // permanently banned for equivocation -- never eligible again
		}
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

// The reverse index: given a consensus address (what CometBFT's evidence
// reports identify offenders by), find the miner address that registered
// it. Built alongside the existing minerAddress -> consensusPubkey index.
func (k Keeper) SetConsensusToMiner(ctx sdk.Context, consensusAddr []byte, minerAddr sdk.AccAddress) {
	ctx.KVStore(k.storeKey).Set(append(KeyConsensusToMinerPrefix, consensusAddr...), minerAddr.Bytes())
}

func (k Keeper) GetMinerByConsensusAddr(ctx sdk.Context, consensusAddr []byte) (sdk.AccAddress, bool) {
	bz := ctx.KVStore(k.storeKey).Get(append(KeyConsensusToMinerPrefix, consensusAddr...))
	if bz == nil {
		return nil, false
	}
	return sdk.AccAddress(bz), true
}

// Permanent ban -- once set, never cleared. A banned address must never be
// selected as a validator again, regardless of future mining work.
func (k Keeper) SetBanned(ctx sdk.Context, minerAddr sdk.AccAddress) {
	ctx.KVStore(k.storeKey).Set(append(KeyBannedPrefix, minerAddr.Bytes()...), []byte{1})
}

func (k Keeper) IsBanned(ctx sdk.Context, minerAddr sdk.AccAddress) bool {
	return ctx.KVStore(k.storeKey).Has(append(KeyBannedPrefix, minerAddr.Bytes()...))
}

func (k Keeper) SetBondCooldown(ctx sdk.Context, cooldown int64) {
	bz, _ := json.Marshal(cooldown)
	ctx.KVStore(k.storeKey).Set(KeyBondCooldown, bz)
}

func (k Keeper) GetBondCooldown(ctx sdk.Context) int64 {
	bz := ctx.KVStore(k.storeKey).Get(KeyBondCooldown)
	if bz == nil {
		return DefaultGenesisState().Params.BondCooldown
	}
	var c int64
	_ = json.Unmarshal(bz, &c)
	return c
}

// AddEscrow increases minerAddr's pending escrowed balance and (re)sets
// their unlock height to the current block height plus the cooldown
// period. Called whenever an active validator earns a reward (their own
// mining reward, or their share of other miners' treasury cut) -- see
// msg_server.go's SubmitPoW.
func (k Keeper) AddEscrow(ctx sdk.Context, minerAddr sdk.AccAddress, amount math.Int) {
	current := k.GetEscrowBalance(ctx, minerAddr)
	newBalance := current.Add(amount)
	bz, _ := newBalance.Marshal()
	ctx.KVStore(k.storeKey).Set(append(KeyEscrowPrefix, minerAddr.Bytes()...), bz)

	unlockHeight := ctx.BlockHeight() + k.GetBondCooldown(ctx)
	unlockBz := sdk.Uint64ToBigEndian(uint64(unlockHeight))
	ctx.KVStore(k.storeKey).Set(append(KeyEscrowUnlockPrefix, minerAddr.Bytes()...), unlockBz)
}

func (k Keeper) GetEscrowBalance(ctx sdk.Context, minerAddr sdk.AccAddress) math.Int {
	bz := ctx.KVStore(k.storeKey).Get(append(KeyEscrowPrefix, minerAddr.Bytes()...))
	if bz == nil {
		return math.ZeroInt()
	}
	var amount math.Int
	if err := amount.Unmarshal(bz); err != nil {
		return math.ZeroInt()
	}
	return amount
}

func (k Keeper) GetEscrowUnlockHeight(ctx sdk.Context, minerAddr sdk.AccAddress) (int64, bool) {
	bz := ctx.KVStore(k.storeKey).Get(append(KeyEscrowUnlockPrefix, minerAddr.Bytes()...))
	if bz == nil {
		return 0, false
	}
	return int64(sdk.BigEndianToUint64(bz)), true
}

// ClearEscrow zeroes out minerAddr's pending escrow and unlock height --
// used both by forfeiture (burn, balance goes to zero) and by release
// (paid out, balance goes to zero) since both end with nothing left
// pending.
func (k Keeper) ClearEscrow(ctx sdk.Context, minerAddr sdk.AccAddress) {
	ctx.KVStore(k.storeKey).Delete(append(KeyEscrowPrefix, minerAddr.Bytes()...))
	ctx.KVStore(k.storeKey).Delete(append(KeyEscrowUnlockPrefix, minerAddr.Bytes()...))
}

// IterateEscrows returns every address with a nonzero pending escrow
// balance, for use by the automatic release check in EndBlock.
func (k Keeper) IterateEscrows(ctx sdk.Context) []sdk.AccAddress {
	store := ctx.KVStore(k.storeKey)
	iterator := store.Iterator(KeyEscrowPrefix, storetypes.PrefixEndBytes(KeyEscrowPrefix))
	defer iterator.Close()

	var addrs []sdk.AccAddress
	for ; iterator.Valid(); iterator.Next() {
		addrBytes := iterator.Key()[len(KeyEscrowPrefix):]
		addrs = append(addrs, sdk.AccAddress(addrBytes))
	}
	return addrs
}

// Pending removal: populated by misbehavior consumption (BeginBlock),
// drained unconditionally by EndBlock every single block -- independent
// of the epoch-boundary Top-K logic, since a banned validator must lose
// power immediately, not wait for the next epoch transition.
func (k Keeper) MarkPendingRemoval(ctx sdk.Context, minerAddr sdk.AccAddress) {
	ctx.KVStore(k.storeKey).Set(append(KeyPendingRemovalPrefix, minerAddr.Bytes()...), []byte{1})
}

func (k Keeper) IteratePendingRemovals(ctx sdk.Context) []sdk.AccAddress {
	store := ctx.KVStore(k.storeKey)
	iterator := store.Iterator(KeyPendingRemovalPrefix, storetypes.PrefixEndBytes(KeyPendingRemovalPrefix))
	defer iterator.Close()

	var addrs []sdk.AccAddress
	for ; iterator.Valid(); iterator.Next() {
		addrBytes := iterator.Key()[len(KeyPendingRemovalPrefix):]
		addrs = append(addrs, sdk.AccAddress(addrBytes))
	}
	return addrs
}

func (k Keeper) ClearPendingRemoval(ctx sdk.Context, minerAddr sdk.AccAddress) {
	ctx.KVStore(k.storeKey).Delete(append(KeyPendingRemovalPrefix, minerAddr.Bytes()...))
}

// ProcessMisbehavior reads CometBFT's reported evidence for this block
// (already cryptographically verified by CometBFT itself before it ever
// reaches the app -- see aether-randomness-beacon-design.md) and, for each
// offending validator we can identify, permanently bans them and burns
// their entire pending escrow. Called from BeginBlock so the ban takes
// effect before any transactions in this same block are processed.
func (k Keeper) ProcessMisbehavior(ctx sdk.Context) {
	evidenceList := ctx.CometInfo().GetEvidence()

	for i := 0; i < evidenceList.Len(); i++ {
		evidence := evidenceList.Get(i)
		consensusAddr := evidence.Validator().Address()

		minerAddr, ok := k.GetMinerByConsensusAddr(ctx, consensusAddr)
		if !ok {
			// Evidence against a validator we have no record of registering.
			// Can't act on it, but log it -- this shouldn't normally happen
			// on a chain where only our own registered addresses ever hold
			// voting power.
			k.logger.Error("misbehavior evidence for unrecognized consensus address, cannot act on it")
			continue
		}

		if k.IsBanned(ctx, minerAddr) {
			continue // already banned, nothing further to do
		}

		k.logger.Error("validator equivocation detected, permanently banning and forfeiting escrow",
			"miner", minerAddr.String(),
			"misbehavior_type", int32(evidence.Type()),
			"evidence_height", evidence.Height(),
		)

		forfeited := k.GetEscrowBalance(ctx, minerAddr)
		if !forfeited.IsZero() {
			coins := sdk.NewCoins(sdk.NewCoin("aeth", forfeited))
			if err := k.bankKeeper.BurnCoins(ctx, ModuleName, coins); err != nil {
				k.logger.Error("failed to burn forfeited escrow", "miner", minerAddr.String(), "error", err)
			}
		}
		k.ClearEscrow(ctx, minerAddr)

		k.SetBanned(ctx, minerAddr)
		k.RemoveActiveValidator(ctx, minerAddr)
		k.MarkPendingRemoval(ctx, minerAddr)
	}
}

// CreditTreasuryShareToValidators splits amount evenly across every
// currently active validator, adding each share to their escrow (subject
// to the same cooldown as their own mining rewards). Returns the amount
// actually credited to validators, so the caller can send the remainder
// (including any leftover from integer-division truncation) to the fee
// collector rather than letting it silently vanish.
func (k Keeper) CreditTreasuryShareToValidators(ctx sdk.Context, amount math.Int) math.Int {
	validators := k.IterateActiveValidators(ctx)
	if len(validators) == 0 {
		return math.ZeroInt()
	}

	share := amount.QuoRaw(int64(len(validators)))
	if share.IsZero() {
		return math.ZeroInt() // amount too small to meaningfully divide
	}

	for _, addr := range validators {
		k.AddEscrow(ctx, addr, share)
	}
	return share.MulRaw(int64(len(validators)))
}

// ReleaseMaturedEscrows pays out any pending escrow whose unlock height
// has passed, to the validator's own account. Skips (and permanently
// leaves locked) any address that's since been banned -- a banned
// validator's escrow was already forfeited and burned in
// ProcessMisbehavior, so there should be nothing left to release for
// them, but this guard exists in case of ordering edge cases within a
// single block.
func (k Keeper) ReleaseMaturedEscrows(ctx sdk.Context) {
	for _, minerAddr := range k.IterateEscrows(ctx) {
		if k.IsBanned(ctx, minerAddr) {
			continue
		}

		unlockHeight, ok := k.GetEscrowUnlockHeight(ctx, minerAddr)
		if !ok || ctx.BlockHeight() < unlockHeight {
			continue
		}

		balance := k.GetEscrowBalance(ctx, minerAddr)
		if balance.IsZero() {
			k.ClearEscrow(ctx, minerAddr)
			continue
		}

		coins := sdk.NewCoins(sdk.NewCoin("aeth", balance))
		if err := k.bankKeeper.SendCoinsFromModuleToAccount(ctx, ModuleName, minerAddr, coins); err != nil {
			k.logger.Error("failed to release matured escrow", "miner", minerAddr.String(), "error", err)
			continue
		}

		k.ClearEscrow(ctx, minerAddr)
	}
}

func (k Keeper) SetRecencyWindowK(ctx sdk.Context, kWindow int64) {
	bz, _ := json.Marshal(kWindow)
	ctx.KVStore(k.storeKey).Set(KeyRecencyWindowK, bz)
}

func (k Keeper) GetRecencyWindowK(ctx sdk.Context) int64 {
	bz := ctx.KVStore(k.storeKey).Get(KeyRecencyWindowK)
	if bz == nil {
		return DefaultGenesisState().Params.RecencyWindowK
	}
	var v int64
	_ = json.Unmarshal(bz, &v)
	return v
}

func recentHashKey(height int64) []byte {
	return append(KeyRecentHashPrefix, sdk.Uint64ToBigEndian(uint64(height))...)
}

func recentDifficultyKey(height int64) []byte {
	return append(KeyRecentDifficultyPrefix, sdk.Uint64ToBigEndian(uint64(height))...)
}

// RecordRecentBlock stores this block's real hash and post-adjustment
// difficulty, called from EndBlock every block. Retention is K+2 heights;
// older entries are pruned immediately (this window is small and
// unconditionally bounded, unlike AcceptedWork).
func (k Keeper) RecordRecentBlock(ctx sdk.Context) {
	height := ctx.BlockHeight()
	ctx.KVStore(k.storeKey).Set(recentHashKey(height), ctx.HeaderHash())
	ctx.KVStore(k.storeKey).Set(recentDifficultyKey(height), []byte(k.GetDifficulty(ctx).String()))

	retain := k.GetRecencyWindowK(ctx) + 2
	pruneHeight := height - retain
	if pruneHeight >= 0 {
		ctx.KVStore(k.storeKey).Delete(recentHashKey(pruneHeight))
		ctx.KVStore(k.storeKey).Delete(recentDifficultyKey(pruneHeight))
	}
}

func (k Keeper) GetRecentHash(ctx sdk.Context, height int64) ([]byte, bool) {
	bz := ctx.KVStore(k.storeKey).Get(recentHashKey(height))
	if bz == nil {
		return nil, false
	}
	return bz, true
}

func (k Keeper) GetRecentDifficulty(ctx sdk.Context, height int64) (math.Int, bool) {
	bz := ctx.KVStore(k.storeKey).Get(recentDifficultyKey(height))
	if bz == nil {
		return math.ZeroInt(), false
	}
	d, ok := math.NewIntFromString(string(bz))
	if !ok {
		return math.ZeroInt(), false
	}
	return d, true
}

func acceptedWorkKey(headerHash []byte) []byte {
	return append(KeyAcceptedWorkPrefix, headerHash...)
}

func (k Keeper) IsWorkAccepted(ctx sdk.Context, headerHash []byte) bool {
	return ctx.KVStore(k.storeKey).Has(acceptedWorkKey(headerHash))
}

func (k Keeper) MarkWorkAccepted(ctx sdk.Context, headerHash []byte) {
	ctx.KVStore(k.storeKey).Set(acceptedWorkKey(headerHash), []byte{1})
}

// BootstrapValidator registers a genesis-declared validator (placed
// directly into genesis.json's consensus.validators, never through
// MsgRegisterValidatorPubkey) into x/pow's own tracked validator state.
// Without this, the bootstrap validator is permanently exempt from Top-K
// removal (ComputeValidatorUpdates only ever considers addresses already
// in IterateActiveValidators) and invisible to equivocation slashing
// (ProcessMisbehavior maps evidence's consensus address back to a miner
// address via the same reverse index this populates).
//
// Since a genesis validator has no natural miner account, a stable,
// deterministic miner address is derived directly from its consensus
// address bytes -- this makes it behave identically to any other
// validator for every existing mechanism, with zero special-casing
// required anywhere else in the codebase.
//
// Voting power is intentionally NOT preserved from genesis here -- it's
// set to the same flat ValidatorVotingPower constant every other
// validator receives. The old, large genesis power value was only ever
// needed to bootstrap a single-node devnet; once tracked, this validator
// is subject to the exact same rules (equal power, Top-K removal,
// slashing) as anyone else. No permanent privilege.
func (k Keeper) BootstrapValidator(ctx sdk.Context, pubKeyProto cryptoproto.PublicKey) error {
	cometPubKey, err := cometencoding.PubKeyFromProto(pubKeyProto)
	if err != nil {
		return err
	}
	rawPubKey := cometPubKey.Bytes()
	consensusAddr := cometPubKey.Address()
	derivedMinerAddr := sdk.AccAddress(consensusAddr)

	if k.IsActiveValidator(ctx, derivedMinerAddr) {
		return nil // already bootstrapped (e.g. re-run on a restart); idempotent
	}

	k.SetValidatorPubkey(ctx, derivedMinerAddr, rawPubKey)
	k.SetConsensusToMiner(ctx, consensusAddr, derivedMinerAddr)
	k.SetActiveValidator(ctx, derivedMinerAddr)

	return nil
}