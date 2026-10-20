package wallet

import (
	"bytes"
	"context"
	"fmt"
	"sort"

	"cosmossdk.io/math"
	cometed25519 "github.com/cometbft/cometbft/crypto/ed25519"
	"github.com/cosmos/cosmos-sdk/client/grpc/cmtservice"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"
	"google.golang.org/protobuf/encoding/protowire"

	"github.com/whoyoujoshin/aether/x/pow"
)

// The seat rule (x/pow/seats.go, docs/SEAT-RULE.md) seats the top miners
// by seat work, the difficulty their accepted submissions met over the
// last pow.SeatWorkWindowEpochs epochs, and gives each power in
// proportion. There is no query for seat work, so it is read from x/pow's
// store, and power from the validator set CometBFT holds.

// Why an address can't be seated under the seat rule.
const (
	IneligibleNoSeatWork   = "no_seat_work_in_window"
	IneligibleBelowMinimum = "below_minimum_share"
)

// Field numbers of the store's kv.Pairs and kv.Pair messages.
const (
	kvPairsField = 1
	kvKeyField   = 1
	kvValueField = 2
)

// seatWindow is every address's seat work over the window ending in epoch,
// in the order the store returned them.
type seatWindow struct {
	order []string
	work  map[string]math.Int
	total math.Int
	// current is each address's seat work in epoch itself.
	current map[string]math.Int
}

func (c *Client) seatWindow(at context.Context, height, epoch int64) (*seatWindow, error) {
	cmt := cmtservice.NewServiceClient(c.conn)
	w := &seatWindow{work: map[string]math.Int{}, total: math.ZeroInt(), current: map[string]math.Int{}}
	for e := epoch - pow.SeatWorkWindowEpochs + 1; e <= epoch; e++ {
		if e < 0 {
			continue
		}
		prefix := append(append([]byte{}, pow.KeySeatWorkPrefix...), sdk.Uint64ToBigEndian(uint64(e))...)
		res, err := cmt.ABCIQuery(at, &cmtservice.ABCIQueryRequest{Path: "/store/" + pow.StoreKey + "/subspace", Data: prefix, Height: height})
		if err != nil {
			return nil, fmt.Errorf("seat work in epoch %d: %w", e, err)
		}
		if res.Code != 0 {
			return nil, fmt.Errorf("seat work in epoch %d: %s", e, res.Log)
		}
		pairs, err := decodeKVPairs(res.Value)
		if err != nil {
			return nil, fmt.Errorf("seat work in epoch %d: %w", e, err)
		}
		for _, p := range pairs {
			if len(p[0]) <= len(prefix) || !bytes.HasPrefix(p[0], prefix) {
				continue
			}
			var amt math.Int
			if err := amt.Unmarshal(p[1]); err != nil {
				continue
			}
			addr := sdk.AccAddress(p[0][len(prefix):]).String()
			if e == epoch {
				w.current[addr] = amt
			}
			if cur, ok := w.work[addr]; ok {
				w.work[addr] = cur.Add(amt)
			} else {
				w.work[addr] = amt
				w.order = append(w.order, addr)
			}
			w.total = w.total.Add(amt)
		}
	}
	return w, nil
}

// decodeKVPairs reads a store subspace query's value: the store's
// internal kv.Pairs message, {repeated Pair pairs = 1}, each Pair
// {bytes key = 1; bytes value = 2}.
func decodeKVPairs(bz []byte) ([][2][]byte, error) {
	var out [][2][]byte
	for len(bz) > 0 {
		num, typ, n := protowire.ConsumeTag(bz)
		if n < 0 {
			return nil, protowire.ParseError(n)
		}
		bz = bz[n:]
		if num != kvPairsField || typ != protowire.BytesType {
			n = protowire.ConsumeFieldValue(num, typ, bz)
			if n < 0 {
				return nil, protowire.ParseError(n)
			}
			bz = bz[n:]
			continue
		}
		pair, n := protowire.ConsumeBytes(bz)
		if n < 0 {
			return nil, protowire.ParseError(n)
		}
		bz = bz[n:]
		var kv [2][]byte
		for len(pair) > 0 {
			num, typ, n := protowire.ConsumeTag(pair)
			if n < 0 {
				return nil, protowire.ParseError(n)
			}
			pair = pair[n:]
			if typ != protowire.BytesType {
				n = protowire.ConsumeFieldValue(num, typ, pair)
				if n < 0 {
					return nil, protowire.ParseError(n)
				}
				pair = pair[n:]
				continue
			}
			v, n := protowire.ConsumeBytes(pair)
			if n < 0 {
				return nil, protowire.ParseError(n)
			}
			pair = pair[n:]
			switch num {
			case kvKeyField:
				kv[0] = v
			case kvValueField:
				kv[1] = v
			}
		}
		out = append(out, kv)
	}
	return out, nil
}

