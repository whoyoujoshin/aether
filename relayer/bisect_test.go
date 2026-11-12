package relayer

import (
	"testing"
	"time"

	cmtmath "github.com/cometbft/cometbft/libs/math"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/stretchr/testify/require"
)

// signedBy makes a commit for height h signed by every key in privs.
func signedBy(t *testing.T, chainID string, h int64, set *cmttypes.ValidatorSet, privs []cmttypes.PrivValidator) *cmttypes.Commit {
	t.Helper()
	blockID := cmttypes.BlockID{Hash: make([]byte, 32), PartSetHeader: cmttypes.PartSetHeader{Total: 1, Hash: make([]byte, 32)}}
	voteSet := cmttypes.NewVoteSet(chainID, h, 0, cmtproto.PrecommitType, set)
	ext, err := cmttypes.MakeExtCommit(blockID, h, 0, voteSet, privs, time.Now(), false)
	require.NoError(t, err)
	return ext.ToCommit()
}

// The trust check is the one 07-tendermint applies: a third of the
// trusted set's power must have signed.
func TestTrustedSigned(t *testing.T) {
	const chainID = "injective-888"
	third := cmtmath.Fraction{Numerator: 1, Denominator: 3}
	old, oldPrivs := cmttypes.RandValidatorSet(6, 10)

	commit := signedBy(t, chainID, 100, old, oldPrivs)
	require.True(t, trustedSigned(chainID, old, commit, third), "the same set")

	other, otherPrivs := cmttypes.RandValidatorSet(6, 10)
	commit = signedBy(t, chainID, 100, other, otherPrivs)
	require.False(t, trustedSigned(chainID, old, commit, third), "a set that has changed completely")

	// A new set keeping some of the old validators: three of six (half
	// the old power) vouch; one of six doesn't.
	mixed := func(keep int) bool {
		vals := make([]*cmttypes.Validator, 0, 6)
		privs := make([]cmttypes.PrivValidator, 0, 6)
		for i := 0; i < keep; i++ {
			vals = append(vals, old.Validators[i].Copy())
			privs = append(privs, privFor(t, old.Validators[i], oldPrivs))
		}
		for i := keep; i < 6; i++ {
			vals = append(vals, other.Validators[i].Copy())
			privs = append(privs, privFor(t, other.Validators[i], otherPrivs))
		}
		set := cmttypes.NewValidatorSet(vals)
		ordered := make([]cmttypes.PrivValidator, len(set.Validators))
		for i, v := range set.Validators {
			ordered[i] = privFor(t, v, privs)
		}
		return trustedSigned(chainID, old, signedBy(t, chainID, 100, set, ordered), third)
	}
	require.True(t, mixed(3))
	require.False(t, mixed(1))

	require.False(t, trustedSigned("another-chain", old, signedBy(t, chainID, 100, old, oldPrivs), third), "signatures are bound to the chain ID")
}

func privFor(t *testing.T, v *cmttypes.Validator, privs []cmttypes.PrivValidator) cmttypes.PrivValidator {
	t.Helper()
	for _, p := range privs {
		pk, err := p.GetPubKey()
		require.NoError(t, err)
		if pk.Address().String() == v.Address.String() {
			return p
		}
	}
	t.Fatalf("no key for %s", v.Address)
	return nil
}

// Halving towards the trusted height ends at a height the trusted set
// vouches for, never below trusted+1, and asks only O(log n) heights.
func TestPickUpdateHeight(t *testing.T) {
	// Injective's case: trusted at 147,013,626, tip 148,124,478, and
	// the set the client trusts stopped vouching somewhere in between.
	const trusted, tip = int64(147_013_626), int64(148_124_478)
	for _, lastGood := range []int64{trusted + 1, trusted + 2, 147_200_000, 147_600_000, tip - 1, tip} {
		asked := 0
		h, err := pickUpdateHeight(trusted, tip, func(h int64) (bool, error) {
			asked++
			return h <= lastGood, nil
		})
		require.NoError(t, err)
		require.Greater(t, h, trusted)
		require.LessOrEqual(t, h, lastGood, "a height the trusted set signed")
		require.LessOrEqual(t, asked, 25)
		if lastGood == tip {
			require.Equal(t, tip, h, "the tip when it can")
			require.Equal(t, 1, asked)
		}
	}

	// It walks the whole way in a few cycles: each update becomes the
	// next trusted height, with the set changing every 200,000 blocks.
	at, cycles := trusted, 0
	for at < tip {
		from := at
		h, err := pickUpdateHeight(from, tip, func(h int64) (bool, error) { return h-from <= 200_000, nil })
		require.NoError(t, err)
		require.Greater(t, h, at)
		at, cycles = h, cycles+1
	}
	require.LessOrEqual(t, cycles, 12)

	_, err := pickUpdateHeight(10, 20, func(int64) (bool, error) { return false, nil })
	require.Error(t, err, "nothing signed by the trusted set, not even the next block")
}
