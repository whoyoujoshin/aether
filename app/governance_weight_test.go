package app

import (
	"fmt"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/x/accountauth"
	"github.com/whoyoujoshin/aether/x/governance"
	"github.com/whoyoujoshin/aether/x/pow"
)

// GHSA-3cvf on a fresh chain with the real x/pow keeper: seats that just
// entered can't pass a treasury spend the established validators didn't
// vote for, however many there are; the established ones still can.
func TestGovernance_NewSeatsCantOutvoteByHead(t *testing.T) {
	w := newMoneyWorld(t)
	h := w.app.MsgServiceRouter()
	run := func(ctx sdk.Context, msg sdk.Msg) error {
		_, err := h.Handler(msg)(ctx, msg)
		return err
	}

	setup := func(t *testing.T) (sdk.Context, []sdk.AccAddress) {
		t.Helper()
		ctx, _ := w.base.CacheContext()
		// A month on, the four validators have finished the tenure ramp.
		ctx = ctx.WithBlockHeight(ctx.BlockHeight() + 1).WithBlockTime(ctx.BlockTime().Add(31 * 24 * time.Hour))
		if err := w.app.PowKeeper.DistributeReward(ctx, w.people[0], sdkmath.NewInt(50_000_000)); err != nil {
			t.Fatal(err)
		}
		// 13 seats enter now: 13 of 17 heads (the report's numbers), over
		// the old head-count quorum of 11.
		var attackers []sdk.AccAddress
		for i := 0; i < 13; i++ {
			a := sdk.AccAddress(fmt.Sprintf("gov_sybil_seat_%05d", i))
			w.app.PowKeeper.SetActiveValidator(ctx, a)
			attackers = append(attackers, a)
		}
		// A one-minute vote, so the new seats are counted moments after
		// entering, as in the report. (They hold the same flat power as the
		// established four here; the seat rule would give them far less.)
		w.app.GovernanceKeeper.SetVotingPeriod(ctx, 60)
		if n := len(w.app.PowKeeper.IterateActiveValidators(ctx)); n != 17 {
			t.Fatalf("%d active validators, want 17", n)
		}
		if err := run(ctx, &governance.MsgSubmitProposal{Proposer: w.people[0].String(), Recipient: w.people[3].String(), Amount: "1000", Deposit: "25000000"}); err != nil {
			t.Fatal(err)
		}
		return ctx, attackers
	}
	resolve := func(t *testing.T, ctx sdk.Context) governance.ProposalStatus {
		t.Helper()
		ctx = ctx.WithBlockHeight(ctx.BlockHeight() + 1).WithBlockTime(ctx.BlockTime().Add(2 * time.Minute))
		if _, err := w.app.EndBlocker(ctx); err != nil {
			t.Fatal(err)
		}
		if err := w.app.CheckInvariants(ctx); err != nil {
			t.Fatal(err)
		}
		p, _ := w.app.GovernanceKeeper.GetProposal(ctx, 1)
		return p.Status
	}

	t.Run("the new seats alone fail quorum", func(t *testing.T) {
		ctx, attackers := setup(t)
		for _, a := range attackers {
			if err := run(ctx, &governance.MsgVote{ProposalId: 1, Voter: a.String(), Option: governance.VoteOption_VOTE_OPTION_YES}); err != nil {
				t.Fatal(err)
			}
		}
		if got := resolve(t, ctx); got != governance.ProposalStatus_PROPOSAL_STATUS_FAILED_QUORUM {
			t.Fatalf("status %s, want FAILED_QUORUM", got)
		}
	})

	t.Run("the established validators still pass it", func(t *testing.T) {
		ctx, _ := setup(t)
		for _, p := range w.people {
			if err := run(ctx, &governance.MsgVote{ProposalId: 1, Voter: p.String(), Option: governance.VoteOption_VOTE_OPTION_YES}); err != nil {
				t.Fatal(err)
			}
		}
		if got := resolve(t, ctx); got != governance.ProposalStatus_PROPOSAL_STATUS_PASSED {
			t.Fatalf("status %s, want PASSED", got)
		}
	})

	t.Run("an account that isn't a validator can't vote", func(t *testing.T) {
		ctx, _ := setup(t)
		err := run(ctx, &governance.MsgVote{ProposalId: 1, Voter: sdk.AccAddress("gov_not_a_validator_").String(), Option: governance.VoteOption_VOTE_OPTION_YES})
		if err == nil {
			t.Fatal("a vote from an account outside the set was accepted")
		}
	})
}

// The testnet height comes after the session-key upgrade and sits
// midway between seat boundaries (every 1,440 blocks).
func TestGovernance_PowerWeightedHeight(t *testing.T) {
	h := governance.PowerWeightedVotingHeight
	if h <= accountauth.SessionKeyCapHeight {
		t.Fatalf("height %d is not after the session-key cap (%d)", h, accountauth.SessionKeyCapHeight)
	}
	if epoch := pow.DefaultGenesisState().Params.EpochLength; h%epoch != epoch/2 {
		t.Fatalf("height %d is %d blocks into an epoch, want %d", h, h%epoch, epoch/2)
	}
}
