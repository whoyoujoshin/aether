package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"encoding/json"
	"strings"

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
	"github.com/cosmos/cosmos-sdk/x/auth/tx"
	sdk "github.com/cosmos/cosmos-sdk/types"
	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/whoyoujoshin/aether/x/governance"
	"github.com/whoyoujoshin/aether/x/pow"
	"github.com/whoyoujoshin/aether/x/treasury"
	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/runtime"
	authkeeper "github.com/cosmos/cosmos-sdk/x/auth/keeper"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/cosmos-sdk/x/auth"
	authante "github.com/cosmos/cosmos-sdk/x/auth/ante"
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/cosmos/cosmos-sdk/x/bank"
	signing "cosmossdk.io/x/tx/signing"
	"github.com/cosmos/gogoproto/proto"

)

const Name = "aether"

var DefaultNodeHome string

func init() {
	home, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}
	DefaultNodeHome = filepath.Join(home, ".aether")
}

var ModuleBasics = module.NewBasicManager(
	auth.AppModuleBasic{},
	bank.AppModuleBasic{},
	pow.AppModuleBasic{},
	treasury.AppModuleBasic{},
	governance.AppModuleBasic{},
)

type EncodingConfig struct {
	InterfaceRegistry cdctypes.InterfaceRegistry
	Codec             codec.Codec
	TxConfig          client.TxConfig
	Amino             *codec.LegacyAmino
}

func MakeEncodingConfig() EncodingConfig {
	interfaceRegistry, err := cdctypes.NewInterfaceRegistryWithOptions(cdctypes.InterfaceRegistryOptions{
	ProtoFiles: proto.HybridResolver,
	SigningOptions: signing.Options{
		AddressCodec:          address.NewBech32Codec(sdk.Bech32MainPrefix),
		ValidatorAddressCodec: address.NewBech32Codec(sdk.Bech32PrefixValAddr),
	},
})
if err != nil {
	panic(err)
}
	std.RegisterInterfaces(interfaceRegistry)
	ModuleBasics.RegisterInterfaces(interfaceRegistry)

	appCodec := codec.NewProtoCodec(interfaceRegistry)
	legacyAmino := codec.NewLegacyAmino()
	std.RegisterLegacyAminoCodec(legacyAmino)
	ModuleBasics.RegisterLegacyAminoCodec(legacyAmino)

	txCfg, err := tx.NewTxConfigWithOptions(appCodec, tx.ConfigOptions{
	EnabledSignModes: tx.DefaultSignModes,
	SigningOptions: &signing.Options{
		AddressCodec:          address.NewBech32Codec(sdk.Bech32MainPrefix),
		ValidatorAddressCodec: address.NewBech32Codec(sdk.Bech32PrefixValAddr),
	},
})
if err != nil {
	panic(err)
}

	return EncodingConfig{
		InterfaceRegistry: interfaceRegistry,
		Codec:             appCodec,
		TxConfig:          txCfg,
		Amino:             legacyAmino,
	}
}

type consensusParamsStore struct {
	storeKey storetypes.StoreKey
}

func (s consensusParamsStore) Get(ctx context.Context) (tmproto.ConsensusParams, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	bz := sdkCtx.KVStore(s.storeKey).Get([]byte("consensus_params"))
	if bz == nil {
		return tmproto.ConsensusParams{}, nil
	}
	var params tmproto.ConsensusParams
	if err := params.Unmarshal(bz); err != nil {
		return tmproto.ConsensusParams{}, err
	}
	return params, nil
}

func (s consensusParamsStore) Set(ctx context.Context, params tmproto.ConsensusParams) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	bz, err := params.Marshal()
	if err != nil {
		return err
	}
	sdkCtx.KVStore(s.storeKey).Set([]byte("consensus_params"), bz)
	return nil
}

func (s consensusParamsStore) Has(ctx context.Context) (bool, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	return sdkCtx.KVStore(s.storeKey).Has([]byte("consensus_params")), nil
}

