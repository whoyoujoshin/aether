package app

import (
	"fmt"

	storetypes "cosmossdk.io/store/types"
	"cosmossdk.io/x/feegrant"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authzkeeper "github.com/cosmos/cosmos-sdk/x/authz/keeper"
)

// AuthzFeegrantActivationHeight is the first block that executes with
// x/authz and x/feegrant live.
//
// Unlike every earlier gate in this project, this one adds new KV
// stores, and that changes how it has to work:
//
//   - The multistore refuses to load a store key missing from the last
//     commit unless it's declared via StoreUpgrades (store/rootmulti
//     loadVersion: "new stores should be added using StoreUpgrades"),
//     so a live node can't simply start mounting them.
//   - Every mounted IAVL store, even an empty one, is a leaf in the
//     AppHash Merkle map (store/rootmulti commitStores has no
//     skip-empty case). Mounting these from genesis would change the
//     AppHash of every historical block and break fresh-node replay at
//     block 1 -- an in-handler height check can't prevent that.
//
// So the wiring itself is height-aware, following x/upgrade's pattern
// inside a single binary: a node whose last committed height is below
// AuthzFeegrantActivationHeight-1 builds the app exactly as before
// (no stores, keepers, modules, or interface registrations -- identical
// behavior to the previous binary), and refuses to execute the
// activation block. Restarting it at that point (last committed height
// == AuthzFeegrantActivationHeight-1) mounts both stores via a one-time
// StoreUpgrades{Added}, and every later restart loads them normally.
//
// Operationally this decouples installing the binary from activating
// the feature: any node may run this binary at any point before the
// activation height, even while others still run the old one, with no
// divergence. Every node must be on this binary by the activation
// height, and each node halts there once ("CONSENSUS FAILURE", block
// not committed) until restarted -- `systemctl restart aetherd`.
//
// Matches QuorumActiveValidatorCountActivationHeight: both ride the
// coordinated cutover agreed on 2026-09-26 with the live tip at 107,176
// (the 100,000 placeholder had already been passed). A node that first
// starts this binary after activation-1 is committed can't load the
// stores it never added -- so every node must be running it before
// this height, and restart once at the halt.
const AuthzFeegrantActivationHeight int64 = 109_000

// authzFeegrantActivationHeight is what New() actually reads, so tests
// can exercise the store-upgrade path at a small height instead of
// committing 100k blocks.
var authzFeegrantActivationHeight = AuthzFeegrantActivationHeight

var authzFeegrantStoreKeys = []string{authzkeeper.StoreKey, feegrant.StoreKey}

type authzFeegrantPlan struct {
	// wire: mount the stores and register keepers, modules and message
	// types for this run.
	wire bool
	// addStores: this is the one startup that creates the stores.
	addStores bool
}

// planAuthzFeegrant decides wiring from the last committed height. The
// stores must exist while the activation block executes, i.e. from the
// startup whose last committed height is activation-1.
func planAuthzFeegrant(lastCommittedHeight, activationHeight int64) authzFeegrantPlan {
	if lastCommittedHeight < activationHeight-1 {
		return authzFeegrantPlan{}
	}
	return authzFeegrantPlan{wire: true, addStores: lastCommittedHeight == activationHeight-1}
}

func authzFeegrantStoreLoader(ms storetypes.CommitMultiStore) error {
	return ms.LoadLatestVersionAndUpgrade(&storetypes.StoreUpgrades{Added: authzFeegrantStoreKeys})
}

// authzFeegrantMarkerKey is written into both new stores by the
// activation block. The IAVL version here can't load an empty tree at a
// later version: an added store that stays empty makes every restart
// fail ("failed to load store: version does not exist") and every query
// at the latest height fail the same way. Keys prefixed 0xff are used by
// neither module (authz: 0x01/0x02, feegrant: 0x00/0x01), so no
// iteration or genesis export ever sees it.
var authzFeegrantMarkerKey = []byte("\xffaether/activated")

// checkAuthzFeegrantActivation halts a node that reaches the activation
// height without the stores mounted. The block is not committed; on
// restart, planAuthzFeegrant sees activation-1 as the last committed
// height and adds the stores. The activation block itself then writes
// the marker that keeps them non-empty.
func (app *App) checkAuthzFeegrantActivation(ctx sdk.Context) error {
	if app.authzFeegrantWired {
		if ctx.BlockHeight() == authzFeegrantActivationHeight {
			for _, name := range authzFeegrantStoreKeys {
				ctx.KVStore(app.keys[name]).Set(authzFeegrantMarkerKey, []byte{1})
			}
		}
		return nil
	}
	if ctx.BlockHeight() < authzFeegrantActivationHeight {
		return nil
	}
	return fmt.Errorf(
		"x/authz and x/feegrant activate at height %d: restart this node (e.g. `systemctl restart aetherd`) to add their stores, then it will resume from this block",
		authzFeegrantActivationHeight,
	)
}
