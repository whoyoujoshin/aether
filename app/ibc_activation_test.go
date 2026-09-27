package app

import (
	"testing"
	"time"

	dbm "github.com/cosmos/cosmos-db"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

func TestPlanIBC(t *testing.T) {
	const activation = 100
	cases := []struct {
		lastCommitted int64
		want          ibcPlan
	}{
		{0, ibcPlan{}},
		{98, ibcPlan{}},
		{99, ibcPlan{wire: true, addStores: true}},
		{100, ibcPlan{wire: true}},
		{5000, ibcPlan{wire: true}},
	}
	for _, c := range cases {
		require.Equal(t, c.want, planIBC(c.lastCommitted, activation), "last committed height %d", c.lastCommitted)
	}

	// A chain launched with the feature on from genesis: fresh DB,
	// nothing to upgrade.
	require.Equal(t, ibcPlan{wire: true}, planIBC(0, 0))
	require.Equal(t, ibcPlan{wire: true, addStores: true}, planIBC(0, 1))
}

// Drives a real multistore across the activation boundary, exactly like
// TestAuthzFeegrant_StoresAddedExactlyAtActivationHeight: the three
// restarts a live node actually goes through (before, at, and after the
// one-time store upgrade).
func TestIBC_StoresAddedExactlyAtActivationHeight(t *testing.T) {
	const activation = 3
	defer func(orig int64) { ibcActivationHeight = orig }(ibcActivationHeight)
	ibcActivationHeight = activation

	const msgTransferURL = "/ibc.applications.transfer.v1.MsgTransfer"
	db := dbm.NewMemDB()

	// Before activation: no IBC stores, and the node's registry can't
	// even decode MsgTransfer, same as the previous binary.
	before := newTestApp(t, db)
	require.False(t, before.ibcWired)
	require.Nil(t, before.IBCKeeper)
	_, err := before.interfaceRegistry.Resolve(msgTransferURL)
	require.Error(t, err, "pre-activation node must not know IBC transfer message types")

	initTestChain(t, before)
	finalizeAndCommit(t, before, 1)
	finalizeAndCommit(t, before, 2)
	for _, name := range ibcStoreKeys {
		require.False(t, committedStoreNames(t, before, 2)[name], "%s must not be in the AppHash before activation", name)
	}

	// Reaching the activation block unwired halts instead of executing
	// it without the stores.
	require.ErrorContains(t, finalizeBlock(before, activation), "restart this node")

	// Restart at last committed height activation-1: stores are added
	// via StoreUpgrades and the activation block executes.
	atActivation := newTestApp(t, db)
	require.True(t, atActivation.ibcWired)
	require.NotNil(t, atActivation.IBCKeeper)
	require.NotNil(t, atActivation.CapabilityKeeper)
	_, err = atActivation.interfaceRegistry.Resolve(msgTransferURL)
	require.NoError(t, err)
	finalizeAndCommit(t, atActivation, activation)
	for _, name := range ibcStoreKeys {
		require.True(t, committedStoreNames(t, atActivation, activation)[name], "%s must be in the AppHash from activation on", name)
	}

	// Any later restart loads the stores normally -- no repeat upgrade,
	// no "version of store mismatch" error.
	after := newTestApp(t, db)
	require.True(t, after.ibcWired)
	finalizeAndCommit(t, after, activation+1)

	// The point of all this: IBC's own client/connection/channel keepers
	// and the ICS-20 transfer keeper are live and usable.
	ctx := after.NewUncachedContext(false, sdk.Context{}.BlockHeader())
	ctx = ctx.WithBlockHeight(activation + 2).WithBlockTime(testGenesisTime.Add(time.Hour)).WithChainID(testChainID)

	require.NotPanics(t, func() { after.IBCKeeper.ClientKeeper.GetParams(ctx) }, "02-client params must be initialized by InitGenesis")
	require.NotPanics(t, func() { after.TransferKeeper.GetParams(ctx) }, "transfer params must be initialized by InitGenesis")

	// Self-consensus tracking (IBC's stand-in for x/staking's
	// HistoricalInfo -- see app/ibc_self_consensus.go) recorded this
	// block's header, and reports a real unbonding-time analog derived
	// from x/pow's own BondCooldown/TargetBlockTime.
	shim := selfConsensusStakingShim{app: after}
	unbonding, err := shim.UnbondingTime(ctx)
	require.NoError(t, err)
	require.Positive(t, unbonding)
}
