package pow

import (
	"bytes"
	"sort"

	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	abci "github.com/cometbft/cometbft/abci/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// The seat rule (docs/MAINNET.md §1). Before SeatRuleActivationHeight every
// seat has equal power and anyone with one accepted native submission
// qualifies, so a newcomer with about 12% of native hash and 17 accounts
// can hold 17 of 21 seats. From the height:
//
//   - Seat work is the hashes a miner has proved: each accepted submission
//     adds the difficulty it had to meet. Native and AuxPoW difficulties
//     are the same unit (expected scrypt hashes, target = 2^256/difficulty
//     on both tracks), so the two tracks add up with no further scaling.
//     AuxPoW work is credited to the reward address it commits to. A track
//     few people mine retargets low and counts for little.
//   - Power is in proportion to seat work over the last SeatWorkWindowEpochs
//     epochs (option a), so splitting into many accounts gains nothing.
//     Control needs more than 2/3 of the hashes proved, a halt more than 1/3.
//   - A miner qualifies only with at least SeatMinWorkShareBps of the
//     window's work (option b), and the top TopKSize by work are seated.
//     Accounts too small to make the cut lose their work, so splitting
//     can cost the splitter seats.
//   - Power moves by at most SeatEntryLimitBps of the total each epoch
//     (option c): when the work-proportional powers differ from the
//     current ones by more than that, the new powers are the current ones
//     moved that far toward them. A newcomer with most of the hash takes
//     four epochs (~10 h) to pass 1/3 and seven to pass 2/3.
//
// There is no cap on one validator's power: with a cap, an honest miner's
// excess would be handed to everyone else, accounts an attacker split up
// included.
//
// Below the height none of these keys are read or written.

// SeatRuleActivationHeight switches the seat rule on. A placeholder, far
// above the tip: it changes which addresses validate and with what power,
// so it is set with every operator, to an epoch boundary (a multiple of
// EpochLength), and every node needs a binary carrying it before then.
const SeatRuleActivationHeight int64 = 10_000_800

const (
	// SeatWorkWindowEpochs is how many epochs of seat work power is
	// computed from: this one and the two before it.
	SeatWorkWindowEpochs int64 = 3
	// SeatMinWorkShareBps is the least share of the window's work, in
	// basis points, that qualifies for a seat.
	SeatMinWorkShareBps int64 = 100
	// SeatEntryLimitBps is the most total power, in basis points, that
	// can move between validators in one epoch.
	SeatEntryLimitBps int64 = 1_000
	// SeatPowerScale is about the total voting power the rule hands out.
	SeatPowerScale int64 = 100_000_000
)

var (
	KeySeatWorkPrefix       = []byte("seat_work/")       // epoch (big-endian) + address -> math.Int
	KeyValidatorPowerPrefix = []byte("validator_power/") // address -> power (big-endian)
)

func seatWorkKey(epoch int64, addr sdk.AccAddress) []byte {
	key := append(append([]byte{}, KeySeatWorkPrefix...), sdk.Uint64ToBigEndian(uint64(epoch))...)
	return append(key, addr.Bytes()...)
}

// AddSeatWork credits amount (a difficulty) to addr's seat work in epoch.
func (k Keeper) AddSeatWork(ctx sdk.Context, epoch int64, addr sdk.AccAddress, amount math.Int) {
	if !amount.IsPositive() {
		return
	}
	store := ctx.KVStore(k.storeKey)
	key := seatWorkKey(epoch, addr)
	total := amount
	if bz := store.Get(key); bz != nil {
		var cur math.Int
		if err := cur.Unmarshal(bz); err == nil {
			total = cur.Add(amount)
		}
	}
	bz, _ := total.Marshal()
	store.Set(key, bz)
}

// creditSeatWork is the submission handlers' hook: from
// SeatRuleActivationHeight it adds difficulty to addr's seat work for the
// current epoch.
func (k Keeper) creditSeatWork(ctx sdk.Context, addr sdk.AccAddress, difficulty math.Int) {
	if ctx.BlockHeight() < SeatRuleActivationHeight {
		return
	}
	k.AddSeatWork(ctx, k.CurrentEpoch(ctx), addr, difficulty)
}

// SeatWorkEntry is one address's seat work.
type SeatWorkEntry struct {
	Addr sdk.AccAddress
	Work math.Int
}

// IterateSeatWork returns every address's seat work in epoch, in key order.
func (k Keeper) IterateSeatWork(ctx sdk.Context, epoch int64) []SeatWorkEntry {
	prefix := append(append([]byte{}, KeySeatWorkPrefix...), sdk.Uint64ToBigEndian(uint64(epoch))...)
	it := ctx.KVStore(k.storeKey).Iterator(prefix, storetypes.PrefixEndBytes(prefix))
	defer it.Close()
	var out []SeatWorkEntry
	for ; it.Valid(); it.Next() {
		var w math.Int
		if err := w.Unmarshal(it.Value()); err != nil {
			continue
		}
		out = append(out, SeatWorkEntry{Addr: sdk.AccAddress(bytes.Clone(it.Key()[len(prefix):])), Work: w})
	}
	return out
}

func (k Keeper) pruneSeatWork(ctx sdk.Context, epoch int64) {
	store := ctx.KVStore(k.storeKey)
	for _, e := range k.IterateSeatWork(ctx, epoch) {
		store.Delete(seatWorkKey(epoch, e.Addr))
	}
}

// GetValidatorPower is the power the seat rule last gave addr. Validators
// seated before SeatRuleActivationHeight have none recorded; they hold
// ValidatorVotingPower.
func (k Keeper) GetValidatorPower(ctx sdk.Context, addr sdk.AccAddress) (int64, bool) {
	bz := ctx.KVStore(k.storeKey).Get(append(append([]byte{}, KeyValidatorPowerPrefix...), addr.Bytes()...))
	if bz == nil {
		return 0, false
	}
	return int64(sdk.BigEndianToUint64(bz)), true
}

func (k Keeper) setValidatorPower(ctx sdk.Context, addr sdk.AccAddress, power int64) {
	ctx.KVStore(k.storeKey).Set(append(append([]byte{}, KeyValidatorPowerPrefix...), addr.Bytes()...), sdk.Uint64ToBigEndian(uint64(power)))
}

// seatCandidate is an address's work over the window; Eligible is false
// for one with no consensus key or a ban, whose work still counts toward
// the window's total.
type seatCandidate struct {
	Addr     sdk.AccAddress
	Work     math.Int
	Eligible bool
}

// seat is a validator and the power the rule gives it.
type seat struct {
	Addr  sdk.AccAddress
	Power int64
}

// computeSeats is the seat rule's arithmetic: who is seated, with what
// power, given every address's window work and the current validators'
// powers (prev). ok is false when nobody qualifies, and the set stays as
// it is.
func computeSeats(cands []seatCandidate, prev []seat, topK int64) ([]seat, bool) {
	total := math.ZeroInt()
	for _, c := range cands {
		total = total.Add(c.Work)
	}
	var qualified []seatCandidate
	for _, c := range cands {
		// work/total >= SeatMinWorkShareBps/10_000
		if c.Eligible && c.Work.IsPositive() && c.Work.MulRaw(10_000).GTE(total.MulRaw(SeatMinWorkShareBps)) {
			qualified = append(qualified, c)
		}
	}
	if len(qualified) == 0 {
		return nil, false
	}
	sort.Slice(qualified, func(i, j int) bool {
		if !qualified[i].Work.Equal(qualified[j].Work) {
			return qualified[i].Work.GT(qualified[j].Work)
		}
		return bytes.Compare(qualified[i].Addr, qualified[j].Addr) < 0
	})
	if int64(len(qualified)) > topK {
		qualified = qualified[:topK]
	}
	seatedWork := math.ZeroInt()
	for _, q := range qualified {
		seatedWork = seatedWork.Add(q.Work)
	}

	// target: power in proportion to work; every share is at least
	// SeatMinWorkShareBps of SeatPowerScale, so none rounds to zero.
	target := map[string]int64{}
	order := []sdk.AccAddress{}
	for _, q := range qualified {
		target[string(q.Addr)] = q.Work.MulRaw(SeatPowerScale).Quo(seatedWork).Int64()
		order = append(order, q.Addr)
	}

	// current: the validators' powers rescaled to SeatPowerScale.
	prevTotal := int64(0)
	for _, p := range prev {
		prevTotal += p.Power
	}
	current := map[string]int64{}
	if prevTotal > 0 {
		for _, p := range prev {
			current[string(p.Addr)] = math.NewInt(p.Power).MulRaw(SeatPowerScale).QuoRaw(prevTotal).Int64()
			if _, ok := target[string(p.Addr)]; !ok {
				order = append(order, p.Addr)
			}
		}
	}

	final := target
	limit := SeatPowerScale * SeatEntryLimitBps / 10_000
	if prevTotal > 0 {
		gain := int64(0)
		for _, a := range order {
			if d := target[string(a)] - current[string(a)]; d > 0 {
				gain += d
			}
		}
		if gain > limit {
			// Move each power the fraction limit/gain of the way:
			// current*(1-limit/gain) + target*limit/gain, which is never
			// negative and moves at most limit in all.
			final = map[string]int64{}
			for _, a := range order {
				c, t := current[string(a)], target[string(a)]
				final[string(a)] = (c*(gain-limit) + t*limit) / gain
			}
		}
	}

	var out []seat
	for _, a := range order {
		if p := final[string(a)]; p > 0 {
			out = append(out, seat{Addr: a, Power: p})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Power != out[j].Power {
			return out[i].Power > out[j].Power
		}
		return bytes.Compare(out[i].Addr, out[j].Addr) < 0
	})
	if int64(len(out)) > topK {
		out = out[:topK]
	}
	if len(out) == 0 {
		return nil, false // never empty the set
	}
	return out, true
}

// computeSeatUpdates is ComputeValidatorUpdates from
// SeatRuleActivationHeight.
func (k Keeper) computeSeatUpdates(ctx sdk.Context, epoch int64) []abci.ValidatorUpdate {
	window := map[string]math.Int{}
	var addrs []sdk.AccAddress
	for e := epoch - SeatWorkWindowEpochs + 1; e <= epoch; e++ {
		if e < 0 {
			continue
		}
		for _, w := range k.IterateSeatWork(ctx, e) {
			if cur, ok := window[string(w.Addr)]; ok {
				window[string(w.Addr)] = cur.Add(w.Work)
			} else {
				window[string(w.Addr)] = w.Work
				addrs = append(addrs, w.Addr)
			}
		}
	}
	// The oldest epoch in this window isn't in the next one.
	if e := epoch - SeatWorkWindowEpochs + 1; e >= 0 {
		k.pruneSeatWork(ctx, e)
	}

	cands := make([]seatCandidate, 0, len(addrs))
	for _, a := range addrs {
		_, hasKey := k.GetValidatorPubkey(ctx, a)
		cands = append(cands, seatCandidate{Addr: a, Work: window[string(a)], Eligible: hasKey && !k.IsBanned(ctx, a)})
	}

	active := k.IterateActiveValidators(ctx)
	prev := make([]seat, 0, len(active))
	for _, a := range active {
		if k.IsBanned(ctx, a) {
			continue
		}
		p, ok := k.GetValidatorPower(ctx, a)
		if !ok {
			p = ValidatorVotingPower
		}
		prev = append(prev, seat{Addr: a, Power: p})
	}

	seats, ok := computeSeats(cands, prev, k.GetTopKSize(ctx))
	if !ok {
		k.logger.Info("no qualified validator candidates this epoch, leaving validator set unchanged", "epoch", epoch)
		return nil
	}

	seated := make(map[string]bool, len(seats))
	for _, s := range seats {
		seated[string(s.Addr)] = true
	}
	var updates []abci.ValidatorUpdate
	for _, a := range active {
		if seated[string(a)] {
			continue
		}
		if pubkey, ok := k.GetValidatorPubkey(ctx, a); ok {
			if u, ok := k.toValidatorUpdate(pubkey, 0, a); ok {
				updates = append(updates, u)
			}
		}
		k.RemoveActiveValidator(ctx, a)
	}
	for _, s := range seats {
		pubkey, ok := k.GetValidatorPubkey(ctx, s.Addr)
		if !ok {
			continue
		}
		if u, ok := k.toValidatorUpdate(pubkey, s.Power, s.Addr); ok {
			updates = append(updates, u)
			k.SetActiveValidator(ctx, s.Addr)
			k.setValidatorPower(ctx, s.Addr, s.Power)
		}
	}
	return updates
}
