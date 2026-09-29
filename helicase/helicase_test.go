package helicase

import (
	"testing"
	"time"

	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	channeltypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	"github.com/stretchr/testify/require"
)

func TestRelayTxsSkipsStaleBatch(t *testing.T) {
	w := &Worker{batch: [][]byte{[]byte("tx")}, batchTip: 10}
	require.NotNil(t, w.RelayTxs(11), "computed after the latest block")
	require.NotNil(t, w.RelayTxs(12), "one block behind")
	require.Nil(t, w.RelayTxs(13), "two blocks behind")
}

func TestTimedOut(t *testing.T) {
	at := time.Unix(1_000, 0)
	byHeight := channeltypes.Packet{TimeoutHeight: clienttypes.NewHeight(1, 50)}
	require.False(t, timedOut(byHeight, 49, at))
	require.True(t, timedOut(byHeight, 50, at))

	byTime := channeltypes.Packet{TimeoutTimestamp: uint64(at.UnixNano())}
	require.False(t, timedOut(byTime, 1_000_000, at.Add(-time.Nanosecond)))
	require.True(t, timedOut(byTime, 1, at))
}
