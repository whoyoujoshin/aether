package app

import (
	"fmt"
	"io"
	"os"

	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/codec"
	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/std"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/cosmos-sdk/x/auth"
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/x/bank"

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
		auth.AppModuleBasic{},
		bank.AppModuleBasic{},
		pow.AppModuleBasic{},
		treasury.AppModuleBasic{},
		governance.AppModuleBasic{},
	)

	mModuleAccountAddrs = map[string]bool{
		authtypes.FeeCollectorName: true,
	}
)

type App struct {
	*baseapp.BaseApp

	cdc               codec.Codec
	interfaceRegistry cdctypes.InterfaceRegistry

	keys  map[string]*storetypes.KVStoreKey
	tkeys map[string]*storetypes.TransientStoreKey

	// Keepers
	AccountKeeper auth.AccountKeeper
	BankKeeper    bankkeeper.Keeper
	PowKeeper     pow.Keeper
	TreasuryKeeper treasury.Keeper
	GovernanceKeeper governance.Keeper

	sm *module.Manager
}

func New(logger sdk.Logger, db interface{}, traceStore io.Writer, loadLatest bool, skipUpgradeHeights map[int64]bool, homePath string, invCheckPeriod uint, baseAppOptions ...func(*baseapp.BaseApp),
) *App {
	// Create codec
	appCodec := MakeCodec()
	interfaceRegistry := cdctypes.NewInterfaceRegistry()
	std.RegisterInterfaces(interfaceRegistry)

	// Create BaseApp
	bApp := baseapp.NewBaseApp(Name, logger, db, nil, baseAppOptions...)
	// Set BaseApp version
	bApp.SetVersion("0.1")

	// Create App instance
	app := &App{
		BaseApp:           bApp,
		cdc:               appCodec,
		interfaceRegistry: interfaceRegistry,
		keys: map[string]*storetypes.KVStoreKey{
			authtypes.StoreKey:   storetypes.NewKVStoreKey(authtypes.StoreKey),
			bank.StoreKey:        storetypes.NewKVStoreKey(bank.StoreKey),
			pow.StoreKey:         storetypes.NewKVStoreKey(pow.StoreKey),
			treasury.StoreKey:    storetypes.NewKVStoreKey(treasury.StoreKey),
			governance.StoreKey:  storetypes.NewKVStoreKey(governance.StoreKey),
		},
		tkeys: map[string]*storetypes.TransientStoreKey{},
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
