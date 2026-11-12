package relayer

import (
	"context"
	"fmt"

	cmtmath "github.com/cometbft/cometbft/libs/math"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
)

// A 07-tendermint client accepts a header that skips ahead only if
// validators holding its trust level (1/3 by default) of the trusted
// height's set signed it. When the other chain's validators have changed
// by more than that since the client's last update, a header at the tip
// is refused ("insufficient voting power") on every try, and the client
// stays where it is until it expires: Aether's client of Injective
// (07-tendermint-2) after 1.1 million Injective blocks without an update,
// at block 712,080 (2026-11-11). So an update goes to the highest height
// the trusted set can still vouch for, found by halving the distance as a
// light client does, and the next cycle carries on from there.

// trustedSigned reports whether trustLevel of trusted signed commit.
func trustedSigned(chainID string, trusted *cmttypes.ValidatorSet, commit *cmttypes.Commit, trustLevel cmtmath.Fraction) bool {
	return trusted.VerifyCommitLightTrusting(chainID, commit, trustLevel) == nil
}

// pickUpdateHeight returns the height to update a client trusted at
// trusted to, given the other chain's tip: the tip if signedOK accepts it,
// otherwise the first of tip's halfway points down towards trusted that it
// accepts. trusted+1 is always acceptable (the trusted set signed it), so
// it ends there at worst.
func pickUpdateHeight(trusted, tip int64, signedOK func(h int64) (bool, error)) (int64, error) {
	lo, h := trusted, tip
	for h > lo {
		ok, err := signedOK(h)
		if err != nil {
			return 0, err
		}
		if ok {
			return h, nil
		}
		next := lo + (h-lo)/2
		if next == lo && h > lo+1 {
			next = lo + 1
		}
		if next >= h {
			break
		}
		h = next
	}
	return 0, fmt.Errorf("no height after %d that the trusted validators signed", trusted)
}

// commitAt is src's commit for height h.
func commitAt(c *Chain, h int64) (*cmttypes.Commit, error) {
	node, err := c.ClientCtx.GetNode()
	if err != nil {
		return nil, err
	}
	res, err := node.Commit(context.Background(), &h)
	if err != nil {
		return nil, fmt.Errorf("%s: commit at %d: %w", c.Name, h, err)
	}
	return res.SignedHeader.Commit, nil
}

func valSetFromProto(p *cmtproto.ValidatorSet) (*cmttypes.ValidatorSet, error) {
	return cmttypes.ValidatorSetFromProto(p)
}
