package pow

import (
	"encoding/binary"

	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/gates"
)

// Seat-rule hardening: three fixes for the weaknesses docs/SEAT-SIM.md
// confirmed in the rule activating at SeatRuleActivationHeight.
//
//   - A4/A5: the 1% bar is measured against eligible work only. Merged
//     work credited to an address with no consensus key (or a banned one)
//     no longer counts toward the total, so it can't push every validator
//     under the bar (capture by a 1% attacker) or leave nobody qualified
//     (a frozen set).
//   - A6, reserved power: a validator removed for downtime keeps its power
//     at the next boundary, outside the entry budget, if its miner added
//     seat work after the removal (it's back). One that didn't re-enters
//     through the entry limit, as before, so a validator still offline
//     isn't handed back its full power.
//   - A6, liveness window: LongLivenessWindow blocks instead of
//     LivenessWindowSize, still removing at more than half missed. A
//     removal takes about 68 minutes offline instead of 3, so an attacker
//     can't push its share up with short outages, and short outages of
//     our own (a VPN drop, a restart) no longer cost a seat.
//   - A6, removal cap: downtime removals between two boundaries take out
//     at most SeatEntryLimitBps of the power, as the entry limit lets at
//     most that much in. Taking honest validators offline can then raise
//     an attacker's share by at most that much per epoch. A validator past
//     the cap stays in the set; a removal happens only while blocks are
//     being made, so keeping it never halts the chain, it only slows the
//     rounds it would have proposed.
//
// SeatHardeningHeight gates all three. On the testnet it is a
// placeholder until a height is chosen and every validator runs a binary
// carrying it; on a fresh chain (gates.Fresh) it is on from block 1.
const SeatHardeningHeight int64 = 20_001_600

// LongLivenessWindow is the liveness window from SeatHardeningHeight: one
// epoch of blocks.
const LongLivenessWindow int64 = 1_440

// KeyDowntimeReservePrefix holds, for a validator removed for downtime
// since the last boundary, the epoch it was removed in, its power then,
// and its seat work in that epoch then.
var KeyDowntimeReservePrefix = []byte("downtime_reserve/")

func seatHardened(ctx sdk.Context) bool { return gates.On(ctx, SeatHardeningHeight) }

// livenessWindow is the liveness window ctx's block is judged by.
func livenessWindow(ctx sdk.Context) int64 {
	if seatHardened(ctx) {
		return LongLivenessWindow
	}
	return LivenessWindowSize
}

func (k Keeper) getSeatWork(ctx sdk.Context, epoch int64, addr sdk.AccAddress) math.Int {
	bz := ctx.KVStore(k.storeKey).Get(seatWorkKey(epoch, addr))
	if bz == nil {
		return math.ZeroInt()
	}
	var w math.Int
	if err := w.Unmarshal(bz); err != nil {
		return math.ZeroInt()
	}
	return w
}

// powerOrFlat is a validator's power, or the old rule's flat power if it
// has none recorded.
func (k Keeper) powerOrFlat(ctx sdk.Context, addr sdk.AccAddress) int64 {
	if p, ok := k.GetValidatorPower(ctx, addr); ok {
		return p
	}
	return ValidatorVotingPower
}

// mayRemoveForDowntime reports whether removing addr for downtime keeps
// the removals since the last boundary within SeatEntryLimitBps of the
// power (what's active plus what they took out). Always true below
// SeatHardeningHeight.
func (k Keeper) mayRemoveForDowntime(ctx sdk.Context, addr sdk.AccAddress) bool {
	if !seatHardened(ctx) {
		return true
	}
	gone := int64(0)
	store := ctx.KVStore(k.storeKey)
	it := store.Iterator(KeyDowntimeReservePrefix, storetypes.PrefixEndBytes(KeyDowntimeReservePrefix))
	for ; it.Valid(); it.Next() {
		if v := it.Value(); len(v) >= 16 {
			gone += int64(binary.BigEndian.Uint64(v[8:16]))
		}
	}
	it.Close()
	total := gone
	for _, a := range k.IterateActiveValidators(ctx) {
		total += k.powerOrFlat(ctx, a)
	}
	return (gone+k.powerOrFlat(ctx, addr))*10_000 <= total*SeatEntryLimitBps
}

