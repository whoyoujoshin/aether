package app

import (
	"fmt"
	"io"

	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/codec"
	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/std"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	"cosmossdk.io/store/types"
	"cosmossdk.io/log"
	cosmosdb "github.com/cosmos/cosmos-db"

	"github.com/whoyoujoshin/aether/x/pow"
	"github.com/whoyoujoshin/aether/x/treasury"
	"github.com/whoyoujoshin/aether/x/governance"
)

const (
	Name            = "aether"
	DefaultNodeHome = ".aether"
)

var (
	ModuleBasics = module.NewBasicManager(
		pow.AppModuleBasic{},
		treasury.AppModuleBasic{},
		governance.AppModuleBasic{},
	)
)

type App struct {
	*baseapp.BaseApp

	cdc               codec.Codec
	interfaceRegistry cdctypes.InterfaceRegistry

	keys  map[string]*types.KVStoreKey
	tkeys map[string]*types.TransientStoreKey

	// Keepers
	PowKeeper        pow.Keeper
	TreasuryKeeper   treasury.Keeper
	GovernanceKeeper governance.Keeper

	sm *module.Manager
}

func New(logger log.Logger, db cosmosdb.DB, traceStore io.Writer, loadLatest bool, skipUpgradeHeights map[int64]bool, homePath string, invCheckPeriod uint, baseAppOptions ...func(*baseapp.BaseApp),
) *App {
	fmt.Println("✅ Initializing Aether App...")

	// Create codec
	appCodec := MakeCodec()
	interfaceRegistry := cdctypes.NewInterfaceRegistry()
	std.RegisterInterfaces(interfaceRegistry)

	// Create BaseApp
	bApp := baseapp.NewBaseApp(Name, logger, db, nil, baseAppOptions...)
	bApp.SetVersion("0.1")

	// Create App instance
	app := &App{
		BaseApp:           bApp,
		cdc:               appCodec,
		interfaceRegistry: interfaceRegistry,
		keys: map[string]*types.KVStoreKey{
			pow.StoreKey:        types.NewKVStoreKey(pow.StoreKey),
			treasury.StoreKey:   types.NewKVStoreKey(treasury.StoreKey),
			governance.StoreKey: types.NewKVStoreKey(governance.StoreKey),
		},
		tkeys: map[string]*types.TransientStoreKey{},
	}

	// Initialize Keepers
	app.PowKeeper = pow.NewKeeper(appCodec, app.keys[pow.StoreKey])
	app.TreasuryKeeper = treasury.NewKeeper(appCodec, app.keys[treasury.StoreKey])
	app.GovernanceKeeper = governance.NewKeeper(appCodec, app.keys[governance.StoreKey])

	// Set Module Manager
	app.sm = module.NewManager(
		pow.NewAppModule(appCodec, app.PowKeeper),
		treasury.NewAppModule(appCodec, app.TreasuryKeeper),
		governance.NewAppModule(appCodec, app.GovernanceKeeper),
	)

	app.sm.SetOrderBeginBlockers()
	app.sm.SetOrderEndBlockers()

	fmt.Println("✅ Aether App initialized successfully")
	return app
}

func MakeCodec() codec.Codec {
	reg := cdctypes.NewInterfaceRegistry()
	cdc := codec.NewProtoCodec(reg)
	return cdc
}

func (app *App) GetModuleManager() *module.Manager {
	return app.sm
}
