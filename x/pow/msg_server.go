package pow

import (
	"context"

	sdkerrors "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/x/pow/types"
)

type msgServer struct {
	Keeper
}

func NewMsgServerImpl(keeper Keeper) MsgServer {
	return &msgServer{Keeper: keeper}
}

func (k msgServer) SubmitPoW(goCtx context.Context, msg *MsgSubmitPoW) (*MsgSubmitPoWResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	minerAddr, err := sdk.AccAddressFromBech32(msg.Miner)
	if err != nil {
		return nil, sdkerrors.Wrapf(types.ErrInvalidCreator, "invalid miner address %q: %s", msg.Miner, err)
	}

	header := MiningHeader{
		Height:       msg.Height,
		Timestamp:    msg.Timestamp,
		PrevHash:     msg.PrevHash,
		MerkleRoot:   msg.MerkleRoot,
		Nonce:        msg.Nonce,
		Difficulty:   msg.Difficulty,
		MinerAddress: minerAddr,
	}

	current := k.Keeper.GetDifficulty(ctx)
	if header.Difficulty < current.Uint64() {
		return nil, sdkerrors.Wrapf(types.ErrInvalidPoW, "submitted difficulty %d below required difficulty %d", header.Difficulty, current.Uint64())
	}

	if !k.Keeper.VerifyMiningHeader(ctx, header) {
		return nil, sdkerrors.Wrapf(types.ErrInvalidPoW, "proof of work verification failed for miner %s at height %d", msg.Miner, msg.Height)
	}

	if err := k.Keeper.DistributeBlockReward(ctx, minerAddr); err != nil {
		return nil, sdkerrors.Wrapf(err, "failed to distribute block reward")
	}

	k.Keeper.SetLastBlockTime(ctx, ctx.BlockTime().Unix())

	return &MsgSubmitPoWResponse{}, nil
}