package accountauth

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
)

// AccountKeeper is the subset of the auth keeper that rotation needs.
// The module's Keeper gains this field when the handler is wired.
type AccountKeeper interface {
	GetAccount(ctx sdk.Context, addr sdk.AccAddress) types.AccountI
	SetAccount(ctx sdk.Context, acc types.AccountI)
}

// ApplyDueRotations is called from the app EndBlocker. It applies every
// pending rotation whose apply height has been reached, by setting the
// account's pubkey to the new key. The address is unchanged.
func ApplyDueRotations(ctx sdk.Context, k Keeper, ak AccountKeeper) {
	for _, account := range k.PendingRotations(ctx) {
		p, ok := k.GetPendingRotation(ctx, account)
		if !ok || ctx.BlockHeight() < p.ApplyHeight {
			continue
		}
		acc := ak.GetAccount(ctx, account)
		if acc == nil {
			k.DeletePendingRotation(ctx, account)
			continue
		}
		newPub := &mldsa.PubKey{Key: p.NewPubkey}
		if err := acc.SetPubKey(newPub); err != nil {
			// A bad pubkey should have been rejected at schedule time.
			// Skip rather than halt.
			continue
		}
		ak.SetAccount(ctx, acc)
		k.DeletePendingRotation(ctx, account)
	}
}
