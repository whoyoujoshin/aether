package wallet_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/wallet"
	"github.com/whoyoujoshin/aether/x/pow"
)

// x/pow's EndBlock selects at the height where (height+1) % length == 0,
// and CurrentEpoch is height / length: the window must agree with both.
func TestEpochWindow(t *testing.T) {
	cases := []struct {
		height, length          int64
		index, start, selection int64
	}{
		{0, 1440, 0, 0, 1439},
		{1439, 1440, 0, 0, 1439},
		{1440, 1440, 1, 1440, 2879},
		{122_000, 1440, 84, 120_960, 122_399},
		{7, 0, 7, 7, 7}, // x/pow treats a non-positive length as 1
	}
	for _, c := range cases {
		index, start, selection := wallet.EpochWindow(c.height, c.length)
		require.Equal(t, c.index, index, "index at %d", c.height)
		require.Equal(t, c.start, start, "start at %d", c.height)
		require.Equal(t, c.selection, selection, "selection at %d", c.height)
		length := c.length
		if length <= 0 {
			length = 1
		}
		require.Zero(t, (selection+1)%length, "selection height must be where EndBlock selects")
	}
}

func TestSelectionRuleAt(t *testing.T) {
	require.Equal(t, wallet.SelectionTopKByWork, wallet.SelectionRuleAt(pow.RandomnessBeaconActivationHeight-1))
	require.Equal(t, wallet.SelectionBeaconWeighted, wallet.SelectionRuleAt(pow.RandomnessBeaconActivationHeight))
}
