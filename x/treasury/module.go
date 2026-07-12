package treasury

import (
	"encoding/json"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	"github.com/grpc-ecosystem/grpc-gateway/runtime"
	abci "github.com/cometbft/cometbft/abci/types"
	"cosmossdk.io/math"
)

var (
	_ module.AppModule      = AppModule{}
	_ module.AppModuleBasic = AppModuleBasic{}
)

type AppModuleBasic struct{}

func (AppModuleBasic) Name() string {
	return ModuleName
}

func (AppModuleBasic) RegisterLegacyAminoCodec(cdc *codec.LegacyAmino) {}

func (AppModuleBasic) RegisterInterfaces(registry cdctypes.InterfaceRegistry) {}

func (AppModuleBasic) DefaultGenesis(cdc codec.JSONCodec) json.RawMessage {
	genState := DefaultGenesisState()
	bz, _ := json.Marshal(&genState)
	return bz
}

func (AppModuleBasic) ValidateGenesis(cdc codec.JSONCodec, config interface{}, bz json.RawMessage) error {
	var genState GenesisState
	return json.Unmarshal(bz, &genState)
}

func (AppModuleBasic) RegisterGRPCGatewayRoutes(ctx client.Context, mux *runtime.ServeMux) {}

type AppModule struct {
	AppModuleBasic
	keeper Keeper
	cdc    codec.Codec
}

func NewAppModule(cdc codec.Codec, keeper Keeper) AppModule {
	return AppModule{
		AppModuleBasic: AppModuleBasic{},
		keeper:         keeper,
		cdc:            cdc,
	}
}

func (am AppModule) IsAppModule() {}

func (am AppModule) IsOnePerModuleType() {}

func (am AppModule) RegisterServices(cfg module.Configurator) {}

func (am AppModule) ConsensusVersion() uint64 {
	return 1
}

func (am AppModule) InitGenesis(ctx sdk.Context, cdc codec.JSONCodec, data json.RawMessage) []abci.ValidatorUpdate {
	var genState GenesisState
	json.Unmarshal(data, &genState)
	am.keeper.FundTreasury(ctx, math.NewInt(int64(genState.Params.InitialBalance)))
	return []abci.ValidatorUpdate{}
}

func (am AppModule) ExportGenesis(ctx sdk.Context, cdc codec.JSONCodec) json.RawMessage {
	genState := DefaultGenesisState()
	bz, _ := json.Marshal(&genState)
	return bz
}