type App struct {
	*baseapp.BaseApp
	cdc               codec.Codec
	interfaceRegistry cdctypes.InterfaceRegistry
	keys              map[string]*storetypes.KVStoreKey

	AccountKeeper    authkeeper.AccountKeeper
	BankKeeper       bankkeeper.BaseKeeper
	PowKeeper        pow.Keeper
	TreasuryKeeper   treasury.Keeper
	GovernanceKeeper governance.Keeper

	sm *module.Manager
	BasicModuleManager   module.BasicManager
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

	interfaceRegistry, err := cdctypes.NewInterfaceRegistryWithOptions(cdctypes.InterfaceRegistryOptions{
	ProtoFiles: proto.HybridResolver,
	SigningOptions: signing.Options{
		AddressCodec:          address.NewBech32Codec(sdk.Bech32MainPrefix),
		ValidatorAddressCodec: address.NewBech32Codec(sdk.Bech32PrefixValAddr),
	},
})
if err != nil {
	panic(err)
}
	std.RegisterInterfaces(interfaceRegistry)
	appCodec := codec.NewProtoCodec(interfaceRegistry)

	bApp := baseapp.NewBaseApp(Name, logger, db, nil, baseAppOptions...)
	bApp.SetVersion("0.1")

	app := &App{
		BaseApp:           bApp,
		cdc:               appCodec,
		interfaceRegistry: interfaceRegistry,
		keys: map[string]*storetypes.KVStoreKey{
			authtypes.StoreKey:  storetypes.NewKVStoreKey(authtypes.StoreKey),
			banktypes.StoreKey:  storetypes.NewKVStoreKey(banktypes.StoreKey),
			pow.StoreKey:        storetypes.NewKVStoreKey(pow.StoreKey),
			treasury.StoreKey:   storetypes.NewKVStoreKey(treasury.StoreKey),
			governance.StoreKey: storetypes.NewKVStoreKey(governance.StoreKey),
			"consensus":         storetypes.NewKVStoreKey("consensus"),
		},
	}
	app.SetInterfaceRegistry(app.interfaceRegistry)
	
	app.MountKVStores(app.keys)
	app.SetParamStore(consensusParamsStore{storeKey: app.keys["consensus"]})
	
	maccPerms := map[string][]string{
	authtypes.FeeCollectorName: nil,
	pow.ModuleName:             {authtypes.Minter},
}

app.AccountKeeper = authkeeper.NewAccountKeeper(
	appCodec,
	runtime.NewKVStoreService(app.keys[authtypes.StoreKey]),
	authtypes.ProtoBaseAccount,
	maccPerms,
	address.NewBech32Codec(sdk.Bech32MainPrefix),
	sdk.Bech32MainPrefix,
	authtypes.NewModuleAddress("gov").String(),
)

app.BankKeeper = bankkeeper.NewBaseKeeper(
	appCodec,
	runtime.NewKVStoreService(app.keys[banktypes.StoreKey]),
	app.AccountKeeper,
	nil,
	authtypes.NewModuleAddress("gov").String(),
	logger,
)
	// Initialize keepers
	app.PowKeeper = pow.NewKeeper(appCodec, app.keys[pow.StoreKey], logger, app.BankKeeper)
	app.TreasuryKeeper = treasury.NewKeeper(appCodec, app.keys[treasury.StoreKey])
	app.GovernanceKeeper = governance.NewKeeper(appCodec, app.keys[governance.StoreKey])

	// Module manager
	powModule := pow.NewAppModule(appCodec, app.PowKeeper)

	app.sm = module.NewManager(
	auth.NewAppModule(appCodec, app.AccountKeeper, nil, nil),
	bank.NewAppModule(appCodec, app.BankKeeper, app.AccountKeeper, nil),
	powModule,
	treasury.NewAppModule(appCodec, app.TreasuryKeeper),
	governance.NewAppModule(appCodec, app.GovernanceKeeper),
)
	app.BasicModuleManager = module.NewBasicManagerFromManager(app.sm, nil)
	app.BasicModuleManager.RegisterInterfaces(app.interfaceRegistry)

	configurator := module.NewConfigurator(appCodec, app.MsgServiceRouter(), app.GRPCQueryRouter())
	app.sm.RegisterServices(configurator)

	txConfig, err := tx.NewTxConfigWithOptions(appCodec, tx.ConfigOptions{
	EnabledSignModes: tx.DefaultSignModes,
	SigningOptions: &signing.Options{
		AddressCodec:          address.NewBech32Codec(sdk.Bech32MainPrefix),
		ValidatorAddressCodec: address.NewBech32Codec(sdk.Bech32PrefixValAddr),
	},
})
if err != nil {
	panic(err)
}
	bApp.SetTxDecoder(txConfig.TxDecoder())

	// Standard ante handler
	stdAnteHandler, err := authante.NewAnteHandler(authante.HandlerOptions{
		AccountKeeper:   app.AccountKeeper,
		BankKeeper:      app.BankKeeper,
		SignModeHandler: txConfig.SignModeHandler(),
		FeegrantKeeper:  nil,
		SigGasConsumer:  authante.DefaultSigVerificationGasConsumer,
	})
	if err != nil {
		panic(err)
	}

	// Wrap with our PostQuantumDecorator (outermost)
	// The decorator calls next (the standard handler) after its own logic.
	pqDecorator := NewPostQuantumDecorator()
	anteHandler := func(ctx sdk.Context, tx sdk.Tx, simulate bool) (newCtx sdk.Context, err error) {
		return pqDecorator.AnteHandle(ctx, tx, simulate, stdAnteHandler)
	}

	app.SetAnteHandler(anteHandler)
	app.SetInitChainer(app.InitChainer)
	app.SetBeginBlocker(app.BeginBlocker)
	app.SetEndBlocker(app.EndBlocker)
	if loadLatest {
		if err := app.LoadLatestVersion(); err != nil {
			panic(fmt.Errorf("error loading last version: %w", err))
		}
	}

	return app
}

