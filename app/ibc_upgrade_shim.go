package app

import (
	"context"
	"errors"

	upgradetypes "cosmossdk.io/x/upgrade/types"
)

// noIBCSoftwareUpgrades implements the clienttypes.UpgradeKeeper
// interface ibc-go's core keeper requires a non-empty value for (see
// 02-client/keeper.NewKeeper's isEmpty checks), without pulling in the
// full x/upgrade module.
//
// Aether has no x/upgrade: every coordinated change to what's mounted
// on this chain (AuthzFeegrantActivationHeight, IBCActivationHeight)
// goes through this project's own height-gated activation pattern
// instead of x/upgrade's governance-scheduled binary swaps. IBC's own
// "software upgrade" path (MsgIBCSoftwareUpgrade, forcing a client-state
// upgrade for every counterparty client) is therefore never wired into
// this chain's message routing, so these methods are unreachable in
// normal operation. They return a clear error rather than silently
// pretending to support a feature that doesn't exist, so a future
// attempt to wire that message route fails loudly here instead of
// quietly doing nothing.
type noIBCSoftwareUpgrades struct {
	// nonZero exists only so this stub satisfies ibc-go's isEmpty()
	// reflection check: an all-zero-value struct (this one has no other
	// fields) is indistinguishable from "no keeper provided" and would
	// panic at construction. This is not a workaround for a missing real
	// keeper -- see the type doc above.
	nonZero bool
}

func newNoIBCSoftwareUpgrades() noIBCSoftwareUpgrades {
	return noIBCSoftwareUpgrades{nonZero: true}
}

var errIBCSoftwareUpgradesUnsupported = errors.New("Aether has no x/upgrade module and does not support IBC software-upgrade proposals; see app.noIBCSoftwareUpgrades")

func (noIBCSoftwareUpgrades) ClearIBCState(ctx context.Context, lastHeight int64) error {
	return nil // nothing was ever scheduled, so there is nothing to clear
}

func (noIBCSoftwareUpgrades) GetUpgradePlan(ctx context.Context) (upgradetypes.Plan, error) {
	return upgradetypes.Plan{}, errIBCSoftwareUpgradesUnsupported
}

func (noIBCSoftwareUpgrades) GetUpgradedClient(ctx context.Context, height int64) ([]byte, error) {
	return nil, errIBCSoftwareUpgradesUnsupported
}

func (noIBCSoftwareUpgrades) SetUpgradedClient(ctx context.Context, planHeight int64, bz []byte) error {
	return errIBCSoftwareUpgradesUnsupported
}

func (noIBCSoftwareUpgrades) GetUpgradedConsensusState(ctx context.Context, lastHeight int64) ([]byte, error) {
	return nil, errIBCSoftwareUpgradesUnsupported
}

func (noIBCSoftwareUpgrades) SetUpgradedConsensusState(ctx context.Context, planHeight int64, bz []byte) error {
	return errIBCSoftwareUpgradesUnsupported
}

func (noIBCSoftwareUpgrades) ScheduleUpgrade(ctx context.Context, plan upgradetypes.Plan) error {
	return errIBCSoftwareUpgradesUnsupported
}
