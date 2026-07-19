package pow

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

func (q queryServer) Difficulty(goCtx context.Context, req *QueryDifficultyRequest) (*QueryDifficultyResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	return &QueryDifficultyResponse{
		Difficulty: q.Keeper.GetDifficulty(ctx).String(),
	}, nil
}

func (q queryServer) BlockReward(goCtx context.Context, req *QueryBlockRewardRequest) (*QueryBlockRewardResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	return &QueryBlockRewardResponse{
		BlockReward: q.Keeper.GetBlockReward(ctx).String(),
	}, nil
}

func (q queryServer) Escrow(goCtx context.Context, req *QueryEscrowRequest) (*QueryEscrowResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	minerAddr, err := sdk.AccAddressFromBech32(req.Miner)
	if err != nil {
		return nil, err
	}

	balance := q.Keeper.GetEscrowBalance(ctx, minerAddr)
	unlockHeight, hasPending := q.Keeper.GetEscrowUnlockHeight(ctx, minerAddr)

	return &QueryEscrowResponse{
		Balance:          balance.String(),
		UnlockHeight:     unlockHeight,
		HasPendingEscrow: hasPending,
	}, nil
}

func (q queryServer) BanStatus(goCtx context.Context, req *QueryBanStatusRequest) (*QueryBanStatusResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	minerAddr, err := sdk.AccAddressFromBech32(req.Miner)
	if err != nil {
		return nil, err
	}

	return &QueryBanStatusResponse{
		Banned: q.Keeper.IsBanned(ctx, minerAddr),
	}, nil
}

func (q queryServer) ActiveValidators(goCtx context.Context, req *QueryActiveValidatorsRequest) (*QueryActiveValidatorsResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	addrs := q.Keeper.IterateActiveValidators(ctx)
	validators := make([]string, len(addrs))
	for i, addr := range addrs {
		validators[i] = addr.String()
	}

	return &QueryActiveValidatorsResponse{
		Validators: validators,
	}, nil
}

func (q queryServer) CurrentEpoch(goCtx context.Context, req *QueryCurrentEpochRequest) (*QueryCurrentEpochResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	return &QueryCurrentEpochResponse{
		Epoch: q.Keeper.CurrentEpoch(ctx),
	}, nil
}