package pow

import (
	"encoding/json"
	"context"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	"github.com/grpc-ecosystem/grpc-gateway/runtime"
	abci "github.com/cometbft/cometbft/abci/types"
	"cosmossdk.io/math"
	crypto "github.com/cometbft/cometbft/proto/tendermint/crypto"
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

func (AppModuleBasic) RegisterInterfaces(registry cdctypes.InterfaceRegistry) {
	registry.RegisterImplementations((*sdk.Msg)(nil), &MsgSubmitPoW{})
	registry.RegisterImplementations((*sdk.Msg)(nil), &MsgRegisterValidatorPubkey{})
}

func (AppModuleBasic) DefaultGenesis(cdc codec.JSONCodec) json.RawMessage {
	genState := DefaultGenesisState()
	bz, _ := json.Marshal(&genState)
	return bz
}

func (AppModuleBasic) ValidateGenesis(cdc codec.JSONCodec, config client.TxEncodingConfig, bz json.RawMessage) error {
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

func (am AppModule) RegisterServices(cfg module.Configurator) {
	RegisterMsgServer(cfg.MsgServer(), NewMsgServerImpl(am.keeper))
	RegisterQueryServer(cfg.QueryServer(), NewQueryServerImpl(am.keeper))
}

func (am AppModule) ConsensusVersion() uint64 {
	return 1
}

func (am AppModule) InitGenesis(ctx sdk.Context, cdc codec.JSONCodec, data json.RawMessage) []abci.ValidatorUpdate {
	var genState GenesisState
	json.Unmarshal(data, &genState)

	am.keeper.SetBlockReward(ctx, math.NewInt(int64(genState.Params.BlockReward)))
	am.keeper.SetDifficulty(ctx, math.NewInt(int64(genState.Params.Difficulty)))
	am.keeper.SetMinDifficulty(ctx, int64(genState.Params.MinDifficulty))
	am.keeper.SetMaxDifficulty(ctx, int64(genState.Params.MaxDifficulty))
	am.keeper.SetTargetBlockTime(ctx, genState.Params.TargetBlockTime)
	am.keeper.SetEpochLength(ctx, genState.Params.EpochLength)
	am.keeper.SetTopKSize(ctx, genState.Params.TopKSize)
	am.keeper.SetBondCooldown(ctx, genState.Params.BondCooldown)

	return []abci.ValidatorUpdate{
		{
			PubKey: crypto.PublicKey{
				Sum: &crypto.PublicKey_Ed25519{
					Ed25519: make([]byte, 32), // dummy - will be overridden by genesis
				},
			},
			Power: 1000000000000,
		},
	}
}

func (am AppModule) ExportGenesis(ctx sdk.Context, cdc codec.JSONCodec) json.RawMessage {
	genState := GenesisState{
		Params: Params{
			BlockReward: int(am.keeper.GetBlockReward(ctx).Int64()),
			Difficulty:  int(am.keeper.GetDifficulty(ctx).Int64()),
		},
	}
	bz, _ := json.Marshal(&genState)
	return bz
}

func (am AppModule) EndBlock(ctx context.Context) ([]abci.ValidatorUpdate, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	am.keeper.ReleaseMaturedEscrows(sdkCtx)

	// Immediate removals from misbehavior: unconditional, every block --
	// independent of epoch timing. A banned validator must lose voting
	// power the same block their ban is processed, not wait for the next
	// epoch boundary.
	var updates []abci.ValidatorUpdate
	for _, minerAddr := range am.keeper.IteratePendingRemovals(sdkCtx) {
		pubkey, ok := am.keeper.GetValidatorPubkey(sdkCtx, minerAddr)
		if ok {
			if update, ok := am.keeper.toValidatorUpdate(pubkey, 0, minerAddr); ok {
				updates = append(updates, update)
			}
		}
		am.keeper.ClearPendingRemoval(sdkCtx, minerAddr)
	}

	epochLength := am.keeper.GetEpochLength(sdkCtx)
	if epochLength <= 0 {
		return updates, nil
	}

	height := sdkCtx.BlockHeight()
	if (height+1)%epochLength != 0 {
		return updates, nil
	}

	epoch := am.keeper.CurrentEpoch(sdkCtx)
	epochUpdates := am.keeper.ComputeValidatorUpdates(sdkCtx, epoch)
	updates = append(updates, epochUpdates...)
	return updates, nil
}

func (am AppModule) BeginBlock(ctx context.Context) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	am.keeper.ProcessMisbehavior(sdkCtx)
	return nil
}