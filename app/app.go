package app

import (
	"context"
	"io"

	dbm "github.com/cosmos/cosmos-db"
	"cosmossdk.io/log"
	tmproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cosmos/gogoproto/grpc"
	storetypes "cosmossdk.io/store/types"

	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/server/api"
	"github.com/cosmos/cosmos-sdk/server/config"
	"github.com/cosmos/cosmos-sdk/server/types"
	"github.com/cosmos/cosmos-sdk/std"
	"github.com/cosmos/cosmos-sdk/types/module"

	"github.com/whoyoujoshin/aether/x/pow"
	"github.com/whoyoujoshin/aether/x/treasury"
	"github.com/whoyoujoshin/aether/x/governance"
)

const (
	Name            = "aether"
	DefaultNodeHome = ".aether"
)

var ModuleBasics = module.NewBasicManager(
	pow.AppModuleBasic{},
	treasury.AppModuleBasic{},
	governance.AppModuleBasic{},
)

// Minimal param store to satisfy baseapp.ParamStore in v0.50
type paramStore struct {
	cdc codec.BinaryCodec
}

func (p paramStore) Get(ctx context.Context) (tmproto.ConsensusParams, error) {
	return tmproto.ConsensusParams{}, nil
}

func (p paramStore) Set(ctx context.Context, params tmproto.ConsensusParams) error {
	return nil
}

func (p paramStore) Has(ctx context.Context) (bool, error) { return false, nil }
func (p paramStore) GetIfExists(ctx context.Context, key []byte, ptr interface{}) {}
func (p paramStore) Modify(ctx context.Context, f func(*tmproto.ConsensusParams)) {}

type App struct {
	*baseapp.BaseApp

	cdc               codec.Codec
	interfaceRegistry cdctypes.InterfaceRegistry

	keys map[string]*storetypes.KVStoreKey

	PowKeeper        pow.Keeper
	TreasuryKeeper   treasury.Keeper
	GovernanceKeeper governance.Keeper

	sm *module.Manager
}

func New(
	logger log.Logger,
	db dbm.DB,
	traceStore io.Writer,
	loadLatest bool,
	skipUpgradeHeights map[int64]bool,
	homePath string,
	invCheckPeriod uint,
	appOpts types.AppOptions,
	baseAppOptions ...func(*baseapp.BaseApp),
) types.Application {

	interfaceRegistry := cdctypes.NewInterfaceRegistry()
	std.RegisterInterfaces(interfaceRegistry)
	appCodec := codec.NewProtoCodec(interfaceRegistry)

	bApp := baseapp.NewBaseApp(Name, logger, db, nil, baseAppOptions...)
	bApp.SetVersion("0.1")

	app := &App{
		BaseApp:           bApp,
		cdc:               appCodec,
		interfaceRegistry: interfaceRegistry,
		keys: map[string]*storetypes.KVStoreKey{
			pow.StoreKey:        storetypes.NewKVStoreKey(pow.StoreKey),
			treasury.StoreKey:   storetypes.NewKVStoreKey(treasury.StoreKey),
			governance.StoreKey: storetypes.NewKVStoreKey(governance.StoreKey),
			"consensus":         storetypes.NewKVStoreKey("consensus"),
		},
	}

	app.MountKVStores(app.keys)
	app.SetParamStore(paramStore{cdc: appCodec})

	app.PowKeeper = pow.NewKeeper(appCodec, app.keys[pow.StoreKey])
	app.TreasuryKeeper = treasury.NewKeeper(appCodec, app.keys[treasury.StoreKey])
	app.GovernanceKeeper = governance.NewKeeper(appCodec, app.keys[governance.StoreKey])

	app.sm = module.NewManager(
		pow.NewAppModule(appCodec, app.PowKeeper),
		treasury.NewAppModule(appCodec, app.TreasuryKeeper),
		governance.NewAppModule(appCodec, app.GovernanceKeeper),
	)

	app.sm.SetOrderBeginBlockers()
	app.sm.SetOrderEndBlockers()

	return app
}

// Required methods to satisfy servertypes.Application
func (app *App) RegisterAPIRoutes(apiSvr *api.Server, apiConfig config.APIConfig) {}

func (app *App) RegisterGRPCServerWithSkipCheckHeader(grpcSrv grpc.Server, skip bool) {}

func (app *App) RegisterTxService(clientCtx client.Context) {}

func (app *App) RegisterTendermintService(clientCtx client.Context) {}

func (app *App) RegisterNodeService(clientCtx client.Context, cfg config.Config) {}

func (app *App) GetModuleManager() *module.Manager {
	return app.sm
}