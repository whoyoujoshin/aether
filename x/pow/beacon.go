package pow

import (
	"crypto/sha256"
	"math/big"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// This file implements Phase 3 of aether-randomness-beacon-design.md:
// a sequential-hashing epoch beacon, feeding grinding-resistant
// weighted-random sampling of the validator set. Deliberately NOT a
// name-brand VDF -- see that doc's §2 for why (post-quantum security
// assumptions). Gated behind RandomnessBeaconActivationHeight; see
// that constant's doc comment for the required deployment discipline
// and its known, undocumented-elsewhere residual limitation.

func beaconStateKey(epoch int64) []byte {
	return append(KeyBeaconStatePrefix, sdk.Uint64ToBigEndian(uint64(epoch))...)
}

func beaconSeedKey(epoch int64) []byte {
	return append(KeyBeaconSeedPrefix, sdk.Uint64ToBigEndian(uint64(epoch))...)
}

func (k Keeper) SetBeaconRoundsPerBlock(ctx sdk.Context, rounds int64) {
	bz := sdk.Uint64ToBigEndian(uint64(rounds))
	ctx.KVStore(k.storeKey).Set(KeyBeaconRoundsPerBlock, bz)
}

func (k Keeper) GetBeaconRoundsPerBlock(ctx sdk.Context) int64 {
	bz := ctx.KVStore(k.storeKey).Get(KeyBeaconRoundsPerBlock)
	if bz == nil {
		return DefaultGenesisState().Params.BeaconRoundsPerBlock
	}
	return int64(sdk.BigEndianToUint64(bz))
}

// initialBeaconState is the fresh starting value for an epoch that has
// no prior accumulated state yet -- domain-separated by a fixed label
// and the epoch number, so no two epochs (and no test replaying the
// same epoch number in isolation) can ever start from a colliding or
// predictable-in-a-useful-way seed.
func initialBeaconState(epoch int64) []byte {
	h := sha256.Sum256(append([]byte("aether-beacon-v1/"), sdk.Uint64ToBigEndian(uint64(epoch))...))
	return h[:]
}

func (k Keeper) getBeaconState(ctx sdk.Context, epoch int64) []byte {
	bz := ctx.KVStore(k.storeKey).Get(beaconStateKey(epoch))
	if bz == nil {
		return initialBeaconState(epoch)
	}
	return bz
}

func (k Keeper) setBeaconState(ctx sdk.Context, epoch int64, state []byte) {
	ctx.KVStore(k.storeKey).Set(beaconStateKey(epoch), state)
}

func (k Keeper) clearBeaconState(ctx sdk.Context, epoch int64) {
	ctx.KVStore(k.storeKey).Delete(beaconStateKey(epoch))
}

// GetBeaconSeed returns epoch's finalized beacon seed, if that epoch
// has completed (its last block has been processed) since
// RandomnessBeaconActivationHeight took effect. Finalized seeds are
// kept forever -- one 32-byte value per epoch (~1440 blocks) is
// negligible storage over the life of the chain, and they're useful
// as a permanent, publicly-verifiable audit trail of every past
// validator-selection draw.
func (k Keeper) GetBeaconSeed(ctx sdk.Context, epoch int64) ([]byte, bool) {
	bz := ctx.KVStore(k.storeKey).Get(beaconSeedKey(epoch))
	if bz == nil {
		return nil, false
	}
	return bz, true
}

func (k Keeper) setBeaconSeed(ctx sdk.Context, epoch int64, seed []byte) {
	ctx.KVStore(k.storeKey).Set(beaconSeedKey(epoch), seed)
}

// AdvanceBeacon folds this block's real header hash into the current
// epoch's running accumulator, then runs a modest number of
// additional pure-sequential hashing rounds for mixing. Called from
// EndBlock every block, but a no-op before
// RandomnessBeaconActivationHeight.
//
// The actual delay/unpredictability property this beacon relies on
// comes from spanning ~EpochLength real blocks (hours of real wall-
// clock time an attacker cannot skip or pre-compute past, since future
// blocks' hashes genuinely don't exist yet) -- NOT from
// BeaconRoundsPerBlock being large. That parameter only needs to be
// large enough to mix state well (avalanche effect) and to make
// grinding a single block's worth of alternate nonce choices cost
// something nontrivial; it deliberately stays cheap enough (a few
// thousand SHA-256 rounds, sub-millisecond) to never risk block-time
// consistency, unlike a real VDF computed in one synchronous burst.
//
// On the epoch's last block, the just-updated state is finalized as
// that epoch's permanent seed and the in-progress accumulator is
// cleared (redundant with the finalized copy once sealed).
func (k Keeper) AdvanceBeacon(ctx sdk.Context) {
	if ctx.BlockHeight() < RandomnessBeaconActivationHeight {
		return
	}

	epochLength := k.GetEpochLength(ctx)
	if epochLength <= 0 {
		return
	}

	epoch := k.CurrentEpoch(ctx)
	state := k.getBeaconState(ctx, epoch)

	blockHash := ctx.HeaderHash()
	folded := sha256.Sum256(append(append([]byte{}, state...), blockHash...))
	state = folded[:]

	rounds := k.GetBeaconRoundsPerBlock(ctx)
	for i := int64(0); i < rounds; i++ {
		next := sha256.Sum256(state)
		state = next[:]
	}

	height := ctx.BlockHeight()
	if (height+1)%epochLength == 0 {
		k.setBeaconSeed(ctx, epoch, state)
		k.clearBeaconState(ctx, epoch)
		return
	}

	k.setBeaconState(ctx, epoch, state)
}

// SampleValidatorsWithBeacon performs weighted random sampling
// without replacement: each round, one candidate is drawn with
// probability proportional to its remaining Work, deterministically
// derived from seed and the round index, so any node recomputing from
// the same finalized seed and candidate list reaches the identical
// result. More real mining work still means better odds of a seat,
// exactly as Phase 1's flat ranking intended -- it's just no longer a
// guarantee, which is what actually buys grinding resistance: nobody
// can be certain in advance exactly which K addresses a given amount
// of work will produce.
//
// candidates is consumed by copy, not mutated -- callers can reuse
// their own slice afterward.
//
// KNOWN, DELIBERATELY UNRESOLVED LIMITATION (flagged for the external
// cryptographic review aether-randomness-beacon-design.md requires
// before any mainnet path, not silently glossed over): this is a
// "last-revealer" scheme. Whoever mines an epoch's last block chooses
// which valid header hash to reveal with no later independent block
// diluting their contribution to that epoch's own seed. The one-epoch
// lag applied by ComputeValidatorUpdates (using epoch-1's seed, never
// an epoch's own) denies that miner any way to know at grinding time
// what the NEXT epoch's candidate pool and work totals will even look
// like, which is a real, meaningful mitigation -- but it does not
// eliminate last-revealer bias in the strict cryptographic sense the
// way a true VDF would. This tradeoff is exactly what
// aether-randomness-beacon-design.md's §2 already accepted in choosing
// hash-chaining over a (classically-quantum-breakable) VDF; it is not
// a new gap introduced here, but it is real and unresolved, and this
// implementation makes no claim otherwise.
func (k Keeper) SampleValidatorsWithBeacon(seed []byte, candidates []qualifiedEntry, topK int64) []qualifiedEntry {
	remaining := make([]qualifiedEntry, len(candidates))
	copy(remaining, candidates)

	var selected []qualifiedEntry
	for round := int64(0); round < topK && len(remaining) > 0; round++ {
		totalWeight := new(big.Int)
		for _, c := range remaining {
			totalWeight.Add(totalWeight, new(big.Int).SetUint64(c.Work))
		}
		if totalWeight.Sign() <= 0 {
			// No remaining candidate has any recorded work -- shouldn't
			// happen (qualification already requires Work > 0), but
			// stop rather than risk a mod-by-zero.
			break
		}

		drawInput := append(append([]byte{}, seed...), sdk.Uint64ToBigEndian(uint64(round))...)
		drawHash := sha256.Sum256(drawInput)
		drawVal := new(big.Int).SetBytes(drawHash[:])
		drawPoint := new(big.Int).Mod(drawVal, totalWeight)

		cumulative := new(big.Int)
		chosenIdx := len(remaining) - 1 // defensive fallback; loop below always finds one for a valid drawPoint
		for i, c := range remaining {
			cumulative.Add(cumulative, new(big.Int).SetUint64(c.Work))
			if drawPoint.Cmp(cumulative) < 0 {
				chosenIdx = i
				break
			}
		}

		selected = append(selected, remaining[chosenIdx])
		remaining = append(remaining[:chosenIdx], remaining[chosenIdx+1:]...)
	}

	return selected
}
