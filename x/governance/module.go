package governance

import (
	"encoding/json"

	"github.com/cosmos/cosmos-sdk/codec"
	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
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
	return cdc.MustMarshalJSON(&genState)
}

func (AppModuleBasic) ValidateGenesis(cdc codec.JSONCodec, config interface{}, bz json.RawMessage) error {
	var genState GenesisState
	return cdc.UnmarshalJSON(bz, &genState)
}

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

func (am AppModule) RegisterServices(cfg module.Configurator) {}

func (am AppModule) ConsensusVersion() uint64 {
	return 1
}

func (am AppModule) InitGenesis(ctx sdk.Context, cdc codec.JSONCodec, data json.RawMessage) []interface{} {
	var genState GenesisState
	cdc.MustUnmarshalJSON(data, &genState)
	return []interface{}{}
}

func (am AppModule) ExportGenesis(ctx sdk.Context, cdc codec.JSONCodec) json.RawMessage {
	genState := DefaultGenesisState()
	return cdc.MustMarshalJSON(&genState)
}
