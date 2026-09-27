package accountauth

import (
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

type Keeper struct {
	cdc      codec.Codec
	storeKey storetypes.StoreKey
	router   baseapp.MessageRouter
}

func NewKeeper(cdc codec.Codec, storeKey storetypes.StoreKey, router baseapp.MessageRouter) Keeper {
	return Keeper{cdc: cdc, storeKey: storeKey, router: router}
}

// NextAuthenticatorID returns and reserves the next id this account
// will use, starting at 1 (0 is never a valid id, so a zero-valued
// Authenticator.Id unambiguously means "not found").
func (k Keeper) NextAuthenticatorID(ctx sdk.Context, account sdk.AccAddress) uint64 {
	store := ctx.KVStore(k.storeKey)
	key := nextIDKey(account)
	bz := store.Get(key)
	var next uint64 = 1
	if bz != nil {
		next = sdk.BigEndianToUint64(bz) + 1
	}
	store.Set(key, sdk.Uint64ToBigEndian(next))
	return next
}

func (k Keeper) SetAuthenticator(ctx sdk.Context, account sdk.AccAddress, a Authenticator) {
	store := ctx.KVStore(k.storeKey)
	store.Set(authenticatorKey(account, a.Id), k.cdc.MustMarshal(&a))
}

func (k Keeper) GetAuthenticator(ctx sdk.Context, account sdk.AccAddress, id uint64) (Authenticator, bool) {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get(authenticatorKey(account, id))
	if bz == nil {
		return Authenticator{}, false
	}
	var a Authenticator
	k.cdc.MustUnmarshal(bz, &a)
	return a, true
}

func (k Keeper) DeleteAuthenticator(ctx sdk.Context, account sdk.AccAddress, id uint64) {
	ctx.KVStore(k.storeKey).Delete(authenticatorKey(account, id))
}

// GetAuthenticators lists every authenticator account has registered,
// for the query service.
func (k Keeper) GetAuthenticators(ctx sdk.Context, account sdk.AccAddress) []Authenticator {
	store := ctx.KVStore(k.storeKey)
	prefix := authenticatorPrefix(account)
	iterator := storetypes.KVStorePrefixIterator(store, prefix)
	defer iterator.Close()

	var out []Authenticator
	for ; iterator.Valid(); iterator.Next() {
		var a Authenticator
		k.cdc.MustUnmarshal(iterator.Value(), &a)
		out = append(out, a)
	}
	return out
}

// saveGuardianSequence persists a GuardianThreshold authenticator's
// advanced next_sequence after an accepted exec -- a small helper so
// msg_server.go doesn't have to reconstruct the whole Authenticator
// wrapper inline.
func (k Keeper) saveGuardianSequence(ctx sdk.Context, account sdk.AccAddress, a Authenticator, gt *GuardianThreshold, newSequence uint64) {
	gt.NextSequence = newSequence
	a.Kind = &Authenticator_GuardianThreshold{GuardianThreshold: gt}
	k.SetAuthenticator(ctx, account, a)
}
