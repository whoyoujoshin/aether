package escrow

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type queryServer struct {
	Keeper
}

func NewQueryServerImpl(keeper Keeper) QueryServer {
	return &queryServer{Keeper: keeper}
}

func (q queryServer) Escrow(goCtx context.Context, req *QueryEscrowRequest) (*QueryEscrowResponse, error) {
	e, ok := q.GetEscrow(sdk.UnwrapSDKContext(goCtx), req.Id)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "escrow %d is not open: it was settled, or never existed", req.Id)
	}
	return &QueryEscrowResponse{Escrow: e}, nil
}

func (q queryServer) EscrowsByAddress(goCtx context.Context, req *QueryEscrowsByAddressRequest) (*QueryEscrowsByAddressResponse, error) {
	addr, err := sdk.AccAddressFromBech32(req.Address)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "address: %v", err)
	}
	limit := int(req.Limit)
	if limit <= 0 || limit > MaxPageSize {
		limit = MaxPageSize
	}
	list, next := q.OpenEscrowsOf(sdk.UnwrapSDKContext(goCtx), addr, req.AfterId, limit)
	return &QueryEscrowsByAddressResponse{Escrows: list, NextAfterId: next}, nil
}
