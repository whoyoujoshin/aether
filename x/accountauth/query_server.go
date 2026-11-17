package accountauth

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

func (q queryServer) Authenticators(goCtx context.Context, req *QueryAuthenticatorsRequest) (*QueryAuthenticatorsResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	account, err := sdk.AccAddressFromBech32(req.Account)
	if err != nil {
		return nil, sdkerrors.Wrapf(ErrInvalidAccount, "invalid account address %q: %s", req.Account, err)
	}

	// Authenticators an earlier key registered no longer act
	// (keygen.go), so they aren't listed.
	list := q.Keeper.GetAuthenticators(ctx, account)
	out := make([]*Authenticator, 0, len(list))
	for i := range list {
		if q.Keeper.AuthenticatorCurrent(ctx, account, list[i].Id) {
			out = append(out, &list[i])
		}
	}
	return &QueryAuthenticatorsResponse{Authenticators: out}, nil
}
