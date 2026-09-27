package app

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
	paramtypes "github.com/cosmos/cosmos-sdk/x/params/types"
)

// noLegacyParamSubspace satisfies the legacy types.ParamSubspace
// argument ibc-go's 02-client keeper constructor still takes for
// backwards compatibility. It is only ever read by that keeper's
// Migrate1to2 (moving params off the legacy x/params subspace and onto
// this module's own store), which runs when a module's on-chain
// ConsensusVersion advances from 1 to 2 during RunMigrations.
//
// Aether never wires x/params and never registers a stored
// ConsensusVersion below 2 for 02-client -- this chain's IBC module
// starts fresh at its current version, so that migration is dead code
// here by construction, not by luck. If that ever stops being true (a
// real fromVersion 1 shows up somehow), this panics loudly instead of
// silently reading empty legacy params.
type noLegacyParamSubspace struct{}

func (noLegacyParamSubspace) GetParamSet(ctx sdk.Context, ps paramtypes.ParamSet) {
	panic("noLegacyParamSubspace.GetParamSet called: IBC's legacy-params migration should be unreachable on Aether (see type doc)")
}

// GetParamSetIfExists satisfies interchain accounts' own slightly wider
// ParamSubspace interface (it also wants this non-panicking variant, used
// by its own legacy migration path) -- same unreachability rationale as
// GetParamSet above.
func (noLegacyParamSubspace) GetParamSetIfExists(ctx sdk.Context, ps paramtypes.ParamSet) {
	panic("noLegacyParamSubspace.GetParamSetIfExists called: IBC's legacy-params migration should be unreachable on Aether (see type doc)")
}
