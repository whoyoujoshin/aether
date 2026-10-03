package pow

import (
	"math/big"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// SmoothRetargetActivationHeight is the first height at which an accepted
// submission moves difficulty by asertDifficulty instead of
// current × TargetBlockTime ÷ elapsed.
//
// The old rule has no damping: it rescales difficulty by the whole ratio
// on every share. Two shares a block apart (~6 s) multiply it by about
// ten, and on 3 October 2026 a handful of back-to-back shares took the
// live chain from 7,278 to about 21,000,000 within hours. At that
// difficulty a share takes over an hour of single-thread hashing, far
// longer than the RecencyWindowK blocks a mined header stays valid, and
// an active validator keeps its seat only by landing a share every
// epoch.
//
// It changes the difficulty every later share is checked against, so
// every node needs a binary carrying it before this height, and a fresh
// replay must see the old rule below it. 350,000 was chosen on 3 October
// at height ~192,500: about nine days at ~5 s blocks, twelve at ~6.7 s,
// inside the rolling upgrade that already has to finish before 500,000
// (docs/UPGRADE-2026-10.md).
const SmoothRetargetActivationHeight int64 = 350_000

// DifficultyHalfLife is how far, in seconds, the shares must run behind
// (or ahead of) one per TargetBlockTime for asertDifficulty to halve (or
// double) difficulty. One hour is sixty target intervals: a single share,
// however early, moves difficulty by at most about 1% (60 s of 3600), and
// a two-hour silence divides it by about four.
const DifficultyHalfLife int64 = 3600

// retarget is the difficulty after a share accepted now, given the
// track's current difficulty and its last share's time (ok false if it
// has none), clamped to [MinDifficulty, maxD].
func (k Keeper) retarget(ctx sdk.Context, current math.Int, lastTime int64, ok bool, maxD math.Int) math.Int {
	if !ok {
		return current
	}
	elapsed := ctx.BlockTime().Unix() - lastTime
	var adjusted math.Int
	if ctx.BlockHeight() >= SmoothRetargetActivationHeight {
		adjusted = asertDifficulty(current, k.GetTargetBlockTime(ctx), elapsed, DifficultyHalfLife)
	} else {
		if elapsed <= 0 {
			return current
		}
		adjusted = current.MulRaw(k.GetTargetBlockTime(ctx)).QuoRaw(elapsed)
	}
	if minD := k.GetMinDifficulty(ctx); adjusted.LT(minD) {
		adjusted = minD
	}
	if adjusted.GT(maxD) {
		adjusted = maxD
	}
	return adjusted
}

// asertDifficulty is current × 2^((target − elapsed) / halfLife): the
// relative form of ASERT, the retarget Bitcoin Cash has used since
// November 2020 (aserti3-2d), applied per share. A share on schedule
// leaves difficulty unchanged; early shares raise it and late ones lower
// it, each in proportion to how early or late, so a run of shares moves
// it smoothly and a single fast one barely at all.
//
// Integer-only, for consensus: the exponent is 16.16 fixed point, its
// fractional power of two comes from aserti3-2d's cubic approximation
// (within 0.013% of 2^x), and the result is never below 1. Callers clamp
// to the chain's min and max.
func asertDifficulty(current math.Int, target, elapsed, halfLife int64) math.Int {
	if elapsed < 0 {
		elapsed = 0
	}
	// Past 64 halvings (or doublings) the result is pinned to a clamp
	// anyway; capping here keeps the arithmetic small.
	const maxShift = 64
	if lim := target + maxShift*halfLife; elapsed > lim {
		elapsed = lim
	}
	exponent := ((target - elapsed) * 65536) / halfLife
	shifts := exponent >> 16 // floor, for negative exponents too
	frac := uint64(exponent - shifts<<16)

	// 2^(frac/65536) × 65536, from aserti3-2d.
	factor := 65536 + ((195766423245049*frac + 971821376*frac*frac + 5127*frac*frac*frac + (1 << 47)) >> 48)

	next := new(big.Int).Mul(current.BigInt(), new(big.Int).SetUint64(factor))
	if s := shifts - 16; s >= 0 {
		next.Lsh(next, uint(s))
	} else {
		next.Rsh(next, uint(-s))
	}
	if next.Sign() <= 0 {
		next.SetInt64(1)
	}
	return math.NewIntFromBigInt(next)
}
