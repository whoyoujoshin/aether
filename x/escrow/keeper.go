package escrow

import (
	"context"
	"fmt"
	"strconv"

	"cosmossdk.io/log"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// BankKeeper is what escrow needs from x/bank: moving money into and
// out of its module account.
type BankKeeper interface {
	SendCoinsFromAccountToModule(ctx context.Context, sender sdk.AccAddress, recipientModule string, amt sdk.Coins) error
	SendCoinsFromModuleToAccount(ctx context.Context, senderModule string, recipientAddr sdk.AccAddress, amt sdk.Coins) error
}

type Keeper struct {
	cdc      codec.BinaryCodec
	storeKey storetypes.StoreKey
	bank     BankKeeper
	logger   log.Logger
}

func NewKeeper(cdc codec.BinaryCodec, storeKey storetypes.StoreKey, bank BankKeeper, logger log.Logger) Keeper {
	return Keeper{cdc: cdc, storeKey: storeKey, bank: bank, logger: logger.With("module", "x/"+ModuleName)}
}

func (k Keeper) nextID(ctx sdk.Context) uint64 {
	bz := ctx.KVStore(k.storeKey).Get(KeyNextID)
	if bz == nil {
		return 1
	}
	return sdk.BigEndianToUint64(bz)
}

func (k Keeper) setNextID(ctx sdk.Context, id uint64) {
	ctx.KVStore(k.storeKey).Set(KeyNextID, u64(id))
}

func (k Keeper) GetEscrow(ctx sdk.Context, id uint64) (Escrow, bool) {
	bz := ctx.KVStore(k.storeKey).Get(escrowKey(id))
	if bz == nil {
		return Escrow{}, false
	}
	var e Escrow
	k.cdc.MustUnmarshal(bz, &e)
	return e, true
}

func (k Keeper) OpenCount(ctx sdk.Context, payer sdk.AccAddress) uint64 {
	bz := ctx.KVStore(k.storeKey).Get(openCountKey(payer))
	if bz == nil {
		return 0
	}
	return sdk.BigEndianToUint64(bz)
}

func (k Keeper) setOpenCount(ctx sdk.Context, payer sdk.AccAddress, n uint64) {
	store := ctx.KVStore(k.storeKey)
	if n == 0 {
		store.Delete(openCountKey(payer))
		return
	}
	store.Set(openCountKey(payer), u64(n))
}

// putEscrow stores e and every index entry for it.
func (k Keeper) putEscrow(ctx sdk.Context, e Escrow) {
	store := ctx.KVStore(k.storeKey)
	store.Set(escrowKey(e.Id), k.cdc.MustMarshal(&e))
	store.Set(expiryKey(e.ExpiresAt, e.Id), []byte{})
	for _, p := range e.parties() {
		store.Set(partyKey(sdk.MustAccAddressFromBech32(p), e.Id), []byte{})
	}
	payer := sdk.MustAccAddressFromBech32(e.Payer)
	k.setOpenCount(ctx, payer, k.OpenCount(ctx, payer)+1)
}

func (k Keeper) deleteEscrow(ctx sdk.Context, e Escrow) {
	store := ctx.KVStore(k.storeKey)
	store.Delete(escrowKey(e.Id))
	store.Delete(expiryKey(e.ExpiresAt, e.Id))
	for _, p := range e.parties() {
		store.Delete(partyKey(sdk.MustAccAddressFromBech32(p), e.Id))
	}
	payer := sdk.MustAccAddressFromBech32(e.Payer)
	if n := k.OpenCount(ctx, payer); n > 0 {
		k.setOpenCount(ctx, payer, n-1)
	}
}

// create locks e.Amount from the payer and stores e under a new id.
func (k Keeper) create(ctx sdk.Context, e Escrow) (uint64, error) {
	payer := sdk.MustAccAddressFromBech32(e.Payer)
	if k.OpenCount(ctx, payer) >= MaxOpenPerPayer {
		return 0, ErrTooManyOpen.Wrapf("%s has %d open escrows; settle some first", e.Payer, MaxOpenPerPayer)
	}
	if err := k.bank.SendCoinsFromAccountToModule(ctx, payer, ModuleName, e.Amount); err != nil {
		return 0, err
	}
	e.Id = k.nextID(ctx)
	e.CreatedHeight = ctx.BlockHeight()
	k.setNextID(ctx, e.Id+1)
	k.putEscrow(ctx, e)

	attrs := []sdk.Attribute{
		sdk.NewAttribute(AttributeID, strconv.FormatUint(e.Id, 10)),
		sdk.NewAttribute(AttributePayer, e.Payer),
		sdk.NewAttribute(AttributePayee, e.Payee),
		sdk.NewAttribute(AttributeAmount, e.Amount.String()),
		sdk.NewAttribute(AttributeExpiresAt, strconv.FormatInt(e.ExpiresAt, 10)),
		sdk.NewAttribute(AttributeOnExpiry, e.OnExpiry.String()),
	}
	if e.Arbiter != "" {
		attrs = append(attrs, sdk.NewAttribute(AttributeArbiter, e.Arbiter))
	}
	if e.Terms != "" {
		attrs = append(attrs, sdk.NewAttribute(AttributeTerms, e.Terms))
	}
	ctx.EventManager().EmitEvent(sdk.NewEvent(EventTypeCreated, attrs...))
	return e.Id, nil
}

// settle pays e out -- to the payee when release, else back to the
// payer -- and deletes it. by is who settled it, or ByExpiry.
func (k Keeper) settle(ctx sdk.Context, e Escrow, release bool, by string) error {
	to, eventType := e.Payer, EventTypeRefunded
	if release {
		to, eventType = e.Payee, EventTypeReleased
	}
	if err := k.bank.SendCoinsFromModuleToAccount(ctx, ModuleName, sdk.MustAccAddressFromBech32(to), e.Amount); err != nil {
		return err
	}
	k.deleteEscrow(ctx, e)
	ctx.EventManager().EmitEvent(sdk.NewEvent(eventType,
		sdk.NewAttribute(AttributeID, strconv.FormatUint(e.Id, 10)),
		sdk.NewAttribute(AttributePayer, e.Payer),
		sdk.NewAttribute(AttributePayee, e.Payee),
		sdk.NewAttribute(AttributeAmount, e.Amount.String()),
		sdk.NewAttribute(AttributeBy, by),
	))
	return nil
}

// ProcessExpired settles up to MaxExpiriesPerBlock escrows whose
// deadline has passed, earliest first, the way each one's payer chose.
// Any left over wait for the next block.
func (k Keeper) ProcessExpired(ctx sdk.Context) {
	now := ctx.BlockTime().Unix()
	if now < 0 {
		return
	}
	store := ctx.KVStore(k.storeKey)
	end := join(KeyExpiryPrefix, u64(uint64(now)+1))
	it := store.Iterator(KeyExpiryPrefix, end)
	var due []uint64
	for ; it.Valid() && len(due) < MaxExpiriesPerBlock; it.Next() {
		key := it.Key()
		due = append(due, sdk.BigEndianToUint64(key[len(key)-8:]))
	}
	it.Close()

	for _, id := range due {
		e, ok := k.GetEscrow(ctx, id)
		if !ok {
			continue
		}
		// Each in its own cache, so one that can't be paid out (which
		// would mean the module account is short -- a bug) is skipped
		// without touching the rest.
		cache, write := ctx.CacheContext()
		if err := k.settle(cache, e, e.OnExpiry == ON_EXPIRY_RELEASE, ByExpiry); err != nil {
			k.logger.Error("couldn't settle expired escrow", "id", id, "error", err)
			continue
		}
		write() // also passes its events on
	}
}

// OpenEscrowsOf lists escrows addr is a party to with id > afterID,
// oldest first, at most limit; next is the id to continue after, or 0.
func (k Keeper) OpenEscrowsOf(ctx sdk.Context, addr sdk.AccAddress, afterID uint64, limit int) (out []Escrow, next uint64) {
	store := ctx.KVStore(k.storeKey)
	prefix := partyPrefix(addr)
	it := store.Iterator(join(prefix, u64(afterID+1)), storetypes.PrefixEndBytes(prefix))
	defer it.Close()
	for ; it.Valid(); it.Next() {
		if len(out) == limit {
			return out, out[len(out)-1].Id
		}
		key := it.Key()
		if e, ok := k.GetEscrow(ctx, sdk.BigEndianToUint64(key[len(key)-8:])); ok {
			out = append(out, e)
		}
	}
	return out, 0
}

func (k Keeper) allEscrows(ctx sdk.Context) []Escrow {
	it := storetypes.KVStorePrefixIterator(ctx.KVStore(k.storeKey), KeyEscrowPrefix)
	defer it.Close()
	var out []Escrow
	for ; it.Valid(); it.Next() {
		var e Escrow
		k.cdc.MustUnmarshal(it.Value(), &e)
		out = append(out, e)
	}
	return out
}

func (k Keeper) InitGenesis(ctx sdk.Context, g GenesisState) {
	if err := g.Validate(); err != nil {
		panic(fmt.Errorf("escrow genesis: %w", err))
	}
	k.setNextID(ctx, g.NextId)
	for _, e := range g.Escrows {
		k.putEscrow(ctx, e)
	}
}

func (k Keeper) ExportGenesis(ctx sdk.Context) GenesisState {
	return GenesisState{NextId: k.nextID(ctx), Escrows: k.allEscrows(ctx)}
}
