package app

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	"cosmossdk.io/store/rootmulti"
	"cosmossdk.io/x/feegrant"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	// aetherd's main does this before New(); keepers validate their
	// authority addresses against the configured prefix.
	SetAddressPrefixes()
	os.Exit(m.Run())
}

func TestPlanAuthzFeegrant(t *testing.T) {
	const activation = 100
	cases := []struct {
		lastCommitted int64
		want          authzFeegrantPlan
	}{
		{0, authzFeegrantPlan{}},
		{98, authzFeegrantPlan{}},
		{99, authzFeegrantPlan{wire: true, addStores: true}},
		{100, authzFeegrantPlan{wire: true}},
		{5000, authzFeegrantPlan{wire: true}},
	}
	for _, c := range cases {
		require.Equal(t, c.want, planAuthzFeegrant(c.lastCommitted, activation), "last committed height %d", c.lastCommitted)
	}

	// A chain launched with the feature on from genesis: fresh DB,
	// nothing to upgrade.
	require.Equal(t, authzFeegrantPlan{wire: true}, planAuthzFeegrant(0, 0))
	require.Equal(t, authzFeegrantPlan{wire: true, addStores: true}, planAuthzFeegrant(0, 1))
}

var testGenesisTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

const testChainID = "authz-feegrant-test"

type emptyAppOptions struct{}

func (emptyAppOptions) Get(string) interface{} { return nil }

func newTestApp(t *testing.T, db dbm.DB) *App {
	t.Helper()
	return New(log.NewNopLogger(), db, nil, true, nil, t.TempDir(), 0, emptyAppOptions{}, baseapp.SetChainID(testChainID)).(*App)
}

func initTestChain(t *testing.T, app *App) {
	t.Helper()
	genesis, err := json.Marshal(ModuleBasics.DefaultGenesis(MakeEncodingConfig().Codec))
	require.NoError(t, err)
	_, err = app.InitChain(&abci.RequestInitChain{
		ChainId:         testChainID,
		Time:            testGenesisTime,
		InitialHeight:   1,
		AppStateBytes:   genesis,
		ConsensusParams: simtestutil.DefaultConsensusParams,
	})
	require.NoError(t, err)
}

func finalizeBlock(app *App, height int64) error {
	_, err := app.FinalizeBlock(&abci.RequestFinalizeBlock{
		Height: height,
		Time:   testGenesisTime.Add(time.Duration(height) * time.Minute),
	})
	return err
}

func finalizeAndCommit(t *testing.T, app *App, height int64) {
	t.Helper()
	require.NoError(t, finalizeBlock(app, height), "finalize height %d", height)
	_, err := app.Commit()
	require.NoError(t, err, "commit height %d", height)
}

func committedStoreNames(t *testing.T, app *App, height int64) map[string]bool {
	t.Helper()
	info, err := app.CommitMultiStore().(*rootmulti.Store).GetCommitInfo(height)
	require.NoError(t, err)
	names := map[string]bool{}
	for _, si := range info.StoreInfos {
		names[si.Name] = true
	}
	return names
}

// Drives a real multistore across the activation boundary: the three
// restarts a live node actually goes through (before, at, and after the
// one-time store upgrade).
func TestAuthzFeegrant_StoresAddedExactlyAtActivationHeight(t *testing.T) {
	const activation = 3
	defer func(orig int64) { authzFeegrantActivationHeight = orig }(authzFeegrantActivationHeight)
	authzFeegrantActivationHeight = activation

	const msgGrantURL = "/cosmos.authz.v1beta1.MsgGrant"
	db := dbm.NewMemDB()

	// Before activation: built exactly as before this change -- no
	// stores, and the node's registry can't even decode MsgGrant, same
	// as the previous binary.
	before := newTestApp(t, db)
	require.False(t, before.authzFeegrantWired)
	_, err := before.interfaceRegistry.Resolve(msgGrantURL)
	require.Error(t, err, "pre-activation node must not know authz message types")

	initTestChain(t, before)
	finalizeAndCommit(t, before, 1)
	finalizeAndCommit(t, before, 2)
	for _, name := range authzFeegrantStoreKeys {
		require.False(t, committedStoreNames(t, before, 2)[name], "%s must not be in the AppHash before activation", name)
	}

	// Reaching the activation block unwired halts instead of executing
	// it without the stores.
	require.ErrorContains(t, finalizeBlock(before, activation), "restart this node")

	// Restart at last committed height activation-1: stores are added
	// via StoreUpgrades and the activation block executes.
	atActivation := newTestApp(t, db)
	require.True(t, atActivation.authzFeegrantWired)
	_, err = atActivation.interfaceRegistry.Resolve(msgGrantURL)
	require.NoError(t, err)
	finalizeAndCommit(t, atActivation, activation)
	for _, name := range authzFeegrantStoreKeys {
		require.True(t, committedStoreNames(t, atActivation, activation)[name], "%s must be in the AppHash from activation on", name)
	}

	// Any later restart loads the stores normally -- no repeat upgrade,
	// no "version of store mismatch" error.
	after := newTestApp(t, db)
	require.True(t, after.authzFeegrantWired)
	finalizeAndCommit(t, after, activation+1)

	// Queries pinned to a pre-activation height (wallet.HeightMetadataKey)
	// must still open, even though these stores didn't exist then.
	historical, err := after.CommitMultiStore().CacheMultiStoreWithVersion(activation - 1)
	require.NoError(t, err)
	require.NotPanics(t, func() { historical.GetKVStore(after.keys[banktypes.StoreKey]).Get([]byte("any")) })

	// The point of all this: an on-chain-enforced spending cap for an
	// agent account, and fee delegation so it needs no gas of its own.
	ctx := after.NewUncachedContext(false, cmtproto.Header{Height: activation + 2, Time: testGenesisTime.Add(time.Hour)})
	human := sdk.AccAddress("human_principal_____")
	agent := sdk.AccAddress("ai_agent_account____")
	recipient := sdk.AccAddress("some_recipient______")
	capCoins := sdk.NewCoins(sdk.NewCoin("uaeth", sdkmath.NewInt(1_000_000)))
	expires := testGenesisTime.Add(24 * time.Hour)

	require.NoError(t, after.AuthzKeeper.SaveGrant(ctx, agent, human, banktypes.NewSendAuthorization(capCoins, nil), &expires))
	auth, _ := after.AuthzKeeper.GetAuthorization(ctx, agent, human, sdk.MsgTypeURL(&banktypes.MsgSend{}))
	require.NotNil(t, auth)

	within := banktypes.NewMsgSend(human, recipient, sdk.NewCoins(sdk.NewCoin("uaeth", sdkmath.NewInt(400_000))))
	resp, err := auth.Accept(ctx, within)
	require.NoError(t, err)
	require.True(t, resp.Accept)
	remaining := resp.Updated.(*banktypes.SendAuthorization).SpendLimit
	require.Equal(t, "600000uaeth", remaining.String(), "the cap must shrink by what was spent")

	over := banktypes.NewMsgSend(human, recipient, sdk.NewCoins(sdk.NewCoin("uaeth", sdkmath.NewInt(2_000_000))))
	_, err = auth.Accept(ctx, over)
	require.Error(t, err, "the chain itself must reject spending past the granted cap")

	require.NoError(t, after.FeeGrantKeeper.GrantAllowance(ctx, human, agent, &feegrant.BasicAllowance{SpendLimit: capCoins, Expiration: &expires}))
	allowance, err := after.FeeGrantKeeper.GetAllowance(ctx, human, agent)
	require.NoError(t, err)
	require.NotNil(t, allowance)
}
