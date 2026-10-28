package accountauth

import (
	"encoding/binary"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// RotationDelayBlocks is the delay before a current-key rotation or a
// recovery-set change takes effect. One day, about 15,300 blocks at the
// current block time. Recovery-threshold rotations apply immediately.
const RotationDelayBlocks int64 = 15_300

var (
	// KeyPendingRotationPrefix/<account> -> marshaled PendingRotation
	KeyPendingRotationPrefix = []byte("pending_rot/")
	// KeyRecoveryPrefix/<account> -> marshaled RecoveryThreshold
	KeyRecoveryPrefix = []byte("recovery/")
	// KeyRotationNoncePrefix/<account> -> uint64 nonce
	KeyRotationNoncePrefix = []byte("rot_nonce/")
)

func pendingRotationKey(account []byte) []byte {
	return append(append([]byte{}, KeyPendingRotationPrefix...), account...)
}

func recoveryKey(account []byte) []byte {
	return append(append([]byte{}, KeyRecoveryPrefix...), account...)
}

func rotationNonceKey(account []byte) []byte {
	return append(append([]byte{}, KeyRotationNoncePrefix...), account...)
}

// GetRotationNonce returns the nonce the next rotation signature must
// cover. It starts at 0 and increments on every accepted schedule.
func (k Keeper) GetRotationNonce(ctx sdk.Context, account sdk.AccAddress) uint64 {
	bz := ctx.KVStore(k.storeKey).Get(rotationNonceKey(account))
	if bz == nil {
		return 0
	}
	return binary.BigEndian.Uint64(bz)
}

func (k Keeper) setRotationNonce(ctx sdk.Context, account sdk.AccAddress, n uint64) {
	bz := make([]byte, 8)
	binary.BigEndian.PutUint64(bz, n)
	ctx.KVStore(k.storeKey).Set(rotationNonceKey(account), bz)
}

// SetPendingRotation schedules a rotation. It does not apply it.
func (k Keeper) SetPendingRotation(ctx sdk.Context, account sdk.AccAddress, p PendingRotation) {
	bz, err := p.Marshal()
	if err != nil {
		panic(err)
	}
	ctx.KVStore(k.storeKey).Set(pendingRotationKey(account), bz)
	k.setRotationNonce(ctx, account, p.Nonce+1)
}

// GetPendingRotation returns the scheduled rotation, if any.
func (k Keeper) GetPendingRotation(ctx sdk.Context, account sdk.AccAddress) (PendingRotation, bool) {
	bz := ctx.KVStore(k.storeKey).Get(pendingRotationKey(account))
	if bz == nil {
		return PendingRotation{}, false
	}
	var p PendingRotation
	if err := p.Unmarshal(bz); err != nil {
		panic(err)
	}
	return p, true
}

// DeletePendingRotation removes a scheduled rotation.
func (k Keeper) DeletePendingRotation(ctx sdk.Context, account sdk.AccAddress) {
	ctx.KVStore(k.storeKey).Delete(pendingRotationKey(account))
}

// PendingRotations returns every account with a scheduled rotation.
// Used by the EndBlocker. Iteration order is store order.
func (k Keeper) PendingRotations(ctx sdk.Context) []sdk.AccAddress {
	store := ctx.KVStore(k.storeKey)
	it := sdk.KVStorePrefixIterator(store, KeyPendingRotationPrefix)
	defer it.Close()
	var out []sdk.AccAddress
	for ; it.Valid(); it.Next() {
		out = append(out, sdk.AccAddress(it.Key()[len(KeyPendingRotationPrefix):]))
	}
	return out
}
