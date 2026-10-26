package inflowcap

import (
	"context"

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

// Caps is each entry with its voucher's supply and the room left.
func (k Keeper) Caps(ctx sdk.Context) []CapStatus {
	p := k.GetParams(ctx)
	out := make([]CapStatus, 0, len(p.Entries))
	for _, e := range p.Entries {
		v := e.VoucherDenom()
		supply := k.Supply(ctx, v).Amount
		out = append(out, CapStatus{Entry: e, VoucherDenom: v, Supply: supply, Room: Room(e.Cap, supply)})
	}
	return out
}

func (k Keeper) InitGenesis(ctx sdk.Context, g GenesisState) {
	if err := g.Validate(); err != nil {
		panic(err)
	}
	k.SetParams(ctx, g.Params)
}

func (k Keeper) ExportGenesis(ctx sdk.Context) GenesisState {
	p := k.GetParams(ctx)
	if p.Entries == nil {
		p.Entries = []Entry{}
	}
	return GenesisState{Params: p}
}
