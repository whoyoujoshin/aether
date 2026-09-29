package escrow

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

func (k msgServer) CreateEscrow(goCtx context.Context, msg *MsgCreateEscrow) (*MsgCreateEscrowResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	e := Escrow{
		Payer:     msg.Payer,
		Payee:     msg.Payee,
		Arbiter:   msg.Arbiter,
		Amount:    msg.Amount,
		ExpiresAt: msg.ExpiresAt,
		OnExpiry:  msg.OnExpiry,
		Terms:     msg.Terms,
	}
	if err := e.validate(); err != nil {
		return nil, ErrInvalidEscrow.Wrap(err.Error())
	}
	now := ctx.BlockTime()
	if earliest := now.Add(MinDuration).Unix(); msg.ExpiresAt < earliest {
		return nil, ErrInvalidDeadline.Wrapf("expires_at %d is less than %s after this block's time %d", msg.ExpiresAt, MinDuration, now.Unix())
	}
	if latest := now.Add(MaxDuration).Unix(); msg.ExpiresAt > latest {
		return nil, ErrInvalidDeadline.Wrapf("expires_at %d is more than %s after this block's time %d", msg.ExpiresAt, MaxDuration, now.Unix())
	}
	id, err := k.create(ctx, e)
	if err != nil {
		return nil, err
	}
	return &MsgCreateEscrowResponse{Id: id}, nil
}

// ReleaseEscrow pays the payee: the payer says the work is done, or the
// arbiter rules for the payee.
func (k msgServer) ReleaseEscrow(goCtx context.Context, msg *MsgReleaseEscrow) (*MsgReleaseEscrowResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	e, err := k.settleable(ctx, msg.Sender, msg.Id)
	if err != nil {
		return nil, err
	}
	if msg.Sender != e.Payer && msg.Sender != e.Arbiter {
		return nil, ErrNotAllowed.Wrapf("only the payer or the arbiter may release escrow %d", e.Id)
	}
	if err := k.settle(ctx, e, true, msg.Sender); err != nil {
		return nil, err
	}
	return &MsgReleaseEscrowResponse{}, nil
}

// RefundEscrow returns the money: the payee declines the job, or the
// arbiter rules for the payer.
func (k msgServer) RefundEscrow(goCtx context.Context, msg *MsgRefundEscrow) (*MsgRefundEscrowResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	e, err := k.settleable(ctx, msg.Sender, msg.Id)
	if err != nil {
		return nil, err
	}
	if msg.Sender != e.Payee && msg.Sender != e.Arbiter {
		return nil, ErrNotAllowed.Wrapf("only the payee or the arbiter may refund escrow %d", e.Id)
	}
	if err := k.settle(ctx, e, false, msg.Sender); err != nil {
		return nil, err
	}
	return &MsgRefundEscrowResponse{}, nil
}

func (k msgServer) settleable(ctx sdk.Context, sender string, id uint64) (Escrow, error) {
	if _, err := sdk.AccAddressFromBech32(sender); err != nil {
		return Escrow{}, ErrInvalidAddress.Wrapf("sender: %s", err)
	}
	e, ok := k.GetEscrow(ctx, id)
	if !ok {
		return Escrow{}, ErrNotFound.Wrapf("escrow %d", id)
	}
	return e, nil
}
