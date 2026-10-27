package inflowcap

import (
	"context"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// BankKeeper is what the cap reads from x/bank: a denom's total supply.
type BankKeeper interface {
	GetSupply(ctx context.Context, denom string) sdk.Coin
}

type Keeper struct {
	cdc       codec.BinaryCodec
	storeKey  storetypes.StoreKey
	bank      BankKeeper
	authority string
}

// NewKeeper's authority is the only signer MsgUpdateParams accepts: the
// governance module account.
func NewKeeper(cdc codec.BinaryCodec, storeKey storetypes.StoreKey, bank BankKeeper, authority string) Keeper {
	return Keeper{cdc: cdc, storeKey: storeKey, bank: bank, authority: authority}
}

func (k Keeper) GetAuthority() string { return k.authority }

// GetParams is the allowlist; empty (everything refused) if never set.
func (k Keeper) GetParams(ctx sdk.Context) Params {
	bz := ctx.KVStore(k.storeKey).Get(KeyParams)
	if bz == nil {
		return Params{}
	}
	var p Params
	k.cdc.MustUnmarshal(bz, &p)
	return p
}

// HasParams reports whether the allowlist was ever set.
func (k Keeper) HasParams(ctx sdk.Context) bool {
	return ctx.KVStore(k.storeKey).Has(KeyParams)
}

func (k Keeper) SetParams(ctx sdk.Context, p Params) {
	bz := k.cdc.MustMarshal(&p)
	if bz == nil {
		bz = []byte{} // the empty list: a store refuses a nil value
	}
	ctx.KVStore(k.storeKey).Set(KeyParams, bz)
}

// Supply is voucher's total supply on Aether.
func (k Keeper) Supply(ctx sdk.Context, voucher string) sdk.Coin {
	return k.bank.GetSupply(ctx, voucher)
}

// Caps is each entry with its voucher's supply, its sends in flight and
// the room left.
func (k Keeper) Caps(ctx sdk.Context) []CapStatus {
	p := k.GetParams(ctx)
	out := make([]CapStatus, 0, len(p.Entries))
	for _, e := range p.Entries {
		v := e.VoucherDenom()
		supply := k.Supply(ctx, v).Amount
		inFlight := k.InFlight(ctx, v)
		room := sdkmath.ZeroInt()
		if held, ok := Held(supply, inFlight); ok {
			room = Room(e.Cap, held)
		}
		out = append(out, CapStatus{Entry: e, VoucherDenom: v, Supply: supply, InFlight: inFlight, Room: room})
	}
	return out
}

// InFlight is the sum of voucher's sends back not yet acknowledged or
// timed out.
func (k Keeper) InFlight(ctx sdk.Context, voucher string) sdkmath.Int {
	bz := ctx.KVStore(k.storeKey).Get(InFlightTotalKey(voucher))
	if bz == nil {
		return sdkmath.ZeroInt()
	}
	var total sdkmath.Int
	if err := total.Unmarshal(bz); err != nil {
		panic(err)
	}
	return total
}

func (k Keeper) setInFlightTotal(ctx sdk.Context, voucher string, total sdkmath.Int) {
	store := ctx.KVStore(k.storeKey)
	if !total.IsPositive() {
		store.Delete(InFlightTotalKey(voucher))
		return
	}
	bz, err := total.Marshal()
	if err != nil {
		panic(err)
	}
	store.Set(InFlightTotalKey(voucher), bz)
}

// AddInFlight records a send back whose burn a failure would re-mint.
func (k Keeper) AddInFlight(ctx sdk.Context, s InFlightSend) {
	k.ReleaseInFlight(ctx, s.PortId, s.ChannelId, s.Sequence) // never there: sequences don't repeat
	ctx.KVStore(k.storeKey).Set(InFlightSendKey(s.PortId, s.ChannelId, s.Sequence), k.cdc.MustMarshal(&s))
	k.setInFlightTotal(ctx, s.VoucherDenom, k.InFlight(ctx, s.VoucherDenom).Add(s.Amount))
}

// GetInFlightSend is the recorded send of sequence over port and channel,
// if there is one.
func (k Keeper) GetInFlightSend(ctx sdk.Context, portID, channelID string, sequence uint64) (InFlightSend, bool) {
	bz := ctx.KVStore(k.storeKey).Get(InFlightSendKey(portID, channelID, sequence))
	if bz == nil {
		return InFlightSend{}, false
	}
	var s InFlightSend
	k.cdc.MustUnmarshal(bz, &s)
	return s, true
}

// ReleaseInFlight forgets the send of sequence over port and channel, if
// it was recorded, once its packet is settled: a success leaves the burn
// final, and a failure's refund has just put it back into the supply.
func (k Keeper) ReleaseInFlight(ctx sdk.Context, portID, channelID string, sequence uint64) {
	store := ctx.KVStore(k.storeKey)
	key := InFlightSendKey(portID, channelID, sequence)
	bz := store.Get(key)
	if bz == nil {
		return
	}
	var s InFlightSend
	k.cdc.MustUnmarshal(bz, &s)
	store.Delete(key)
	k.setInFlightTotal(ctx, s.VoucherDenom, k.InFlight(ctx, s.VoucherDenom).Sub(s.Amount))
}

// InFlightSends is every recorded send, in key order.
func (k Keeper) InFlightSends(ctx sdk.Context) []InFlightSend {
	it := storetypes.KVStorePrefixIterator(ctx.KVStore(k.storeKey), KeyInFlightSendPrefix)
	defer it.Close()
	out := []InFlightSend{}
	for ; it.Valid(); it.Next() {
		var s InFlightSend
		k.cdc.MustUnmarshal(it.Value(), &s)
		out = append(out, s)
	}
	return out
}

func (k Keeper) InitGenesis(ctx sdk.Context, g GenesisState) {
	if err := g.Validate(); err != nil {
		panic(err)
	}
	k.SetParams(ctx, g.Params)
	for _, s := range g.InFlight {
		k.AddInFlight(ctx, s)
	}
}

func (k Keeper) ExportGenesis(ctx sdk.Context) GenesisState {
	p := k.GetParams(ctx)
	if p.Entries == nil {
		p.Entries = []Entry{}
	}
	return GenesisState{Params: p, InFlight: k.InFlightSends(ctx)}
}
