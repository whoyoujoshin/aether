// Package counterparty is a small, entirely standard Cosmos SDK chain
// -- real x/staking, standard secp256k1 signing, no ML-DSA requirement,
// no PostQuantumDecorator -- whose only purpose is to be a genuinely
// separate, independently-running IBC counterparty for testing
// Aether's IBC wiring against a real second process instead of the
// in-process ibctesting harness app/ibc_handshake_test.go already
// covers. See docs/IBC.md's "Testing with a real relayer" section.
package counterparty

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"cosmossdk.io/log"
	storetypes "cosmossdk.io/store/types"
	signing "cosmossdk.io/x/tx/signing"
	upgrademodule "cosmossdk.io/x/upgrade"
	upgradekeeper "cosmossdk.io/x/upgrade/keeper"
	upgradetypes "cosmossdk.io/x/upgrade/types"
	abci "github.com/cometbft/cometbft/abci/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/grpc/cmtservice"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/codec/legacy"
	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/server/api"
	"github.com/cosmos/cosmos-sdk/server/config"
	"github.com/cosmos/cosmos-sdk/server/types"
	"github.com/cosmos/cosmos-sdk/std"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	signingtypes "github.com/cosmos/cosmos-sdk/types/tx/signing"
	"github.com/cosmos/cosmos-sdk/x/auth"
	authante "github.com/cosmos/cosmos-sdk/x/auth/ante"
	authkeeper "github.com/cosmos/cosmos-sdk/x/auth/keeper"
	authtx "github.com/cosmos/cosmos-sdk/x/auth/tx"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/cosmos-sdk/x/bank"
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/cosmos/cosmos-sdk/x/consensus"
	consensuskeeper "github.com/cosmos/cosmos-sdk/x/consensus/keeper"
	"github.com/cosmos/cosmos-sdk/x/genutil"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"
	"github.com/cosmos/cosmos-sdk/x/staking"
	stakingkeeper "github.com/cosmos/cosmos-sdk/x/staking/keeper"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	gogoproto "github.com/cosmos/gogoproto/proto"

	capability "github.com/cosmos/ibc-go/modules/capability"
	capabilitykeeper "github.com/cosmos/ibc-go/modules/capability/keeper"
	capabilitytypes "github.com/cosmos/ibc-go/modules/capability/types"
	transfer "github.com/cosmos/ibc-go/v8/modules/apps/transfer"
	ibctransferkeeper "github.com/cosmos/ibc-go/v8/modules/apps/transfer/keeper"
	ibctransfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	ibc "github.com/cosmos/ibc-go/v8/modules/core"
	ibcexported "github.com/cosmos/ibc-go/v8/modules/core/exported"
	ibckeeper "github.com/cosmos/ibc-go/v8/modules/core/keeper"
	ibctm "github.com/cosmos/ibc-go/v8/modules/light-clients/07-tendermint"

	"github.com/whoyoujoshin/aether/crypto/ethsecp256k1"
)

const (
	Name         = "counterparty"
	Bech32Prefix = "cparty"
)

var DefaultNodeHome string

func init() {
	home, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}
	DefaultNodeHome = filepath.Join(home, ".counterparty")
}

// x/auth's gas estimation amino-encodes signers' pubkeys with the SDK's
// global codec, so eth_secp256k1 keys must be registered there too.
func init() {
	ethsecp256k1.RegisterLegacyAminoCodec(legacy.Cdc)
}

func SetAddressPrefixes() {
	config := sdk.GetConfig()
	config.SetBech32PrefixForAccount(Bech32Prefix, Bech32Prefix+"pub")
	config.SetBech32PrefixForValidator(Bech32Prefix+"valoper", Bech32Prefix+"valoperpub")
	config.SetBech32PrefixForConsensusNode(Bech32Prefix+"valcons", Bech32Prefix+"valconspub")
	config.Seal()
}

var ModuleBasics = module.NewBasicManager(
	auth.AppModuleBasic{},
	bank.AppModuleBasic{},
	staking.AppModuleBasic{},
	consensus.AppModuleBasic{},
	genutil.NewAppModuleBasic(genutiltypes.DefaultMessageValidator),
	upgrademodule.AppModuleBasic{},
	capability.AppModuleBasic{},
	ibc.AppModuleBasic{},
	ibctm.AppModuleBasic{},
	transfer.AppModuleBasic{},
)

