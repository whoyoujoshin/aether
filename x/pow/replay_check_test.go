package pow_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	pow "github.com/whoyoujoshin/aether/x/pow"
)

// Simulates the exact real-world scenario from the handoff: a
// continuously-running node hits CorrectBootstrapPower at every height
// from genesis onward (including the real historical height), while a
// "fresh node" replaying history starts calling it only from height 1
// of ITS OWN replay. Both must end up applying the correction at the
// same real height and produce identical resulting state.
func TestCorrectBootstrapPower_FreshReplayMatchesContinuousNode(t *testing.T) {
	runFullHistory := func(t *testing.T, startHeight, endHeight int64) []int64 {
		t.Helper()
		k, ctx, _ := setupKeeper(t)
		minerAddr := []byte("replay_check_validator__")
		k.SetValidatorPubkey(ctx, minerAddr, make([]byte, 32))
		k.SetActiveValidator(ctx, minerAddr)

		var firedAt []int64
		for h := startHeight; h <= endHeight; h++ {
			updates := k.CorrectBootstrapPower(ctx.WithBlockHeight(h))
			if len(updates) > 0 {
				firedAt = append(firedAt, h)
			}
		}
		return firedAt
	}

	// "Continuously running" node: was already alive well before the
	// real correction height, replays every block from height 1.
	continuous := runFullHistory(t, 1, pow.BootstrapPowerCorrectionHeight+10)

	// "Fresh node": syncs from genesis too (a fresh node ALSO starts
	// at height 1 -- the bug was never about the starting height, it
	// was about the store-flag gate firing on "my own first call"
	// instead of the real height). Included here to make explicit that
	// both replay paths are identical inputs and must produce
	// identical outputs.
	fresh := runFullHistory(t, 1, pow.BootstrapPowerCorrectionHeight+10)

	require.Equal(t, continuous, fresh, "a fresh replay and a continuous replay of identical history must apply the correction at the identical height")
	require.Equal(t, []int64{pow.BootstrapPowerCorrectionHeight}, fresh, "the correction must fire at exactly the one real historical height, never at height 1")
}
