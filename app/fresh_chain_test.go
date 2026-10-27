package app

import (
	"encoding/json"
	"testing"

	"cosmossdk.io/log"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/gates"
	"github.com/whoyoujoshin/aether/x/escrow"
)

func newChainApp(t *testing.T, db dbm.DB, chainID string) *App {
	t.Helper()
	return New(log.NewNopLogger(), db, nil, true, nil, t.TempDir(), 0, emptyAppOptions{}, baseapp.SetChainID(chainID)).(*App)
}

// genesisOn starts the chain from a genesis with only the modules the
// testnet launched with, as docs/genesis.template.json has: the rest
// must start from their defaults.
func genesisOn(t *testing.T, app *App, chainID string) {
	t.Helper()
	full := ModuleBasics.DefaultGenesis(MakeEncodingConfig().Codec)
	launch := map[string]json.RawMessage{}
	for _, name := range []string{"auth", "bank", "pow", "treasury", "governance"} {
		launch[name] = full[name]
	}
	genesis, err := json.Marshal(launch)
	require.NoError(t, err)
	_, err = app.InitChain(&abci.RequestInitChain{
		ChainId:         chainID,
		Time:            testGenesisTime,
		InitialHeight:   1,
		AppStateBytes:   genesis,
		ConsensusParams: simtestutil.DefaultConsensusParams,
	})
	require.NoError(t, err)
}

// A fresh chain (docs/MAINNET.md §4) starts with every gate open: every
// module that the testnet added at a height is wired from the first
// start, its stores are in the AppHash from block 1, and blocks keep
// committing across a restart.
func TestFreshChain_EveryModuleFromBlockOne(t *testing.T) {
	const chainID = gates.RehearsalPrefix + "test"
	db := dbm.NewMemDB()

	first := newChainApp(t, db, chainID)
	require.True(t, first.authzFeegrantWired)
	require.True(t, first.ibcWired)
	require.True(t, first.accountAuthWired)
	require.True(t, first.escrowWired)
	require.True(t, first.inflowCapWired)

	genesisOn(t, first, chainID)
	finalizeAndCommit(t, first, 1)
	names := committedStoreNames(t, first, 1)
	for _, set := range [][]string{authzFeegrantStoreKeys, ibcStoreKeys, accountAuthStoreKeys, escrowStoreKeys, inflowCapStoreKeys} {
		for _, name := range set {
			require.True(t, names[name], "%s must be in the AppHash from block 1", name)
		}
	}
	finalizeAndCommit(t, first, 2)

	// IBC's modules have their params, from their defaults.
	ctx := first.NewUncachedContext(false, cmtproto.Header{ChainID: chainID, Height: 3})
	require.NotPanics(t, func() { first.TransferKeeper.GetParams(ctx) })
	require.NotPanics(t, func() { first.ICAHostKeeper.GetParams(ctx) })
	require.True(t, first.TransferKeeper.GetParams(ctx).SendEnabled)
	require.NotNil(t, first.AccountKeeper.GetModuleAccount(ctx, escrow.ModuleName))
	// The inflow cap starts from its default, which lists nothing: every
	// token from another chain is refused until genesis lists it. The
	// testnet's entries are for the testnet only.
	require.True(t, first.InflowCapKeeper.HasParams(ctx))
	require.Empty(t, first.InflowCapKeeper.GetParams(ctx).Entries)
	require.True(t, inflowCapHost{first}.Active(ctx))
	require.True(t, gates.On(ctx, feesToTreasuryActivationHeight), "fees go to the treasury from block 1")

	again := newChainApp(t, db, chainID)
	require.True(t, again.ibcWired)
	finalizeAndCommit(t, again, 3)
	finalizeAndCommit(t, again, 4)
}

// The testnet itself is unchanged: on its chain ID a fresh database
// still starts with none of those modules wired.
func TestTestnetChain_GatesUnchanged(t *testing.T) {
	app := newChainApp(t, dbm.NewMemDB(), gates.TestnetChainID)
	require.False(t, app.authzFeegrantWired)
	require.False(t, app.ibcWired)
	require.False(t, app.accountAuthWired)
	require.False(t, app.escrowWired)
	require.False(t, app.inflowCapWired)
}
