package escrow

import (
	"context"
	"encoding/json"

	"cosmossdk.io/core/appmodule"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	"github.com/grpc-ecosystem/grpc-gateway/runtime"
	"github.com/spf13/cobra"
)

var (
	_ module.AppModule        = AppModule{}
	_ module.AppModuleBasic   = AppModuleBasic{}
	_ module.HasGenesis       = AppModule{}
	_ appmodule.HasEndBlocker = AppModule{}
)

type AppModuleBasic struct{}

func (AppModuleBasic) Name() string { return ModuleName }

func (AppModuleBasic) RegisterLegacyAminoCodec(cdc *codec.LegacyAmino) {}

func (AppModuleBasic) RegisterInterfaces(registry cdctypes.InterfaceRegistry) {
	registry.RegisterImplementations((*sdk.Msg)(nil),
		&MsgCreateEscrow{},
		&MsgReleaseEscrow{},
		&MsgRefundEscrow{},
	)
}

func (AppModuleBasic) DefaultGenesis(cdc codec.JSONCodec) json.RawMessage {
	g := DefaultGenesisState()
	return cdc.MustMarshalJSON(&g)
}

// ValidateGenesis and InitGenesis treat a missing section as the
// default: genesis files written before this module existed have none.
func (AppModuleBasic) ValidateGenesis(cdc codec.JSONCodec, config client.TxEncodingConfig, bz json.RawMessage) error {
	if len(bz) == 0 {
		return nil
	}
	var g GenesisState
	if err := cdc.UnmarshalJSON(bz, &g); err != nil {
		return err
	}
	return g.Validate()
}

func (AppModuleBasic) RegisterGRPCGatewayRoutes(ctx client.Context, mux *runtime.ServeMux) {}

func (AppModuleBasic) GetTxCmd() *cobra.Command    { return nil }
func (AppModuleBasic) GetQueryCmd() *cobra.Command { return nil }

type AppModule struct {
	AppModuleBasic
	keeper Keeper
}

func NewAppModule(keeper Keeper) AppModule {
	return AppModule{keeper: keeper}
}

func (am AppModule) IsAppModule() {}

func (am AppModule) IsOnePerModuleType() {}

func (am AppModule) RegisterServices(cfg module.Configurator) {
	RegisterMsgServer(cfg.MsgServer(), NewMsgServerImpl(am.keeper))
	RegisterQueryServer(cfg.QueryServer(), NewQueryServerImpl(am.keeper))
}

func (am AppModule) ConsensusVersion() uint64 { return 1 }

func (am AppModule) InitGenesis(ctx sdk.Context, cdc codec.JSONCodec, data json.RawMessage) {
	g := DefaultGenesisState()
	if len(data) > 0 {
		cdc.MustUnmarshalJSON(data, &g)
	}
	am.keeper.InitGenesis(ctx, g)
}

func (am AppModule) ExportGenesis(ctx sdk.Context, cdc codec.JSONCodec) json.RawMessage {
	g := am.keeper.ExportGenesis(ctx)
	return cdc.MustMarshalJSON(&g)
}

// EndBlock settles escrows whose deadline has passed.
func (am AppModule) EndBlock(ctx context.Context) error {
	am.keeper.ProcessExpired(sdk.UnwrapSDKContext(ctx))
	return nil
}
