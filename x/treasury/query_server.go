package treasury

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

func (q queryServer) Balance(goCtx context.Context, req *QueryBalanceRequest) (*QueryBalanceResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	tracked := q.Keeper.GetTreasuryBalance(ctx)
	real := q.Keeper.GetRealBankBalance(ctx)
	return &QueryBalanceResponse{
		TrackedBalance:           tracked.String(),
		RealBankBalance:          real.String(),
		LedgerMatchesBankBalance: tracked.Equal(real),
	}, nil
}