type EncodingConfig struct {
	InterfaceRegistry cdctypes.InterfaceRegistry
	Codec             codec.Codec
	TxConfig          client.TxConfig
	Amino             *codec.LegacyAmino
}

// MakeEncodingConfig builds an encoding config for a standard Cosmos
// SDK + ibc-go chain (auth, bank, staking, capability, ibc, transfer --
// see ModuleBasics), parameterized by bech32Prefix so the same, entirely
// generic config can address either this package's own local test
// chain (pass Bech32Prefix) or a real external standard Cosmos SDK
// chain such as Osmosis (pass its prefix, e.g. "osmo") -- nothing in
// here besides the address/validator-address codecs is specific to
// this package's toy chain.
func MakeEncodingConfig(bech32Prefix string) EncodingConfig {
	interfaceRegistry, err := cdctypes.NewInterfaceRegistryWithOptions(cdctypes.InterfaceRegistryOptions{
		ProtoFiles: gogoproto.HybridResolver,
		SigningOptions: signing.Options{
			AddressCodec:          address.NewBech32Codec(bech32Prefix),
			ValidatorAddressCodec: address.NewBech32Codec(bech32Prefix + "valoper"),
		},
	})
	if err != nil {
		panic(err)
	}
	std.RegisterInterfaces(interfaceRegistry)
	// Accounts may also use Injective's eth_secp256k1 keys, so a local
	// devnet can stand in for Injective when testing the relayers'
	// signing for it (see crypto/ethsecp256k1).
	ethsecp256k1.RegisterInterfaces(interfaceRegistry)
	ModuleBasics.RegisterInterfaces(interfaceRegistry)

	appCodec := codec.NewProtoCodec(interfaceRegistry)
	legacyAmino := codec.NewLegacyAmino()
	std.RegisterLegacyAminoCodec(legacyAmino)
	ethsecp256k1.RegisterLegacyAminoCodec(legacyAmino)
	ModuleBasics.RegisterLegacyAminoCodec(legacyAmino)

	txCfg, err := authtx.NewTxConfigWithOptions(appCodec, authtx.ConfigOptions{
		EnabledSignModes: authtx.DefaultSignModes,
		SigningOptions: &signing.Options{
			AddressCodec:          address.NewBech32Codec(bech32Prefix),
			ValidatorAddressCodec: address.NewBech32Codec(bech32Prefix + "valoper"),
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

type App struct {
	*baseapp.BaseApp
	cdc               codec.Codec
	interfaceRegistry cdctypes.InterfaceRegistry
	keys              map[string]*storetypes.KVStoreKey
	memKeys           map[string]*storetypes.MemoryStoreKey
	txConfig          client.TxConfig

	AccountKeeper   authkeeper.AccountKeeper
	BankKeeper      bankkeeper.BaseKeeper
	StakingKeeper   *stakingkeeper.Keeper
	ConsensusKeeper consensuskeeper.Keeper
	UpgradeKeeper   *upgradekeeper.Keeper

	CapabilityKeeper     *capabilitykeeper.Keeper
	IBCKeeper            *ibckeeper.Keeper
	TransferKeeper       ibctransferkeeper.Keeper
	ScopedIBCKeeper      capabilitykeeper.ScopedKeeper
	ScopedTransferKeeper capabilitykeeper.ScopedKeeper

	mm                 *module.Manager
	BasicModuleManager module.BasicManager
}

func New(
	logger log.Logger,
	db dbm.DB,
	traceStore io.Writer,
	loadLatest bool,
	appOpts types.AppOptions,
	baseAppOptions ...func(*baseapp.BaseApp),
) *App {
	encCfg := MakeEncodingConfig(Bech32Prefix)
	appCodec := encCfg.Codec
	interfaceRegistry := encCfg.InterfaceRegistry

	bApp := baseapp.NewBaseApp(Name, logger, db, encCfg.TxConfig.TxDecoder(), baseAppOptions...)
	bApp.SetVersion("0.1")
	bApp.SetInterfaceRegistry(interfaceRegistry)

	keys := storetypes.NewKVStoreKeys(
		authtypes.StoreKey, banktypes.StoreKey, stakingtypes.StoreKey,
		"consensus", upgradetypes.StoreKey,
		capabilitytypes.StoreKey, ibcexported.StoreKey, ibctransfertypes.StoreKey,
	)
	memKeys := storetypes.NewMemoryStoreKeys(capabilitytypes.MemStoreKey)

	app := &App{
		BaseApp:           bApp,
		cdc:               appCodec,
		interfaceRegistry: interfaceRegistry,
		keys:              keys,
		memKeys:           memKeys,
		txConfig:          encCfg.TxConfig,
	}
	app.MountKVStores(keys)
	app.MountMemoryStores(memKeys)

	authority := authtypes.NewModuleAddress("gov").String()

	maccPerms := map[string][]string{
		authtypes.FeeCollectorName:     nil,
		stakingtypes.BondedPoolName:    {authtypes.Burner, authtypes.Staking},
		stakingtypes.NotBondedPoolName: {authtypes.Burner, authtypes.Staking},
		ibctransfertypes.ModuleName:    {authtypes.Minter, authtypes.Burner},
	}

	app.AccountKeeper = authkeeper.NewAccountKeeper(
		appCodec, runtime.NewKVStoreService(keys[authtypes.StoreKey]), authtypes.ProtoBaseAccount,
		maccPerms, address.NewBech32Codec(Bech32Prefix), Bech32Prefix, authority,
	)
	app.BankKeeper = bankkeeper.NewBaseKeeper(
		appCodec, runtime.NewKVStoreService(keys[banktypes.StoreKey]), app.AccountKeeper,
		nil, authority, logger,
	)
	app.StakingKeeper = stakingkeeper.NewKeeper(
		appCodec, runtime.NewKVStoreService(keys[stakingtypes.StoreKey]), app.AccountKeeper, app.BankKeeper,
		authority, address.NewBech32Codec(Bech32Prefix+"valoper"), address.NewBech32Codec(Bech32Prefix+"valcons"),
	)
	app.ConsensusKeeper = consensuskeeper.NewKeeper(
		appCodec, runtime.NewKVStoreService(keys["consensus"]), authority, runtime.EventService{},
	)
	app.SetParamStore(&app.ConsensusKeeper.ParamsStore)
	app.UpgradeKeeper = upgradekeeper.NewKeeper(
		nil, runtime.NewKVStoreService(keys[upgradetypes.StoreKey]), appCodec, DefaultNodeHome, bApp, authority,
	)

	app.CapabilityKeeper = capabilitykeeper.NewKeeper(appCodec, keys[capabilitytypes.StoreKey], memKeys[capabilitytypes.MemStoreKey])
	app.ScopedIBCKeeper = app.CapabilityKeeper.ScopeToModule(ibcexported.ModuleName)
	app.ScopedTransferKeeper = app.CapabilityKeeper.ScopeToModule(ibctransfertypes.ModuleName)
	app.CapabilityKeeper.Seal()

	app.IBCKeeper = ibckeeper.NewKeeper(
		appCodec, keys[ibcexported.StoreKey], noLegacyParamSubspace{},
		app.StakingKeeper, app.UpgradeKeeper, app.ScopedIBCKeeper, authority,
	)
	app.TransferKeeper = ibctransferkeeper.NewKeeper(
		appCodec, keys[ibctransfertypes.StoreKey], noLegacyParamSubspace{},
		app.IBCKeeper.ChannelKeeper, app.IBCKeeper.ChannelKeeper, app.IBCKeeper.PortKeeper,
		app.AccountKeeper, app.BankKeeper, app.ScopedTransferKeeper, authority,
	)

	ibcRouter := newIBCRouter(app)
	app.IBCKeeper.SetRouter(ibcRouter)

	consensusModule := consensus.NewAppModule(appCodec, app.ConsensusKeeper)
	genutilModule := genutil.NewAppModule(app.AccountKeeper, app.StakingKeeper, app, encCfg.TxConfig)

	app.mm = module.NewManager(
		auth.NewAppModule(appCodec, app.AccountKeeper, nil, nil),
		bank.NewAppModule(appCodec, app.BankKeeper, app.AccountKeeper, nil),
		staking.NewAppModule(appCodec, app.StakingKeeper, app.AccountKeeper, app.BankKeeper, nil),
		consensusModule,
		upgrademodule.NewAppModule(app.UpgradeKeeper, address.NewBech32Codec(Bech32Prefix)),
		genutilModule,
		capability.NewAppModule(appCodec, *app.CapabilityKeeper, false),
		ibc.NewAppModule(app.IBCKeeper),
		ibctm.NewAppModule(),
		transfer.NewAppModule(app.TransferKeeper),
	)

	app.mm.SetOrderInitGenesis(
		capabilitytypes.ModuleName, authtypes.ModuleName, banktypes.ModuleName,
		stakingtypes.ModuleName, "consensus", upgradetypes.ModuleName,
		ibcexported.ModuleName, ibctransfertypes.ModuleName, genutiltypes.ModuleName,
	)
	app.mm.SetOrderBeginBlockers(
		upgradetypes.ModuleName, capabilitytypes.ModuleName, stakingtypes.ModuleName,
		ibcexported.ModuleName, ibctransfertypes.ModuleName,
		authtypes.ModuleName, banktypes.ModuleName, "consensus", genutiltypes.ModuleName,
	)
	app.mm.SetOrderEndBlockers(
		stakingtypes.ModuleName, ibcexported.ModuleName, ibctransfertypes.ModuleName,
		authtypes.ModuleName, banktypes.ModuleName, capabilitytypes.ModuleName,
		"consensus", upgradetypes.ModuleName, genutiltypes.ModuleName,
	)

	app.BasicModuleManager = module.NewBasicManagerFromManager(app.mm, nil)
	app.BasicModuleManager.RegisterInterfaces(interfaceRegistry)

	configurator := module.NewConfigurator(appCodec, bApp.MsgServiceRouter(), bApp.GRPCQueryRouter())
	app.mm.RegisterServices(configurator)

	stdAnteHandler, err := authante.NewAnteHandler(authante.HandlerOptions{
		AccountKeeper:   app.AccountKeeper,
		BankKeeper:      app.BankKeeper,
		SignModeHandler: encCfg.TxConfig.SignModeHandler(),
		SigGasConsumer:  sigGasConsumer,
	})
	if err != nil {
		panic(err)
	}
	app.SetAnteHandler(stdAnteHandler)
	app.SetInitChainer(app.InitChainer)
	app.SetBeginBlocker(app.mm.BeginBlock)
	app.SetEndBlocker(app.mm.EndBlock)

	if loadLatest {
		if err := app.LoadLatestVersion(); err != nil {
			panic(err)
		}
	}
	return app
}

// sigGasConsumer charges an eth_secp256k1 signature like a secp256k1 one
// (as Injective does), and everything else as the SDK does.
func sigGasConsumer(meter storetypes.GasMeter, sig signingtypes.SignatureV2, params authtypes.Params) error {
	if _, ok := sig.PubKey.(*ethsecp256k1.PubKey); ok {
		meter.ConsumeGas(params.SigVerifyCostSecp256k1, "ante verify: eth_secp256k1")
		return nil
	}
	return authante.DefaultSigVerificationGasConsumer(meter, sig, params)
}

func (app *App) InitChainer(ctx sdk.Context, req *abci.RequestInitChain) (*abci.ResponseInitChain, error) {
	var genesisState map[string]json.RawMessage
	if err := json.Unmarshal(req.AppStateBytes, &genesisState); err != nil {
		return nil, err
	}
	resp, err := app.mm.InitGenesis(ctx, app.cdc, genesisState)
	if err != nil {
		return nil, err
	}
	// x/upgrade's own InitGenesis never writes anything (its genesis is
	// just {}), so without this its IAVL store would go from mounted to
	// forever un-rooted: a store that never receives a single write
	// never gets a version-1 root committed, and rootmulti requires
	// EVERY mounted store to load at a given version -- so one
	// perpetually rootless store breaks every historical (and even
	// latest-height) query for the WHOLE app, not just upgrade's own.
	// Real cosmos-sdk apps always seed this map at genesis; this was the
	// one line missing here.
	if err := app.UpgradeKeeper.SetModuleVersionMap(ctx, app.mm.GetVersionMap()); err != nil {
		return nil, err
	}
	resp.ConsensusParams = req.ConsensusParams
	resp.AppHash = app.LastCommitID().Hash
	return resp, nil
}

func (app *App) RegisterAPIRoutes(apiSvr *api.Server, apiConfig config.APIConfig) {}
func (app *App) RegisterTxService(clientCtx client.Context) {
	authtx.RegisterTxService(app.BaseApp.GRPCQueryRouter(), clientCtx, app.BaseApp.Simulate, app.interfaceRegistry)
}
func (app *App) RegisterTendermintService(clientCtx client.Context) {
	cmtservice.RegisterTendermintService(clientCtx, app.BaseApp.GRPCQueryRouter(), app.interfaceRegistry, app.Query)
}
func (app *App) RegisterNodeService(clientCtx client.Context, cfg config.Config) {}
