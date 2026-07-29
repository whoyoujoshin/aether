package pow

import (
	"context"
	"bytes"
	"crypto/sha256"

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

	switch submission := msg.Submission.(type) {
	case *MsgSubmitPoW_Native:
		return k.submitNativePoW(ctx, minerAddr, submission.Native)
	case *MsgSubmitPoW_AuxPow:
		return k.submitAuxPoW(ctx, minerAddr, submission.AuxPow)
	default:
		return nil, sdkerrors.Wrapf(types.ErrInvalidPoW, "submission must include either native or aux_pow data")
	}
}

// submitNativePoW handles a native Scrypt submission -- identical
// logic to the pre-AuxPoW handler, unchanged, just factored into its
// own function.
func (k msgServer) submitNativePoW(ctx sdk.Context, minerAddr sdk.AccAddress, native *NativeSubmission) (*MsgSubmitPoWResponse, error) {
	header := MiningHeader{
		Height:       native.Height,
		Timestamp:    native.Timestamp,
		PrevHash:     native.PrevHash,
		MerkleRoot:   native.MerkleRoot,
		Nonce:        native.Nonce,
		Difficulty:   native.Difficulty,
		MinerAddress: minerAddr,
	}

	claimedHeight := int64(header.Height)
	storedHash, ok := k.Keeper.GetRecentHash(ctx, claimedHeight)
	if !ok {
		return nil, sdkerrors.Wrapf(types.ErrUnknownAncestor, "no known block at height %d", claimedHeight)
	}
	if !bytes.Equal(storedHash, header.PrevHash) {
		return nil, sdkerrors.Wrapf(types.ErrUnknownAncestor, "prevHash does not match the real block hash at height %d", claimedHeight)
	}

	recencyWindow := k.Keeper.GetRecencyWindowK(ctx)
	if ctx.BlockHeight()-claimedHeight > recencyWindow {
		return nil, sdkerrors.Wrapf(types.ErrStaleAncestor, "claimed height %d is more than %d blocks behind current height %d", claimedHeight, recencyWindow, ctx.BlockHeight())
	}

	historicalDifficulty, ok := k.Keeper.GetRecentDifficulty(ctx, claimedHeight)
	if !ok {
		return nil, sdkerrors.Wrapf(types.ErrUnknownAncestor, "no recorded difficulty at height %d", claimedHeight)
	}
	if native.Difficulty < historicalDifficulty.Uint64() {
		return nil, sdkerrors.Wrapf(types.ErrInvalidPoW, "submitted difficulty %d below required difficulty %d at height %d", native.Difficulty, historicalDifficulty.Uint64(), claimedHeight)
	}

	if !k.Keeper.VerifyMiningHeader(ctx, header) {
		return nil, sdkerrors.Wrapf(types.ErrInvalidPoW, "proof of work verification failed for miner %s at height %d", minerAddr.String(), native.Height)
	}

	headerHash := sha256.Sum256(headerToBytes(header))
	if k.Keeper.IsWorkAccepted(ctx, headerHash[:]) {
		return nil, sdkerrors.Wrapf(types.ErrDuplicateWork, "this exact mining header has already been accepted")
	}

	if err := k.Keeper.DistributeBlockReward(ctx, minerAddr); err != nil {
		return nil, sdkerrors.Wrapf(err, "failed to distribute block reward")
	}
	newDifficulty := k.Keeper.AdjustDifficulty(ctx)
	k.Keeper.SetDifficulty(ctx, newDifficulty)
	k.Keeper.SetLastBlockTime(ctx, ctx.BlockTime().Unix())

	currentEpoch := k.Keeper.CurrentEpoch(ctx)
	k.Keeper.AddMiningWork(ctx, currentEpoch, minerAddr, 1)

	k.Keeper.MarkWorkAccepted(ctx, headerHash[:])

	return &MsgSubmitPoWResponse{}, nil
}

// submitAuxPoW handles a merged-mining submission. Per the locked
// design (see auxpow-decision-addendum.md): earns the full mining
// reward and retargets difficulty exactly like a native submission,
// but deliberately does NOT call AddMiningWork -- AuxPoW work secures
// the chain and earns rewards, but never counts toward Top-K validator
// eligibility, bonding, tenure, or governance voting power. Only
// native, dedicated work does.
func (k msgServer) submitAuxPoW(ctx sdk.Context, minerAddr sdk.AccAddress, auxPow *AuxPowData) (*MsgSubmitPoWResponse, error) {
	currentDifficulty := k.Keeper.GetDifficulty(ctx).Uint64()
	if err := CheckAuxPow(auxPow, currentDifficulty); err != nil {
		return nil, sdkerrors.Wrapf(types.ErrInvalidPoW, "AuxPoW verification failed: %s", err)
	}

	if k.Keeper.IsWorkAccepted(ctx, auxPow.AuxBlockHash) {
		return nil, sdkerrors.Wrapf(types.ErrDuplicateWork, "this exact AuxPoW submission has already been accepted")
	}

	if err := k.Keeper.DistributeBlockReward(ctx, minerAddr); err != nil {
		return nil, sdkerrors.Wrapf(err, "failed to distribute block reward")
	}
	newDifficulty := k.Keeper.AdjustDifficulty(ctx)
	k.Keeper.SetDifficulty(ctx, newDifficulty)
	k.Keeper.SetLastBlockTime(ctx, ctx.BlockTime().Unix())

	// Deliberately no AddMiningWork call here -- see function comment.

	k.Keeper.MarkWorkAccepted(ctx, auxPow.AuxBlockHash)

	return &MsgSubmitPoWResponse{}, nil
}