package governance

import (
	"context"

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