// reserveDowntimePower records a validator removed for downtime, so the
// next boundary can give it back its power if it's back.
func (k Keeper) reserveDowntimePower(ctx sdk.Context, addr sdk.AccAddress) {
	if !seatHardened(ctx) || !gates.On(ctx, SeatRuleActivationHeight) {
		return
	}
	power := k.powerOrFlat(ctx, addr)
	epoch := k.CurrentEpoch(ctx)
	work, _ := k.getSeatWork(ctx, epoch, addr).Marshal()
	bz := make([]byte, 16, 16+len(work))
	binary.BigEndian.PutUint64(bz[:8], uint64(epoch))
	binary.BigEndian.PutUint64(bz[8:], uint64(power))
	ctx.KVStore(k.storeKey).Set(append(append([]byte{}, KeyDowntimeReservePrefix...), addr.Bytes()...), append(bz, work...))
}

// takeDowntimeReserves returns, at the boundary ending epoch, the
// validators removed for downtime since the last boundary whose miners
// have added seat work since, with the power each held; and clears every
// reserve, so one lasts a single boundary.
func (k Keeper) takeDowntimeReserves(ctx sdk.Context, epoch int64) []seat {
	store := ctx.KVStore(k.storeKey)
	it := store.Iterator(KeyDowntimeReservePrefix, storetypes.PrefixEndBytes(KeyDowntimeReservePrefix))
	var keys [][]byte
	var out []seat
	for ; it.Valid(); it.Next() {
		keys = append(keys, append([]byte{}, it.Key()...))
		v := it.Value()
		if len(v) < 16 {
			continue
		}
		addr := sdk.AccAddress(append([]byte{}, it.Key()[len(KeyDowntimeReservePrefix):]...))
		if k.IsActiveValidator(ctx, addr) || k.IsBanned(ctx, addr) {
			continue
		}
		removedIn := int64(binary.BigEndian.Uint64(v[:8]))
		power := int64(binary.BigEndian.Uint64(v[8:16]))
		var then math.Int
		if err := then.Unmarshal(v[16:]); err != nil {
			then = math.ZeroInt()
		}
		now := k.getSeatWork(ctx, epoch, addr)
		// Removed in this epoch: work added since then. Removed in an
		// earlier one (at the boundary block itself): any work this epoch.
		back := now.GT(then)
		if removedIn != epoch {
			back = now.IsPositive()
		}
		if back && power > 0 {
			out = append(out, seat{Addr: addr, Power: power})
		}
	}
	it.Close()
	for _, key := range keys {
		store.Delete(key)
	}
	return out
}

// recordSigningLong is RecordValidatorSigning over a window of
// LongLivenessWindow, one bit per block. A bitmap kept under the old
// window (one byte per block) is dropped and the window starts fresh.
func (k Keeper) recordSigningLong(ctx sdk.Context, minerAddr sdk.AccAddress, signed bool) bool {
	store := ctx.KVStore(k.storeKey)
	bitmapKey := append(append([]byte{}, KeyLivenessBitmapPrefix...), minerAddr.Bytes()...)
	indexKey := append(append([]byte{}, KeyLivenessIndexPrefix...), minerAddr.Bytes()...)
	missedKey := append(append([]byte{}, KeyLivenessMissedPrefix...), minerAddr.Bytes()...)

	size := int((LongLivenessWindow + 7) / 8)
	bitmap := store.Get(bitmapKey)
	index, missed := int64(0), int64(0)
	if len(bitmap) != size {
		bitmap = make([]byte, size)
	} else {
		if bz := store.Get(indexKey); bz != nil {
			index = int64(sdk.BigEndianToUint64(bz))
		}
		if bz := store.Get(missedKey); bz != nil {
			missed = int64(sdk.BigEndianToUint64(bz))
		}
	}

	byteAt, bit := index/8, byte(1)<<(index%8)
	if bitmap[byteAt]&bit != 0 {
		missed-- // the oldest result, a miss, ages out
	}
	if signed {
		bitmap[byteAt] &^= bit
	} else {
		bitmap[byteAt] |= bit
		missed++
	}
	index = (index + 1) % LongLivenessWindow

	store.Set(bitmapKey, bitmap)
	store.Set(indexKey, sdk.Uint64ToBigEndian(uint64(index)))
	store.Set(missedKey, sdk.Uint64ToBigEndian(uint64(missed)))
	return missed*2 > LongLivenessWindow
}
