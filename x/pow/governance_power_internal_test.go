package pow

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

// GetValidatorVotingPower is what governance weighs a vote by from
// governance.PowerWeightedVotingHeight (GHSA-3cvf).
func TestGetValidatorVotingPower(t *testing.T) {
	k, ctx := newBeaconTestKeeper(t)
	seated := sdk.AccAddress("gov_power_seated____")
	flat := sdk.AccAddress("gov_power_flat______")
	outside := sdk.AccAddress("gov_power_outside___")

	k.SetActiveValidator(ctx, seated)
	k.setValidatorPower(ctx, seated, 33_721_908)
	k.SetActiveValidator(ctx, flat)
	k.setValidatorPower(ctx, outside, 5_000_000) // a record, but not in the set

	require.Equal(t, int64(33_721_908), k.GetValidatorVotingPower(ctx, seated), "the seat rule's power")
	require.Equal(t, int64(ValidatorVotingPower), k.GetValidatorVotingPower(ctx, flat), "no record: the old flat power")
	require.Zero(t, k.GetValidatorVotingPower(ctx, outside), "not active: nothing")

	k.RemoveActiveValidator(ctx, seated)
	require.Zero(t, k.GetValidatorVotingPower(ctx, seated), "removed: nothing")
}
