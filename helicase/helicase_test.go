package helicase

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRelayTxsSkipsStaleBatch(t *testing.T) {
	w := &Worker{batch: [][]byte{[]byte("tx")}, batchTip: 10}
	require.NotNil(t, w.RelayTxs(11), "computed after the latest block")
	require.NotNil(t, w.RelayTxs(12), "one block behind")
	require.Nil(t, w.RelayTxs(13), "two blocks behind")
}

func TestSourcesShareABlock(t *testing.T) {
	a := &Worker{batch: [][]byte{[]byte("osmo-update"), []byte("osmo-packet")}, batchTip: 10}
	b := &Worker{batch: [][]byte{[]byte("inj-update")}, batchTip: 10}
	stale := &Worker{batch: [][]byte{[]byte("old")}, batchTip: 5}
	require.Equal(t, [][]byte{[]byte("osmo-update"), []byte("osmo-packet"), []byte("inj-update")}, Sources{a, b, stale}.RelayTxs(11))

	require.Equal(t, maxMsgsPerCycle, Split(1))
	require.Equal(t, maxMsgsPerCycle/2, Split(2))
	// Every path's messages plus its client update fit one block.
	for n := 1; n <= 10; n++ {
		require.LessOrEqual(t, n*(Split(n)+1), 100, "n=%d", n)
	}
}
