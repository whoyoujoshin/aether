package counterparty

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
	paramtypes "github.com/cosmos/cosmos-sdk/x/params/types"
)

// noLegacyParamSubspace satisfies the legacy types.ParamSubspace
// argument ibc-go's 02-client and transfer keeper constructors still
// take for backwards compatibility -- unreachable here since this
// chain never wires x/params and never stores a ConsensusVersion below
// 2 for either module. See app/ibc_params_shim.go in the main Aether
// app for the identical rationale.
type noLegacyParamSubspace struct{}

func (noLegacyParamSubspace) GetParamSet(ctx sdk.Context, ps paramtypes.ParamSet) {
	panic("noLegacyParamSubspace.GetParamSet called: unreachable on this chain")
}

func (noLegacyParamSubspace) GetParamSetIfExists(ctx sdk.Context, ps paramtypes.ParamSet) {
	panic("noLegacyParamSubspace.GetParamSetIfExists called: unreachable on this chain")
}
