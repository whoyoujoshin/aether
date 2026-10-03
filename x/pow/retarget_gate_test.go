package pow_test

import (
	"testing"
	"time"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	pow "github.com/whoyoujoshin/aether/x/pow"
)

// Below SmoothRetargetActivationHeight a share one block after the last
// still rescales difficulty by the whole ratio, as every node replaying
// history computed it; from the height it moves by about 1%.
func TestAdjustDifficulty_SmoothRetargetGate(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	last := time.Unix(1_900_000_000, 0)
	k.SetDifficulty(ctx, math.NewInt(7_278))
	k.SetLastBlockTime(ctx, last.Unix())
	ctx = ctx.WithBlockTime(last.Add(6 * time.Second))

	before := k.AdjustDifficulty(ctx.WithBlockHeight(pow.SmoothRetargetActivationHeight - 1))
	require.Equal(t, math.NewInt(7_278*60/6), before)

	at := k.AdjustDifficulty(ctx.WithBlockHeight(pow.SmoothRetargetActivationHeight))
	require.True(t, at.GT(math.NewInt(7_278)) && at.LT(math.NewInt(7_278*102/100)), "got %s", at)

	// The chain's bounds still apply.
	k.SetDifficulty(ctx, k.GetMinDifficulty(ctx))
	slow := ctx.WithBlockHeight(pow.SmoothRetargetActivationHeight).WithBlockTime(last.Add(48 * time.Hour))
	require.Equal(t, k.GetMinDifficulty(ctx), k.AdjustDifficulty(slow))
	k.SetDifficulty(ctx, k.GetMaxDifficulty(ctx))
	fast := ctx.WithBlockHeight(pow.SmoothRetargetActivationHeight).WithBlockTime(last.Add(time.Second))
	require.Equal(t, k.GetMaxDifficulty(ctx), k.AdjustDifficulty(fast))
}
