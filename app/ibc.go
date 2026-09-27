package app

import (
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	capability "github.com/cosmos/ibc-go/modules/capability"
	capabilitytypes "github.com/cosmos/ibc-go/modules/capability/types"
	ica "github.com/cosmos/ibc-go/v8/modules/apps/27-interchain-accounts"
	icacontrollertypes "github.com/cosmos/ibc-go/v8/modules/apps/27-interchain-accounts/controller/types"
	icahosttypes "github.com/cosmos/ibc-go/v8/modules/apps/27-interchain-accounts/host/types"
	icatypes "github.com/cosmos/ibc-go/v8/modules/apps/27-interchain-accounts/types"
	transfer "github.com/cosmos/ibc-go/v8/modules/apps/transfer"
	ibctransfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	ibc "github.com/cosmos/ibc-go/v8/modules/core"
	ibcexported "github.com/cosmos/ibc-go/v8/modules/core/exported"
)

// IBCActivationHeight is the first block that executes with IBC (core,
// ICS-20 transfer, ICS-27 interchain accounts) live.
//
// PLACEHOLDER, same as AuthzFeegrantActivationHeight's original
// "100,000 placeholder" -- this value is not yet coordinated with the
// operator and MUST be replaced with a real, agreed height (based on
// the live chain tip at the time) before this activates anywhere. Do
// not let this height arrive on a live node before that coordination
// happens: like every gate in this file, an un-restarted node just
// halts at it (see checkIBCActivation), so the practical failure mode
// is a halted node, not silent data corruption -- but it's still an
// avoidable operational surprise.
//
// Follows the exact same mechanism as AuthzFeegrantActivationHeight
// (see app/authz_feegrant.go's own extensive doc comment for the full
// rationale: new KV stores can't just be mounted on a live multistore,
// and every mounted store -- even an empty one -- is a leaf in the
// AppHash, so activation must be height-gated and restart-driven, not a
// simple in-handler check). All of IBC's stores -- capability, core,
// transfer, and both interchain-accounts submodules -- are reserved and
// activated together at this one height, even though the transfer and
// interchain-accounts keepers themselves may land in separate commits
// first: bundling every new IBC-related store into one coordinated
// cutover avoids asking every operator to restart their node twice.
const IBCActivationHeight int64 = 200_000

// ibcActivationHeight is what New() actually reads, so tests can
// exercise the store-upgrade path at a small height instead of
// committing 200k blocks.
var ibcActivationHeight = IBCActivationHeight

// ibcStoreKeys are the persistent (IAVL, committed, AppHash-visible)
// stores IBC needs. transfer/icacontroller/icahost are reserved now
// (see IBCActivationHeight's doc comment) even though their keepers are
// wired in later commits -- an unused, empty IAVL store is harmless.
var ibcStoreKeys = []string{
	capabilitytypes.StoreKey,
	ibcexported.StoreKey,
	ibctransfertypes.StoreKey,
	icacontrollertypes.StoreKey,
	icahosttypes.StoreKey,
	ibcSelfConsensusStoreKey,
}

// ibcMemoryStoreKeys are transient (never committed, no AppHash effect,
// rebuilt fresh every process start) stores capability needs for its
// runtime capability-owner index. Unlike ibcStoreKeys these carry no
// history to preserve, so they don't need height-gating or
// StoreUpgrades -- they can always be mounted, and simply go unused
// until IBC is wired.
var ibcMemoryStoreKeys = []string{capabilitytypes.MemStoreKey}

type ibcPlan struct {
	// wire: mount the persistent stores and register keepers, modules
	// and message types for this run.
	wire bool
	// addStores: this is the one startup that creates the persistent
	// stores.
	addStores bool
}

// planIBC decides wiring from the last committed height. See
// planAuthzFeegrant, which this exactly mirrors.
func planIBC(lastCommittedHeight, activationHeight int64) ibcPlan {
	if lastCommittedHeight < activationHeight-1 {
		return ibcPlan{}
	}
	return ibcPlan{wire: true, addStores: lastCommittedHeight == activationHeight-1}
}

// ibcActivationMarkerKey is written into every new persistent store by
// the activation block, for the same reason
// authzFeegrantMarkerKey is: an IAVL store that stays empty at a later
// version fails to load on the next restart ("version does not exist"),
// and fails every query at the latest height the same way. Keys
// prefixed 0xff are used by neither capability (0x00-0x03), IBC core,
// transfer, nor ICA, so no iteration or genesis export ever sees it.
var ibcActivationMarkerKey = []byte("\xffaether/ibc-activated")

// checkIBCActivation halts a node that reaches the activation height
// without the stores mounted, exactly like checkAuthzFeegrantActivation
// (see that function's doc comment for the restart mechanics). Also
// records this block's header into the self-consensus store once IBC is
// wired, so it's available from the very first block IBC could need it.
func (app *App) checkIBCActivation(ctx sdk.Context) error {
	if app.ibcWired {
		if ctx.BlockHeight() == ibcActivationHeight {
			for _, name := range ibcStoreKeys {
				ctx.KVStore(app.keys[name]).Set(ibcActivationMarkerKey, []byte{1})
			}
			if err := app.initIBCGenesisAtActivation(ctx); err != nil {
				return err
			}
		}
		app.trackSelfConsensusInfo(ctx)
		return nil
	}
	if ctx.BlockHeight() < ibcActivationHeight {
		return nil
	}
	return fmt.Errorf(
		"IBC (capability, core, transfer, interchain accounts) activates at height %d: restart this node (e.g. `systemctl restart aetherd`) to add its stores, then it will resume from this block",
		ibcActivationHeight,
	)
}

// initIBCGenesisAtActivation runs InitGenesis for every newly-mounted
// IBC module at the activation block, using each one's own default
// genesis -- the same DefaultGenesis() an operator's `aetherd init`
// would have produced had this chain launched with IBC live from block
// 1. The height-gated activation pattern (see IBCActivationHeight) only
// arranges for the STORES to exist at this height; unlike x/authz and
// x/feegrant, whose default state is trivially empty, IBC's own
// 02-client and transfer submodules panic on first read if their Params
// were never written (GetParams: "client params are not set in
// store"), so this step is load-bearing, not defensive.
func (app *App) initIBCGenesisAtActivation(ctx sdk.Context) error {
	mgr := module.NewManager(
		capability.NewAppModule(app.cdc, *app.CapabilityKeeper, false),
		ibc.NewAppModule(app.IBCKeeper),
		transfer.NewAppModule(app.TransferKeeper),
		ica.NewAppModule(&app.ICAControllerKeeper, &app.ICAHostKeeper),
	)
	genesisState := map[string]json.RawMessage{
		capabilitytypes.ModuleName:  capability.AppModuleBasic{}.DefaultGenesis(app.cdc),
		ibcexported.ModuleName:      ibc.AppModuleBasic{}.DefaultGenesis(app.cdc),
		ibctransfertypes.ModuleName: transfer.AppModuleBasic{}.DefaultGenesis(app.cdc),
		icatypes.ModuleName:         ica.AppModuleBasic{}.DefaultGenesis(app.cdc),
	}
	if _, err := mgr.InitGenesis(ctx, app.cdc, genesisState); err != nil {
		// Mirrors InitChainer's own tolerance (see app.go): this chain
		// has no x/staking-derived validator set, so the module
		// manager's blanket "a chain must initialize with a non-empty
		// validator set" check doesn't apply here either -- none of
		// these three modules ever contributes validator updates.
		if !strings.Contains(err.Error(), "validator set is empty after InitGenesis") {
			return err
		}
	}
	return nil
}
