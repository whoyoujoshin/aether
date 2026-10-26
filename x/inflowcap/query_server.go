package inflowcap

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

func (q queryServer) Params(goCtx context.Context, _ *QueryParamsRequest) (*QueryParamsResponse, error) {
	p := q.GetParams(sdk.UnwrapSDKContext(goCtx))
	if p.Entries == nil {
		p.Entries = []Entry{}
	}
	return &QueryParamsResponse{Params: p}, nil
}

func (q queryServer) Caps(goCtx context.Context, _ *QueryCapsRequest) (*QueryCapsResponse, error) {
	return &QueryCapsResponse{Caps: q.Keeper.Caps(sdk.UnwrapSDKContext(goCtx))}, nil
}
