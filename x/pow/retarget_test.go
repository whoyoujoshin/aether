package pow

import (
	stdmath "math"
	"math/big"
	"math/rand"
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"
)

func TestAsertDifficulty_Exact(t *testing.T) {
	d := math.NewInt(1_000_000)
	// On schedule: unchanged.
	require.Equal(t, d, asertDifficulty(d, 60, 60, 1800))
	// One half-life late: half.
	require.Equal(t, math.NewInt(500_000), asertDifficulty(d, 60, 60+1800, 1800))
	// One half-life early: double (target 60, half-life 30, share at 30 s).
	require.Equal(t, math.NewInt(2_000_000), asertDifficulty(d, 60, 30, 30))
	// Two half-lives late: a quarter.
	require.Equal(t, math.NewInt(250_000), asertDifficulty(d, 60, 60+2*1800, 1800))
}

func TestAsertDifficulty_MatchesExponential(t *testing.T) {
	d := math.NewInt(21_000_000)
	for elapsed := int64(0); elapsed <= 30_000; elapsed += 7 {
		got := new(big.Float).SetInt(asertDifficulty(d, 60, elapsed, DifficultyHalfLife).BigInt())
		want := 21_000_000 * stdmath.Exp2(float64(60-elapsed)/float64(DifficultyHalfLife))
		if want < 1 {
			want = 1
		}
		gf, _ := got.Float64()
		require.InEpsilon(t, want, gf, 0.0002, "elapsed %d", elapsed)
	}
}

func TestAsertDifficulty_MonotonicAndBounded(t *testing.T) {
	d := math.NewInt(7_278)
	prev := asertDifficulty(d, 60, -100, DifficultyHalfLife)
	// A negative elapsed counts as zero.
	require.Equal(t, asertDifficulty(d, 60, 0, DifficultyHalfLife), prev)
	for elapsed := int64(1); elapsed < 400_000; elapsed += 13 {
		next := asertDifficulty(d, 60, elapsed, DifficultyHalfLife)
		require.True(t, next.LTE(prev), "not monotonic at %d", elapsed)
		require.True(t, next.IsPositive())
		prev = next
	}
	// Absurd gaps and huge difficulties don't overflow or reach zero.
	require.Equal(t, math.OneInt(), asertDifficulty(d, 60, stdmath.MaxInt64/2, DifficultyHalfLife))
	huge := math.NewInt(stdmath.MaxInt64)
	require.True(t, asertDifficulty(huge, 60, 0, DifficultyHalfLife).GT(huge))
}

// The incident this replaces: a share one block (~6 s) after the last.
func TestAsertDifficulty_OneFastShareBarelyMoves(t *testing.T) {
	d := math.NewInt(7_278)
	next := asertDifficulty(d, 60, 6, DifficultyHalfLife)
	require.True(t, next.GT(d))
	require.True(t, next.LT(math.NewInt(7_278*103/100)), "one early share moved difficulty to %s", next)

	// Ten back to back: still under 25%, where the old rule multiplied by
	// ten each time.
	for i := 0; i < 10; i++ {
		d = asertDifficulty(d, 60, 6, DifficultyHalfLife)
	}
	require.True(t, d.LT(math.NewInt(7_278*125/100)), "ten early shares moved difficulty to %s", d)
}

// simulateSeats mines n epochs with four equal miners at the testnet's
// measured single-thread Scrypt rate, one share accepted per block, and
// reports how many (epoch, miner) pairs ended with no work (which on the
// live chain costs that validator its seat) and the difficulty's range.
func simulateSeats(t *testing.T, next func(d int64, elapsed int64) int64, start int64, epochs int, seed int64) (misses int, minD, maxD int64) {
	t.Helper()
	const (
		miners     = 4
		hashRate   = 4_767.0 // hashes/s, x/pow/types.go's calibration
		blockTime  = 7       // s
		epochBlock = 1440
		minDiff    = 1_024
		maxDiff    = 100_000_000
	)
	rng := rand.New(rand.NewSource(seed))
	d, last, now := start, int64(0), int64(0)
	maxD, minD = d, d
	for e := 0; e < epochs; e++ {
		var work [miners]int
		for b := 0; b < epochBlock; b++ {
			now += blockTime
			// Each miner finds a share in this block's interval with the
			// Poisson probability for its rate; the block takes one.
			p := 1 - stdmath.Exp(-hashRate*blockTime/float64(d))
			var found []int
			for m := 0; m < miners; m++ {
				if rng.Float64() < p {
					found = append(found, m)
				}
			}
			if len(found) == 0 {
				continue
			}
			work[found[rng.Intn(len(found))]]++
			d = next(d, now-last)
			if d < minDiff {
				d = minDiff
			}
			if d > maxDiff {
				d = maxDiff
			}
			last = now
			if d > maxD {
				maxD = d
			}
			if d < minD {
				minD = d
			}
		}
		for _, w := range work {
			if w == 0 {
				misses++
			}
		}
	}
	return misses, maxD, minD
}

// Four equal miners settle where shares come one per TargetBlockTime:
// 4 × 4,767 hashes/s × 60 s ≈ 1,144,000. ASERT stays near that; the old
// rule swings to the cap.
func TestAsertDifficulty_StaysNearEquilibrium(t *testing.T) {
	asert := func(d, elapsed int64) int64 {
		return asertDifficulty(math.NewInt(d), 60, elapsed, DifficultyHalfLife).Int64()
	}
	old := func(d, elapsed int64) int64 {
		if elapsed <= 0 {
			return d
		}
		return d * 60 / elapsed
	}
	for seed := int64(1); seed <= 5; seed++ {
		oldMiss, oldMin, oldMax := simulateSeats(t, old, 7_278, 20, seed)
		newMiss, newMin, newMax := simulateSeats(t, asert, 7_278, 20, seed)
		t.Logf("seed %d, 20 epochs from 7,278: old rule %d..%d (%d seats lost), ASERT %d..%d (%d)",
			seed, oldMin, oldMax, oldMiss, newMin, newMax, newMiss)
		require.Zero(t, newMiss)
		require.Less(t, newMax, int64(2_000_000))

		// From the 3 October spike: back near equilibrium within an epoch.
		_, _, spikeMax := simulateSeats(t, asert, 21_000_000, 1, seed)
		require.LessOrEqual(t, spikeMax, int64(21_000_000))
		restMiss, _, restMax := simulateSeats(t, asert, 1_500_000, 19, seed)
		require.Zero(t, restMiss)
		require.Less(t, restMax, int64(2_000_000))
	}
}
