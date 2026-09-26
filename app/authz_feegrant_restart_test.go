package app

import (
	"testing"

	pruningtypes "cosmossdk.io/store/pruning/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log"
)

// As a live node runs it: LevelDB, default pruning, and many blocks after
// activation before the next restart. The new stores must still load, and
// queries at the latest height must still open.
func TestAuthzFeegrant_NodeRestartsAndAnswersQueriesLongAfterActivation(t *testing.T) {
	const activation = 3
	defer func(orig int64) { authzFeegrantActivationHeight = orig }(authzFeegrantActivationHeight)
	authzFeegrantActivationHeight = activation

	dir := t.TempDir()
	open := func() (*App, dbm.DB) {
		db, err := dbm.NewGoLevelDB("application", dir, nil)
		require.NoError(t, err)
		a := New(log.NewNopLogger(), db, nil, true, nil, t.TempDir(), 0, emptyAppOptions{},
			baseapp.SetChainID(testChainID), baseapp.SetPruning(pruningtypes.NewPruningOptions(pruningtypes.PruningDefault))).(*App)
		return a, db
	}

	before, db := open()
	initTestChain(t, before)
	finalizeAndCommit(t, before, 1)
	finalizeAndCommit(t, before, 2)
	require.ErrorContains(t, finalizeBlock(before, activation), "restart this node")
	require.NoError(t, db.Close())

	at, db := open()
	const last = activation + 40 // past several pruning intervals
	for h := int64(activation); h <= last; h++ {
		finalizeAndCommit(t, at, h)
	}
	_, err := at.CommitMultiStore().CacheMultiStoreWithVersion(last)
	require.NoError(t, err, "queries at the latest height must open")
	require.NoError(t, db.Close())

	after, db := open() // any later restart
	defer db.Close()
	require.True(t, after.authzFeegrantWired)
	finalizeAndCommit(t, after, last+1)
	ms, err := after.CommitMultiStore().CacheMultiStoreWithVersion(last + 1)
	require.NoError(t, err)
	require.NotPanics(t, func() { ms.GetKVStore(after.keys[banktypes.StoreKey]).Get([]byte("any")) })
}
