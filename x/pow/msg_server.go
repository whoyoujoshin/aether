package pow

import (
	"context"
)

type msgServer struct {
	Keeper
}

func NewMsgServerImpl(keeper Keeper) MsgServer {
	return &msgServer{Keeper: keeper}
}

func (k msgServer) SubmitPoW(goCtx context.Context, msg *MsgSubmitPoW) (*MsgSubmitPoWResponse, error) {
	// TODO: implement actual verification + reward logic
	return &MsgSubmitPoWResponse{}, nil
}