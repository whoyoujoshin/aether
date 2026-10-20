package wallet

import (
	"testing"

	"cosmossdk.io/log"
	"cosmossdk.io/math"
	"cosmossdk.io/store/iavl"
	"cosmossdk.io/store/metrics"
	storetypes "cosmossdk.io/store/types"
	cometed25519 "github.com/cometbft/cometbft/crypto/ed25519"
	dbm "github.com/cosmos/cosmos-db"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protowire"

	"github.com/whoyoujoshin/aether/x/pow"
)

// The seat work a node serves: a real IAVL store answers the same
// "/subspace" query seatWindow sends, and decodeKVPairs reads it back.
func TestDecodeKVPairs_RealStoreSubspace(t *testing.T) {
	st, err := iavl.LoadStore(dbm.NewMemDB(), log.NewNopLogger(), storetypes.NewKVStoreKey(pow.StoreKey), storetypes.CommitID{}, iavl.DefaultIAVLCacheSize, false, metrics.NewNoOpMetrics())
	require.NoError(t, err)
	prefix := append(append([]byte{}, pow.KeySeatWorkPrefix...), sdk.Uint64ToBigEndian(275)...)
	other := append(append([]byte{}, pow.KeySeatWorkPrefix...), sdk.Uint64ToBigEndian(276)...)
	a, b := sdk.AccAddress("seat_status_miner_a_"), sdk.AccAddress("seat_status_miner_b_")
	set := func(key []byte, n int64) {
		bz, err := math.NewInt(n).Marshal()
		require.NoError(t, err)
		st.Set(key, bz)
	}
	set(append(append([]byte{}, prefix...), a...), 1_000)
	set(append(append([]byte{}, prefix...), b...), 285_960)
	set(append(append([]byte{}, other...), a...), 7) // another epoch
	st.Commit()

	res, err := st.(*iavl.Store).Query(&storetypes.RequestQuery{Path: "/subspace", Data: prefix})
	require.NoError(t, err)
	pairs, err := decodeKVPairs(res.Value)
	require.NoError(t, err)
	require.Len(t, pairs, 2)
	got := map[string]string{}
	for _, p := range pairs {
		var n math.Int
		require.NoError(t, n.Unmarshal(p[1]))
		got[sdk.AccAddress(p[0][len(prefix):]).String()] = n.String()
	}
	require.Equal(t, map[string]string{a.String(): "1000", b.String(): "285960"}, got)

	empty, err := st.(*iavl.Store).Query(&storetypes.RequestQuery{Path: "/subspace", Data: append(append([]byte{}, pow.KeySeatWorkPrefix...), sdk.Uint64ToBigEndian(9)...)})
	require.NoError(t, err)
	pairs, err = decodeKVPairs(empty.Value)
	require.NoError(t, err)
	require.Empty(t, pairs)
}

func TestDecodeKVPairs_SkipsUnknownAndRefusesTruncated(t *testing.T) {
	var pair []byte
	pair = protowire.AppendTag(pair, 1, protowire.BytesType)
	pair = protowire.AppendBytes(pair, []byte("k"))
	pair = protowire.AppendTag(pair, 9, protowire.VarintType) // unknown
	pair = protowire.AppendVarint(pair, 5)
	pair = protowire.AppendTag(pair, 2, protowire.BytesType)
	pair = protowire.AppendBytes(pair, []byte("v"))
	var bz []byte
	bz = protowire.AppendTag(bz, 3, protowire.VarintType) // unknown
	bz = protowire.AppendVarint(bz, 1)
	bz = protowire.AppendTag(bz, 1, protowire.BytesType)
	bz = protowire.AppendBytes(bz, pair)

	pairs, err := decodeKVPairs(bz)
	require.NoError(t, err)
	require.Equal(t, [][2][]byte{{[]byte("k"), []byte("v")}}, pairs)

	_, err = decodeKVPairs(bz[:len(bz)-1])
	require.Error(t, err)
}

// The 1% minimum counts against all the window's work, as x/pow does.
func TestQualifies(t *testing.T) {
	total := math.NewInt(10_000)
	require.True(t, qualifies(math.NewInt(100), total))
	require.False(t, qualifies(math.NewInt(99), total))
	require.False(t, qualifies(math.ZeroInt(), total))
	require.Equal(t, "0.250000000000000000", share(math.NewInt(25), math.NewInt(100)))
	require.Empty(t, share(math.NewInt(1), math.ZeroInt()))
}

func TestPowerOf(t *testing.T) {
	pub := cometed25519.GenPrivKey().PubKey().Bytes()
	powers := map[string]int64{string(cometed25519.PubKey(pub).Address()): 42_000_000}
	require.Equal(t, int64(42_000_000), powerOf(powers, pub))
	require.Zero(t, powerOf(powers, cometed25519.GenPrivKey().PubKey().Bytes()))
	require.Zero(t, powerOf(powers, nil), "no consensus key")
}
