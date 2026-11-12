package accountauth

import (
	"strconv"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/address"

	"github.com/whoyoujoshin/aether/gates"
)

// What an account's key signed stops working when its key changes.
//
// A rotation or a guardian recovery replaces the key stored on an
// account. Before KeyChangeRevokesHeight, everything that key had set up
// to act for the account outlived the change: an x/accountauth
// authenticator (a session key or a guardian_threshold set), an authz
// grant and a fee allowance. So a thief holding the old key could plant
// one, wait for the owner to rotate or the guardians to recover, and go
// on spending through it (GHSA-cgcr-f6g2-ch5v).
//
// From the height every account has a key generation, 0 until its first
// key change and one more on each. Every authenticator, authz grant and
// fee allowance records the generation of the key that made it, and is
// refused once the account's generation has moved on. Nothing is deleted
// at the key change, so a key change costs the same however many of them
// a thief planted. Making them again with the new key brings them back.
//
// Anything made before the height counts as generation 0, so the first
// key change after the height retires all of it.

// KeyChangeRevokesHeight switches this on. Fresh chains have it from
// block 1. On the testnet it needs every validator on a build that has
// it: from the height an older binary charges different gas for a grant
// or a registration, and accepts what this one refuses, so it splits at
// the first such transaction.
//
// Testnet placeholder until a height is chosen.
const KeyChangeRevokesHeight int64 = 60_000_000

var (
	// KeyGenerationPrefix/<len-prefixed account> -> uint64 big-endian.
	KeyGenerationPrefix = []byte("key_gen/")
	// AuthenticatorGenPrefix/<len-prefixed account>/<id> -> the
	// generation that registered it.
	AuthenticatorGenPrefix = []byte("auth_gen/")
	// GrantGenPrefix/<len-prefixed granter><len-prefixed grantee><msg
	// type URL> -> the granter's generation when it granted.
	GrantGenPrefix = []byte("grant_gen/")
	// AllowanceGenPrefix/<len-prefixed granter><len-prefixed grantee> ->
	// the granter's generation when it granted the allowance.
	AllowanceGenPrefix = []byte("allowance_gen/")
)

const (
	EventTypeDelegationsRetired = "key_change_retired_delegations"
	AttributeKeyGeneration      = "key_generation"
)

// KeyChangeRevokes reports whether the rule is on at ctx's height.
func KeyChangeRevokes(ctx sdk.Context) bool {
	return gates.On(ctx, KeyChangeRevokesHeight)
}

func prefixed(p []byte, parts ...[]byte) []byte {
	key := append([]byte{}, p...)
	for _, part := range parts {
		key = append(key, part...)
	}
	return key
}

func (k Keeper) getUint(ctx sdk.Context, key []byte) uint64 {
	bz := ctx.KVStore(k.storeKey).Get(key)
	if bz == nil {
		return 0
	}
	return sdk.BigEndianToUint64(bz)
}

func (k Keeper) setUint(ctx sdk.Context, key []byte, v uint64) {
	ctx.KVStore(k.storeKey).Set(key, sdk.Uint64ToBigEndian(v))
}

// KeyGeneration is how many times account's key has changed since
// KeyChangeRevokesHeight.
func (k Keeper) KeyGeneration(ctx sdk.Context, account sdk.AccAddress) uint64 {
	return k.getUint(ctx, prefixed(KeyGenerationPrefix, address.MustLengthPrefix(account)))
}

// keyChanged moves account to its next generation, retiring everything
// its earlier keys set up. Called wherever the stored key changes.
func (k Keeper) keyChanged(ctx sdk.Context, account sdk.AccAddress) {
	if !KeyChangeRevokes(ctx) {
		return
	}
	gen := k.KeyGeneration(ctx, account) + 1
	k.setUint(ctx, prefixed(KeyGenerationPrefix, address.MustLengthPrefix(account)), gen)
	ctx.EventManager().EmitEvent(sdk.NewEvent(EventTypeDelegationsRetired,
		sdk.NewAttribute(AttributeAccount, account.String()),
		sdk.NewAttribute(AttributeKeyGeneration, strconv.FormatUint(gen, 10))))
}