// qualifies is the seat rule's minimum: at least SeatMinWorkShareBps of
// all the window's work, eligible or not.
func qualifies(work, total math.Int) bool {
	return work.IsPositive() && work.MulRaw(10_000).GTE(total.MulRaw(pow.SeatMinWorkShareBps))
}

// share is a over b as a decimal string, or "" when b is zero.
func share(a, b math.Int) string {
	if !b.IsPositive() {
		return ""
	}
	return math.LegacyNewDecFromInt(a).QuoInt(b).String()
}

// validatorPowers maps each validator's consensus address (raw bytes, as
// a string) to its voting power at height, and returns the total.
func (c *Client) validatorPowers(ctx context.Context, height int64) (map[string]int64, int64, error) {
	cmt := cmtservice.NewServiceClient(c.conn)
	res, err := cmt.GetValidatorSetByHeight(ctx, &cmtservice.GetValidatorSetByHeightRequest{Height: height})
	if err != nil {
		return nil, 0, fmt.Errorf("validator set: %w", err)
	}
	powers := make(map[string]int64, len(res.Validators))
	var total int64
	for _, v := range res.Validators {
		_, addr, err := bech32.DecodeAndConvert(v.Address)
		if err != nil {
			return nil, 0, fmt.Errorf("validator address %q: %w", v.Address, err)
		}
		powers[string(addr)] = v.VotingPower
		total += v.VotingPower
	}
	return powers, total, nil
}

// powerOf is the voting power of the validator whose consensus key is
// pubkey (raw ed25519 bytes), or 0.
func powerOf(powers map[string]int64, pubkey []byte) int64 {
	if len(pubkey) != cometed25519.PubKeySize {
		return 0
	}
	return powers[string(cometed25519.PubKey(pubkey).Address())]
}

// applySeatRule fills st's eligibility, rank and on-track fields as the
// seat rule reads them, from the window's work.
func (c *Client) applySeatRule(at context.Context, st *MinerStatus) error {
	w, err := c.seatWindow(at, st.Height, st.Epoch.Index)
	if err != nil {
		return err
	}
	elig, err := c.eligibility(at, st.Height, w.order)
	if err != nil {
		return err
	}
	type cand struct {
		addr string
		work math.Int
	}
	var qualified []cand
	for i, a := range w.order {
		if elig[i].registered && !elig[i].banned && qualifies(w.work[a], w.total) {
			qualified = append(qualified, cand{a, w.work[a]})
		}
	}
	sort.SliceStable(qualified, func(i, j int) bool {
		if !qualified[i].work.Equal(qualified[j].work) {
			return qualified[i].work.GT(qualified[j].work)
		}
		return bytes.Compare(sdk.MustAccAddressFromBech32(qualified[i].addr), sdk.MustAccAddressFromBech32(qualified[j].addr)) < 0
	})
	st.EligibleMiners = len(qualified)
	st.Rank = 0
	for i, q := range qualified {
		if q.addr == st.Address {
			st.Rank = i + 1
		}
	}

	mine, ok := w.work[st.Address]
	if !ok {
		mine = math.ZeroInt()
	}
	st.SeatWork = mine.String()
	st.SeatWorkThisEpoch = "0"
	if cur, ok := w.current[st.Address]; ok {
		st.SeatWorkThisEpoch = cur.String()
	}
	st.SeatWorkTotal = w.total.String()
	st.WorkShare = share(mine, w.total)
	st.NotEligibleBecause = nil
	switch {
	case !mine.IsPositive():
		st.NotEligibleBecause = append(st.NotEligibleBecause, IneligibleNoSeatWork)
	case !qualifies(mine, w.total):
		st.NotEligibleBecause = append(st.NotEligibleBecause, IneligibleBelowMinimum)
	}
	if !st.RegisteredConsensusKey {
		st.NotEligibleBecause = append(st.NotEligibleBecause, IneligibleNoConsensusKey)
	}
	if st.Banned {
		st.NotEligibleBecause = append(st.NotEligibleBecause, IneligibleBanned)
	}
	st.Eligible = len(st.NotEligibleBecause) == 0
	st.OnTrack = st.Eligible && st.Rank > 0 && int64(st.Rank) <= st.TopKSize
	return nil
}
