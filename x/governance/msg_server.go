package governance

import (
	"context"

	sdkerrors "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"cosmossdk.io/math"
)

type msgServer struct {
	Keeper
}

func NewMsgServerImpl(keeper Keeper) MsgServer {
	return &msgServer{Keeper: keeper}
}

func (k msgServer) SubmitProposal(goCtx context.Context, msg *MsgSubmitProposal) (*MsgSubmitProposalResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	proposerAddr, err := sdk.AccAddressFromBech32(msg.Proposer)
	if err != nil {
		return nil, sdkerrors.Wrapf(ErrInvalidProposer, "invalid proposer address %q: %s", msg.Proposer, err)
	}
	if _, err := sdk.AccAddressFromBech32(msg.Recipient); err != nil {
		return nil, sdkerrors.Wrapf(ErrInvalidRecipient, "invalid recipient address %q: %s", msg.Recipient, err)
	}

	depositAmount, ok := math.NewIntFromString(msg.Deposit)
	if !ok || depositAmount.IsNegative() {
		return nil, sdkerrors.Wrapf(ErrInvalidDeposit, "invalid deposit amount %q", msg.Deposit)
	}

	proposalID := k.Keeper.NextProposalID(ctx)
	now := ctx.BlockTime().Unix()
	depositPeriod := k.Keeper.GetDepositPeriod(ctx)

	proposal := Proposal{
		Id:             proposalID,
		Recipient:      msg.Recipient,
		Amount:         msg.Amount,
		TotalDeposit:   "0",
		Status:         ProposalStatus_PROPOSAL_STATUS_DEPOSIT_PERIOD,
		SubmitTime:     now,
		DepositEndTime: now + depositPeriod,
	}
	k.Keeper.SetProposal(ctx, proposal)

	if depositAmount.IsPositive() {
		if err := k.Keeper.addDeposit(ctx, proposalID, proposerAddr, depositAmount); err != nil {
			return nil, err
		}
	}

	return &MsgSubmitProposalResponse{ProposalId: proposalID}, nil
}

func (k msgServer) Deposit(goCtx context.Context, msg *MsgDeposit) (*MsgDepositResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	depositorAddr, err := sdk.AccAddressFromBech32(msg.Depositor)
	if err != nil {
		return nil, sdkerrors.Wrapf(ErrInvalidProposer, "invalid depositor address %q: %s", msg.Depositor, err)
	}

	proposal, ok := k.Keeper.GetProposal(ctx, msg.ProposalId)
	if !ok {
		return nil, sdkerrors.Wrapf(ErrProposalNotFound, "no proposal with id %d", msg.ProposalId)
	}
	if proposal.Status != ProposalStatus_PROPOSAL_STATUS_DEPOSIT_PERIOD {
		return nil, sdkerrors.Wrapf(ErrDepositPeriodEnded, "proposal %d is not in its deposit period", msg.ProposalId)
	}

	amount, ok := math.NewIntFromString(msg.Amount)
	if !ok || !amount.IsPositive() {
		return nil, sdkerrors.Wrapf(ErrInvalidDeposit, "invalid deposit amount %q", msg.Amount)
	}

	if err := k.Keeper.addDeposit(ctx, msg.ProposalId, depositorAddr, amount); err != nil {
		return nil, err
	}

	return &MsgDepositResponse{}, nil
}

func (k msgServer) Vote(goCtx context.Context, msg *MsgVote) (*MsgVoteResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	voterAddr, err := sdk.AccAddressFromBech32(msg.Voter)
	if err != nil {
		return nil, sdkerrors.Wrapf(ErrInvalidProposer, "invalid voter address %q: %s", msg.Voter, err)
	}

	proposal, ok := k.Keeper.GetProposal(ctx, msg.ProposalId)
	if !ok {
		return nil, sdkerrors.Wrapf(ErrProposalNotFound, "no proposal with id %d", msg.ProposalId)
	}
	if proposal.Status != ProposalStatus_PROPOSAL_STATUS_VOTING_PERIOD {
		return nil, sdkerrors.Wrapf(ErrNotInVotingPeriod, "proposal %d is not in its voting period", msg.ProposalId)
	}

	if msg.Option == VoteOption_VOTE_OPTION_UNSPECIFIED {
		return nil, sdkerrors.Wrapf(ErrInvalidVoteOption, "vote option must be specified")
	}

	// Voting power (tenure ratio) is locked in at the moment of casting,
	// not recomputed at tally time -- see design discussion. This is
	// separate from active-validator STATUS, which IS re-checked at tally
	// time: if this voter has since fallen out of the active set (or been
	// slashed), their vote counts for nothing at tally, regardless of
	// what weight was recorded here.
	weight := k.Keeper.powKeeper.GetValidatorTenureRatio(ctx, voterAddr)

	vote := Vote{
		ProposalId: msg.ProposalId,
		Voter:      msg.Voter,
		Option:     msg.Option,
		Weight:     weight.String(),
	}
	k.Keeper.SetVote(ctx, vote)

	return &MsgVoteResponse{}, nil
}