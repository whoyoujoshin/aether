package pow

import (
	"context"

	sdkerrors "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"crypto/ed25519"
	"github.com/whoyoujoshin/aether/x/pow/types"
	cometed25519 "github.com/cometbft/cometbft/crypto/ed25519"
)

type msgServer struct {
	Keeper
}

func NewMsgServerImpl(keeper Keeper) MsgServer {
	return &msgServer{Keeper: keeper}
}

func (k msgServer) RegisterValidatorPubkey(goCtx context.Context, msg *MsgRegisterValidatorPubkey) (*MsgRegisterValidatorPubkeyResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	minerAddr, err := sdk.AccAddressFromBech32(msg.Miner)
	if err != nil {
		return nil, sdkerrors.Wrapf(types.ErrInvalidCreator, "invalid miner address %q: %s", msg.Miner, err)
	}

	if len(msg.ConsensusPubkey) != ed25519.PublicKeySize {
		return nil, sdkerrors.Wrapf(types.ErrInvalidConsensusPubkey,
			"consensus pubkey must be exactly %d bytes, got %d", ed25519.PublicKeySize, len(msg.ConsensusPubkey))
	}

	challenge := []byte(msg.Miner)
	if !ed25519.Verify(msg.ConsensusPubkey, challenge, msg.Signature) {
		return nil, sdkerrors.Wrapf(types.ErrInvalidProofOfPossession,
			"signature does not verify against the provided consensus pubkey for miner %s", msg.Miner)
	}

	k.Keeper.SetValidatorPubkey(ctx, minerAddr, msg.ConsensusPubkey)

	consensusAddr := cometed25519.PubKey(msg.ConsensusPubkey).Address()
	k.Keeper.SetConsensusToMiner(ctx, consensusAddr, minerAddr)

	return &MsgRegisterValidatorPubkeyResponse{}, nil
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
	
	newDifficulty := k.Keeper.AdjustDifficulty(ctx)
	k.Keeper.SetDifficulty(ctx, newDifficulty)
	k.Keeper.SetLastBlockTime(ctx, ctx.BlockTime().Unix())

	currentEpoch := k.Keeper.CurrentEpoch(ctx)
	k.Keeper.AddMiningWork(ctx, currentEpoch, minerAddr, 1)

	return &MsgSubmitPoWResponse{}, nil
}