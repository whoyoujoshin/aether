package app

import (
	"math"
	"testing"
	"time"

	"cosmossdk.io/log"
	"cosmossdk.io/store/snapshots"
	snapshottypes "cosmossdk.io/store/snapshots/types"
	abci "github.com/cometbft/cometbft/abci/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/stretchr/testify/require"
)

type mapAppOptions map[string]interface{}

func (m mapAppOptions) Get(k string) interface{} { return m[k] }

var stateSyncOn = mapAppOptions{"statesync.enable": true}

func TestStorePlanHeight(t *testing.T) {
	empty := dbm.NewMemDB()
	require.Equal(t, int64(0), storePlanHeight(empty, emptyAppOptions{}), "an ordinary node replays from genesis")
	require.Equal(t, int64(0), storePlanHeight(empty, nil))
	require.Equal(t, int64(0), storePlanHeight(empty, mapAppOptions{"statesync.enable": false}))
	require.Equal(t, int64(math.MaxInt64), storePlanHeight(empty, stateSyncOn), "a state-syncing node mounts every store")
	require.Equal(t, int64(math.MaxInt64), storePlanHeight(empty, mapAppOptions{"statesync.enable": "true"}))

	// Once anything is saved, the saved height decides, state sync or not
	// (a restored node restarts past every activation, so it mounts them).
	saved := dbm.NewMemDB()
	a := New(log.NewNopLogger(), saved, nil, true, nil, t.TempDir(), 0, emptyAppOptions{}, baseapp.SetChainID(testChainID)).(*App)
	initTestChain(t, a)
	finalizeAndCommit(t, a, 1)
	require.Equal(t, int64(1), storePlanHeight(saved, stateSyncOn))
}

func snapshotApp(t *testing.T, db dbm.DB, opts mapAppOptions, interval uint64) *App {
	t.Helper()
	store, err := snapshots.NewStore(dbm.NewMemDB(), t.TempDir())
	require.NoError(t, err)
	return New(log.NewNopLogger(), db, nil, true, nil, t.TempDir(), 0, opts,
		baseapp.SetChainID(testChainID),
		baseapp.SetSnapshot(store, snapshottypes.NewSnapshotOptions(interval, 2)),
	).(*App)
}

// A fresh node restores a snapshot that carries the height-gated stores
// (here accountauth, IBC, authz/feegrant, escrow and inflowcap, as every
// testnet snapshot since 511,920 does). Before the fix it mounted none of
// them, because nothing was saved yet, and the restore failed on the first
// one: "cannot import into non-IAVL store" (aether-syd1, 2026-11-09).
func TestStateSync_FreshNodeRestoresEveryStore(t *testing.T) {
	// The source: a node with all of those stores, taking a snapshot at 4.
	src := snapshotApp(t, dbm.NewMemDB(), stateSyncOn, 4)
	for _, name := range append(append(append(append(append([]string{}, accountAuthStoreKeys...), ibcStoreKeys...), authzFeegrantStoreKeys...), escrowStoreKeys...), inflowCapStoreKeys...) {
		require.Contains(t, src.keys, name, "the source carries %s", name)
	}
	initTestChain(t, src)
	var hashAt4 []byte
	for h := int64(1); h <= 6; h++ {
		finalizeAndCommit(t, src, h)
		if h == 4 {
			hashAt4 = src.LastCommitID().Hash
		}
	}
	var snap *abci.Snapshot
	require.Eventually(t, func() bool {
		res, err := src.ListSnapshots(&abci.RequestListSnapshots{})
		require.NoError(t, err)
		for _, s := range res.Snapshots {
			if s.Height == 4 {
				snap = s
			}
		}
		return snap != nil
	}, 10*time.Second, 50*time.Millisecond)

	restore := func(dst *App) abci.ResponseApplySnapshotChunk_Result {
		offer, err := dst.OfferSnapshot(&abci.RequestOfferSnapshot{Snapshot: snap, AppHash: hashAt4})
		require.NoError(t, err)
		require.Equal(t, abci.ResponseOfferSnapshot_ACCEPT, offer.Result)
		result := abci.ResponseApplySnapshotChunk_ACCEPT
		for i := uint32(0); i < snap.Chunks; i++ {
			chunk, err := src.LoadSnapshotChunk(&abci.RequestLoadSnapshotChunk{Height: snap.Height, Format: snap.Format, Chunk: i})
			require.NoError(t, err)
			res, err := dst.ApplySnapshotChunk(&abci.RequestApplySnapshotChunk{Index: i, Chunk: chunk.Chunk})
			require.NoError(t, err)
			if res.Result != abci.ResponseApplySnapshotChunk_ACCEPT {
				return res.Result
			}
		}
		return result
	}

	// Without state sync configured, a fresh node mounts only the stores
	// from genesis and can't take the snapshot (the old behaviour).
	old := snapshotApp(t, dbm.NewMemDB(), mapAppOptions{}, 0)
	require.NotContains(t, old.keys, accountAuthStoreKeys[0])
	require.Equal(t, abci.ResponseApplySnapshotChunk_ABORT, restore(old))

	// With [statesync] enable = true it mounts them all and restores.
	dstDB := dbm.NewMemDB()
	dst := snapshotApp(t, dstDB, stateSyncOn, 0)
	require.Equal(t, abci.ResponseApplySnapshotChunk_ACCEPT, restore(dst))
	require.Equal(t, int64(4), dst.LastBlockHeight())
	require.Equal(t, hashAt4, dst.LastCommitID().Hash, "the restored state is the source's at 4")

}
