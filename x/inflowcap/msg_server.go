package inflowcap

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

type msgServer struct {
	Keeper
}

func NewMsgServerImpl(keeper Keeper) MsgServer {
	return &msgServer{Keeper: keeper}
}

// UpdateParams replaces the allowlist. Only the governance module
// account can sign it, so it runs only as a param-change proposal's
// execute_msg.
func (k msgServer) UpdateParams(goCtx context.Context, msg *MsgUpdateParams) (*MsgUpdateParamsResponse, error) {
	if msg.Authority != k.GetAuthority() {
		return nil, ErrInvalidAuthority.Wrapf("expected %s, got %s", k.GetAuthority(), msg.Authority)
	}
	if err := msg.Params.Validate(); err != nil {
		return nil, ErrInvalidParams.Wrap(err.Error())
	}
	k.SetParams(sdk.UnwrapSDKContext(goCtx), msg.Params)
	return &MsgUpdateParamsResponse{}, nil
}
