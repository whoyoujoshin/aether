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

	header := MiningHeader{
		Height:       msg.Height,
		Timestamp:    msg.Timestamp,
		PrevHash:     msg.PrevHash,
		MerkleRoot:   msg.MerkleRoot,
		Nonce:        msg.Nonce,
		Difficulty:   msg.Difficulty,
		MinerAddress: minerAddr,
	}

	// Ancestor validation: header.Height/PrevHash are an UNVERIFIED CLAIM
	// from the client until checked against our own recent-block-hash
	// window. Never trusted for accounting until these checks pass.
	claimedHeight := int64(header.Height)
	storedHash, ok := k.Keeper.GetRecentHash(ctx, claimedHeight)
	if !ok {
		return nil, sdkerrors.Wrapf(types.ErrUnknownAncestor,
			"no known block at height %d", claimedHeight)
	}
	if !bytes.Equal(storedHash, header.PrevHash) {
		return nil, sdkerrors.Wrapf(types.ErrUnknownAncestor,
			"prevHash does not match the real block hash at height %d", claimedHeight)
	}

	recencyWindow := k.Keeper.GetRecencyWindowK(ctx)
	if ctx.BlockHeight()-claimedHeight > recencyWindow {
		return nil, sdkerrors.Wrapf(types.ErrStaleAncestor,
			"claimed height %d is more than %d blocks behind current height %d",
			claimedHeight, recencyWindow, ctx.BlockHeight())
	}

	// Historical difficulty: check against what was actually in effect at
	// the claimed height, not the current live difficulty -- a miner
	// shouldn't be penalized if difficulty moved between when they solved
	// their header and when their transaction was included.
	historicalDifficulty, ok := k.Keeper.GetRecentDifficulty(ctx, claimedHeight)
	if !ok {
		return nil, sdkerrors.Wrapf(types.ErrUnknownAncestor,
			"no recorded difficulty at height %d", claimedHeight)
	}
	if header.Difficulty < historicalDifficulty.Uint64() {
		return nil, sdkerrors.Wrapf(types.ErrInvalidPoW,
			"submitted difficulty %d below required difficulty %d at height %d",
			header.Difficulty, historicalDifficulty.Uint64(), claimedHeight)
	}

	if !k.Keeper.VerifyMiningHeader(ctx, header) {
		return nil, sdkerrors.Wrapf(types.ErrInvalidPoW,
			"proof of work verification failed for miner %s at height %d", msg.Miner, msg.Height)
	}

	// Anti-replay: the header hash is computed from data that's
	// cryptographically bound to the actual proof of work (including the
	// miner's own address) -- any change to any field requires new work,
	// so re-submitting the identical header is unambiguous duplication.
	headerHash := sha256.Sum256(headerToBytes(header))
	if k.Keeper.IsWorkAccepted(ctx, headerHash[:]) {
		return nil, sdkerrors.Wrapf(types.ErrDuplicateWork,
			"this exact mining header has already been accepted")
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