package pow

import (
	"bytes"
	"sort"

	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	abci "github.com/cometbft/cometbft/abci/types"
	cometed25519 "github.com/cometbft/cometbft/crypto/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/gates"
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

// SeatRuleActivationHeight switches the seat rule on, and the bond floor
// with it. It changes which addresses validate and with what power, so
// every node needs a binary carrying it before then
// (docs/UPGRADE-SEAT-RULE.md). Set on 19 October 2026 at tip ~349,400
// (5.6 s blocks) to 396,000, an epoch boundary (275 x 1,440), about
// three days out. Earlier binaries carried 10,000,800 as a placeholder.
const SeatRuleActivationHeight int64 = 396_000

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
	if !gates.On(ctx, SeatRuleActivationHeight) {
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
// it is. eligibleTotal measures the 1% bar against eligible candidates'
// work only (from SeatHardeningHeight); otherwise every candidate's work
// counts toward it.
func computeSeats(cands []seatCandidate, prev []seat, topK int64, eligibleTotal bool) ([]seat, bool) {
	total := math.ZeroInt()
	for _, c := range cands {
		if eligibleTotal && !c.Eligible {
			continue
		}
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
	fresh := gates.Fresh(ctx.ChainID())
	prev := make([]seat, 0, len(active))
	at := map[string]int{}
	for _, a := range active {
		if k.IsBanned(ctx, a) {
			continue
		}
		p, ok := k.GetValidatorPower(ctx, a)
		if !ok {
			p = ValidatorVotingPower
		}
		owner := a
		if fresh {
			// A genesis validator starts active under an address made from
			// its own consensus key (BootstrapValidator). A miner that
			// registers that key takes the validator over, power and all,
			// so one key never holds two seats.
			if heir := k.keyHeir(ctx, a); heir != nil {
				owner = heir
			}
		}
		if i, ok := at[string(owner)]; ok {
			prev[i].Power += p
			continue
		}
		at[string(owner)] = len(prev)
		prev = append(prev, seat{Addr: owner, Power: p})
	}
	hardened := seatHardened(ctx)
	if hardened {
		// A validator removed for downtime this epoch that's back counts
		// as holding its power, so it returns outside the entry budget.
		for _, r := range k.takeDowntimeReserves(ctx, epoch) {
			if _, ok := at[string(r.Addr)]; !ok {
				at[string(r.Addr)] = len(prev)
				prev = append(prev, r)
			}
		}
	}

	seats, ok := computeSeats(cands, prev, k.GetTopKSize(ctx), hardened)
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
		heir := fresh && k.keyHeir(ctx, a) != nil
		if pubkey, ok := k.GetValidatorPubkey(ctx, a); ok && !heir {
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

// ValidatorBondFloor is the most of a validator's rewards held in escrow
// from SeatRuleActivationHeight: 1,000 AETH. Below the height every reward
// a validator earns stays in escrow while it is active, so a pool seated
// by its merged-mining work couldn't pay its miners. From the height
// escrow fills up to the floor and the rest is paid out. The floor stays
// locked while the validator is active, and burned if it equivocates.
const ValidatorBondFloor int64 = 1_000_000_000

// escrowReward is AddEscrow for a validator's reward. Below
// SeatRuleActivationHeight it escrows all of amount. From it, it escrows
// up to ValidatorBondFloor and pays the rest to addr; the unlock height
// moves forward either way, so the bond stays locked while addr earns. A
// payout that fails is escrowed instead.
func (k Keeper) escrowReward(ctx sdk.Context, addr sdk.AccAddress, amount math.Int) {
	if !gates.On(ctx, SeatRuleActivationHeight) {
		k.AddEscrow(ctx, addr, amount)
		return
	}
	room := math.NewInt(ValidatorBondFloor).Sub(k.GetEscrowBalance(ctx, addr))
	if room.IsNegative() {
		room = math.ZeroInt()
	}
	held := math.MinInt(amount, room)
	if paid := amount.Sub(held); paid.IsPositive() {
		if err := k.bankKeeper.SendCoinsFromModuleToAccount(ctx, ModuleName, addr, sdk.NewCoins(sdk.NewCoin("uaeth", paid))); err != nil {
			k.logger.Error("failed to pay a validator's reward above the bond floor; escrowed it", "validator", addr.String(), "error", err)
			held = amount
		}
	}
	k.AddEscrow(ctx, addr, held)
}

// keyHeir is the miner that has taken over genesis validator a's
// consensus key, or nil: a must be the address BootstrapValidator made
// from that key, and another address must have registered the same key
// since.
func (k Keeper) keyHeir(ctx sdk.Context, a sdk.AccAddress) sdk.AccAddress {
	pubkey, ok := k.GetValidatorPubkey(ctx, a)
	if !ok || len(pubkey) != cometed25519.PubKeySize {
		return nil
	}
	consAddr := cometed25519.PubKey(pubkey).Address()
	if !a.Equals(sdk.AccAddress(consAddr)) {
		return nil
	}
	holder, ok := k.GetMinerByConsensusAddr(ctx, consAddr)
	if !ok || holder.Equals(a) {
		return nil
	}
	if held, ok := k.GetValidatorPubkey(ctx, holder); !ok || !bytes.Equal(held, pubkey) {
		return nil
	}
	return holder
}
