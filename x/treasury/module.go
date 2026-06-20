package treasury

import (
	"github.com/cosmos/cosmos-sdk/codec"
	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"
)

type AppModuleBasic struct{}

func (AppModuleBasic) Name() string { return "pow" } // Change to treasury or governance

func (AppModuleBasic) RegisterLegacyAminoCodec(cdc *codec.LegacyAmino) {}

func (AppModuleBasic) RegisterInterfaces(registry cdctypes.InterfaceRegistry) {}

func (AppModuleBasic) DefaultGenesis(cdc codec.JSONCodec) []byte {
	return nil
}

func (AppModuleBasic) ValidateGenesis(cdc codec.JSONCodec, config interface{}, bz []byte) error {
	return nil
}

type AppModule struct {
	AppModuleBasic
}

func NewAppModule() AppModule {
	return AppModule{}
}

func (AppModule) ConsensusVersion() uint64 { return 1 }