func (app *App) InitChainer(ctx sdk.Context, req *abci.RequestInitChain) (*abci.ResponseInitChain, error) {
	fmt.Printf(">>> InitChainer called! Validators: %d\n", len(req.Validators))
	var genesisState map[string]json.RawMessage
	if err := json.Unmarshal(req.AppStateBytes, &genesisState); err != nil {
		return nil, err
	}

	if _, err := app.sm.InitGenesis(ctx, app.cdc, genesisState); err != nil {
		// This chain has no x/staking module — validators come directly
		// from genesis.json's consensus.validators (req.Validators), not
		// from module-aggregated ValidatorUpdates. The module manager
		// treats an empty aggregated validator set as fatal by default;
		// that check doesn't apply to our design, so we tolerate exactly
		// this one known, expected error and let any other error through.
		if !strings.Contains(err.Error(), "validator set is empty after InitGenesis") {
			return nil, err
		}
	}

	if req.ConsensusParams != nil {
		if err := app.StoreConsensusParams(ctx, *req.ConsensusParams); err != nil {
			return nil, err
		}
	}

	return &abci.ResponseInitChain{
		ConsensusParams: req.ConsensusParams,
		Validators:      req.Validators,
		AppHash:         app.LastCommitID().Hash,
	}, nil
}
func (app *App) BeginBlocker(ctx sdk.Context) (sdk.BeginBlock, error) {
	app.PowKeeper.Heartbeat(ctx)
	app.TreasuryKeeper.Heartbeat(ctx)
	app.GovernanceKeeper.Heartbeat(ctx)
	return app.sm.BeginBlock(ctx)
}

func (app *App) EndBlocker(ctx sdk.Context) (sdk.EndBlock, error) {
	return app.sm.EndBlock(ctx)
}
// Required methods
func (app *App) RegisterAPIRoutes(apiSvr *api.Server, apiConfig config.APIConfig) {}
func (app *App) RegisterGRPCServerWithSkipCheckHeader(grpcSrv grpc.Server, skip bool) {}
func (app *App) RegisterTxService(clientCtx client.Context) {}
func (app *App) RegisterTendermintService(clientCtx client.Context) {}
func (app *App) RegisterNodeService(clientCtx client.Context, cfg config.Config) {}
func (app *App) GetModuleManager() *module.Manager { return app.sm }
