package governance

import (
	"context"

	sdkerrors "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

type queryServer struct {
	Keeper
}

func NewQueryServerImpl(keeper Keeper) QueryServer {
	return &queryServer{Keeper: keeper}
}

func (q queryServer) Proposal(goCtx context.Context, req *QueryProposalRequest) (*QueryProposalResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	proposal, found := q.Keeper.GetProposal(ctx, req.ProposalId)
	if !found {
		return &QueryProposalResponse{}, nil
	}
	return &QueryProposalResponse{Proposal: &proposal}, nil
}

func (q queryServer) Proposals(goCtx context.Context, req *QueryProposalsRequest) (*QueryProposalsResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	proposals := q.Keeper.IterateProposals(ctx)
	result := make([]*Proposal, len(proposals))
	for i := range proposals {
		result[i] = &proposals[i]
	}
	return &QueryProposalsResponse{Proposals: result}, nil
}

func (q queryServer) Params(goCtx context.Context, req *QueryParamsRequest) (*QueryParamsResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	return &QueryParamsResponse{
		MinDeposit:    q.Keeper.GetMinDeposit(ctx),
		DepositPeriod: q.Keeper.GetDepositPeriod(ctx),
		VotingPeriod:  q.Keeper.GetVotingPeriod(ctx),
	}, nil
}

func (q queryServer) Vote(goCtx context.Context, req *QueryVoteRequest) (*QueryVoteResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	voterAddr, err := sdk.AccAddressFromBech32(req.Voter)
	if err != nil {
		return nil, sdkerrors.Wrapf(ErrInvalidProposer, "invalid voter address %q: %s", req.Voter, err)
	}
	vote, found := q.Keeper.GetVote(ctx, req.ProposalId, voterAddr)
	if !found {
		return &QueryVoteResponse{}, nil
	}
	return &QueryVoteResponse{Vote: &vote}, nil
}

func (q queryServer) Votes(goCtx context.Context, req *QueryVotesRequest) (*QueryVotesResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	votes := q.Keeper.IterateVotes(ctx, req.ProposalId)
	result := make([]*Vote, len(votes))
	for i := range votes {
		result[i] = &votes[i]
	}
	return &QueryVotesResponse{Votes: result}, nil
}

func (q queryServer) Tally(goCtx context.Context, req *QueryTallyRequest) (*QueryTallyResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	result := q.Keeper.TallyVotes(ctx, req.ProposalId)
	return &QueryTallyResponse{
		ValidVoterCount: result.ValidVoterCount,
		YesPower:        result.YesPower.String(),
		NoPower:         result.NoPower.String(),
		AbstainPower:    result.AbstainPower.String(),
		VetoPower:       result.VetoPower.String(),
		QuorumThreshold: q.Keeper.computeQuorumThreshold(ctx),
	}, nil
}