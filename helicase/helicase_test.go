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