func authenticatorGenKey(account sdk.AccAddress, id uint64) []byte {
	return prefixed(AuthenticatorGenPrefix, address.MustLengthPrefix(account), idBytes(id))
}

// recordAuthenticator notes the generation that registered authenticator
// id.
func (k Keeper) recordAuthenticator(ctx sdk.Context, account sdk.AccAddress, id uint64) {
	if KeyChangeRevokes(ctx) {
		k.setUint(ctx, authenticatorGenKey(account, id), k.KeyGeneration(ctx, account))
	}
}

// AuthenticatorCurrent reports whether authenticator id was registered
// by the account's current key (always true before the height).
func (k Keeper) AuthenticatorCurrent(ctx sdk.Context, account sdk.AccAddress, id uint64) bool {
	if !KeyChangeRevokes(ctx) {
		return true
	}
	return k.getUint(ctx, authenticatorGenKey(account, id)) == k.KeyGeneration(ctx, account)
}

func (k Keeper) forgetAuthenticator(ctx sdk.Context, account sdk.AccAddress, id uint64) {
	ctx.KVStore(k.storeKey).Delete(authenticatorGenKey(account, id))
}

func grantGenKey(granter, grantee sdk.AccAddress, msgTypeURL string) []byte {
	return prefixed(GrantGenPrefix, address.MustLengthPrefix(granter), address.MustLengthPrefix(grantee), []byte(msgTypeURL))
}

// RecordGrant notes the granter's generation for an authz grant.
func (k Keeper) RecordGrant(ctx sdk.Context, granter, grantee sdk.AccAddress, msgTypeURL string) {
	if KeyChangeRevokes(ctx) {
		k.setUint(ctx, grantGenKey(granter, grantee, msgTypeURL), k.KeyGeneration(ctx, granter))
	}
}

// ForgetGrant drops the record when the grant is revoked.
func (k Keeper) ForgetGrant(ctx sdk.Context, granter, grantee sdk.AccAddress, msgTypeURL string) {
	if KeyChangeRevokes(ctx) {
		ctx.KVStore(k.storeKey).Delete(grantGenKey(granter, grantee, msgTypeURL))
	}
}

// GrantCurrent reports whether the granter's current key made the grant.
func (k Keeper) GrantCurrent(ctx sdk.Context, granter, grantee sdk.AccAddress, msgTypeURL string) bool {
	if !KeyChangeRevokes(ctx) {
		return true
	}
	return k.getUint(ctx, grantGenKey(granter, grantee, msgTypeURL)) == k.KeyGeneration(ctx, granter)
}

func allowanceGenKey(granter, grantee sdk.AccAddress) []byte {
	return prefixed(AllowanceGenPrefix, address.MustLengthPrefix(granter), address.MustLengthPrefix(grantee))
}

// RecordAllowance notes the granter's generation for a fee allowance.
func (k Keeper) RecordAllowance(ctx sdk.Context, granter, grantee sdk.AccAddress) {
	if KeyChangeRevokes(ctx) {
		k.setUint(ctx, allowanceGenKey(granter, grantee), k.KeyGeneration(ctx, granter))
	}
}

// ForgetAllowance drops the record when the allowance is revoked.
func (k Keeper) ForgetAllowance(ctx sdk.Context, granter, grantee sdk.AccAddress) {
	if KeyChangeRevokes(ctx) {
		ctx.KVStore(k.storeKey).Delete(allowanceGenKey(granter, grantee))
	}
}

// AllowanceCurrent reports whether the granter's current key made the
// fee allowance.
func (k Keeper) AllowanceCurrent(ctx sdk.Context, granter, grantee sdk.AccAddress) bool {
	if !KeyChangeRevokes(ctx) {
		return true
	}
	return k.getUint(ctx, allowanceGenKey(granter, grantee)) == k.KeyGeneration(ctx, granter)
}
