package app

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/x/accountauth"
)

// AccountAuthActivationHeight is the first block that executes with
// x/accountauth live.
//
// PLACEHOLDER, same as IBCActivationHeight's own placeholder -- this
// value is not yet coordinated with the operator and MUST be replaced
// with a real, agreed height (based on the live chain tip at the time)
// before this activates anywhere. An un-restarted node just halts at
// it (see checkAccountAuthActivation), so the practical failure mode is
// a halted node, not silent data corruption.
//
// Follows the exact same mechanism as AuthzFeegrantActivationHeight and
// IBCActivationHeight (see app/authz_feegrant.go's doc comment for the
// full rationale: a live multistore can't just start mounting new KV
// stores, and every mounted store -- even an empty one -- is a leaf in
// the AppHash, so activation must be height-gated and restart-driven).
const AccountAuthActivationHeight int64 = 300_000

// accountAuthActivationHeight is what New() actually reads, so tests
// can exercise the store-upgrade path at a small height instead of
// committing 300k blocks.
var accountAuthActivationHeight = AccountAuthActivationHeight

var accountAuthStoreKeys = []string{accountauth.StoreKey}

type accountAuthPlan struct {
	// wire: mount the store and register the keeper, module and
	// message types for this run.
	wire bool
	// addStores: this is the one startup that creates the store.
	addStores bool
}

// planAccountAuth decides wiring from the last committed height. See
// planAuthzFeegrant, which this exactly mirrors.
func planAccountAuth(lastCommittedHeight, activationHeight int64) accountAuthPlan {
	if lastCommittedHeight < activationHeight-1 {
		return accountAuthPlan{}
	}
	return accountAuthPlan{wire: true, addStores: lastCommittedHeight == activationHeight-1}
}

// accountAuthMarkerKey is written into the new store by the activation
// block, for the same reason authzFeegrantMarkerKey is: an IAVL store
// that stays empty at a later version fails to load on the next restart
// ("version does not exist"), and fails every query at the latest
// height the same way. Prefixed 0xff, used by no key this module writes
// (next_id/... and auth/... only), so no iteration or genesis export
// ever sees it.
var accountAuthMarkerKey = []byte("\xffaether/accountauth-activated")

// checkAccountAuthActivation halts a node that reaches the activation
// height without the store mounted, exactly like
// checkAuthzFeegrantActivation (see that function's doc comment for the
// restart mechanics).
func (app *App) checkAccountAuthActivation(ctx sdk.Context) error {
	if app.accountAuthWired {
		if ctx.BlockHeight() == accountAuthActivationHeight {
			for _, name := range accountAuthStoreKeys {
				ctx.KVStore(app.keys[name]).Set(accountAuthMarkerKey, []byte{1})
			}
		}
		return nil
	}
	if ctx.BlockHeight() < accountAuthActivationHeight {
		return nil
	}
	return fmt.Errorf(
		"x/accountauth activates at height %d: restart this node (e.g. `systemctl restart aetherd`) to add its store, then it will resume from this block",
		accountAuthActivationHeight,
	)
}
