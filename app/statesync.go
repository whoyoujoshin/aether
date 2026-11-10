package app

import (
	"math"

	dbm "github.com/cosmos/cosmos-db"
	"github.com/spf13/cast"

	"cosmossdk.io/store/rootmulti"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
)

// storePlanHeight is the height New() decides its height-gated stores from
// (authz/feegrant, IBC, accountauth, escrow, inflowcap): the last height
// saved on disk, as an ordinary node replaying or following the chain needs.
//
// A node with nothing saved that is set to state-sync (config.toml
// [statesync] enable = true) is different: it restores a snapshot taken at a
// recent height, which carries every store active there, and the store
// restore refuses an item for a store the app didn't mount ("cannot import
// into non-IAVL store"). So such a node plans as if it were past every
// activation and mounts them all. Found on the testnet on 2026-11-09: a
// fresh Sydney node rejected sync3's snapshot at 684,600 on "accountauth".
//
// The snapshot must then be from after the last height that adds a store
// (511,920 on the testnet, block 1 on fresh chains); an older one would
// leave a mounted store empty. Every snapshot a testnet node takes now is.
func storePlanHeight(db dbm.DB, appOpts servertypes.AppOptions) int64 {
	last := rootmulti.GetLatestVersion(db)
	if last == 0 && stateSyncEnabled(appOpts) {
		return math.MaxInt64
	}
	return last
}

// stateSyncEnabled reads config.toml's [statesync] enable, which the SDK's
// server puts in the same options as app.toml.
func stateSyncEnabled(appOpts servertypes.AppOptions) bool {
	if appOpts == nil {
		return false
	}
	return cast.ToBool(appOpts.Get("statesync.enable"))
}
