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

	consensusAddr := cometed25519.PubKey(msg.ConsensusPubkey).Address()

	// One key, one miner account: two accounts holding the same key can
	// put it in one block's validator updates twice, which halts the
	// chain. See ConsensusKeyGuardActivationHeight.
	if ctx.BlockHeight() >= ConsensusKeyGuardActivationHeight {
		if holder, ok := k.Keeper.GetMinerByConsensusAddr(ctx, consensusAddr); ok &&
			!holder.Equals(minerAddr) && !holder.Equals(sdk.AccAddress(consensusAddr)) {
			if held, ok := k.Keeper.GetValidatorPubkey(ctx, holder); ok && bytes.Equal(held, msg.ConsensusPubkey) {
				return nil, sdkerrors.Wrapf(types.ErrConsensusKeyInUse,
					"miner %s already holds this consensus key; register a different key there first to release it", holder)
			}
		}
	}

	// A real, live-flagged gap (Gitty, Section 3 item 1): this handler
	// never emitted any abci.ValidatorUpdate, so an active miner
	// rotating consensus keys left their OLD key's real CometBFT
	// voting power live forever -- nothing else in this module ever
	// builds a revocation from anything but the CURRENT registered
	// pubkey. Gated on RotationRevocationActivationHeight per the same
	// discipline as the other gates, even though a live-history audit
	// found this was never actually exercised (no miner has ever
	// registered a second consensus pubkey while active) -- see that
	// constant's doc comment. Scheduling the revocation here (rather
	// than emitting it directly, which a Msg handler cannot do) mirrors
	// the existing MarkPendingRemoval/IteratePendingRemovals pattern
	// this module already uses for immediate equivocation-driven
	// removal -- see module.go's EndBlock.
	if ctx.BlockHeight() >= RotationRevocationActivationHeight {
		if oldPubkey, ok := k.Keeper.GetValidatorPubkey(ctx, minerAddr); ok &&
			!bytes.Equal(oldPubkey, msg.ConsensusPubkey) &&
			k.Keeper.IsActiveValidator(ctx, minerAddr) {
			k.Keeper.MarkPendingKeyRevocation(ctx, minerAddr, oldPubkey)
			// Its old key loses power this block and the new one has
			// none, so it's no longer validating: leave the active set,
			// or a later removal would name the new key, which CometBFT
			// doesn't have, and halt the chain. It's back when an
			// epoch picks it with the new key.
			if ctx.BlockHeight() >= ConsensusKeyGuardActivationHeight {
				k.Keeper.RemoveActiveValidator(ctx, minerAddr)
				k.Keeper.ClearValidatorLiveness(ctx, minerAddr)
			}
		}
	}

	k.Keeper.SetValidatorPubkey(ctx, minerAddr, msg.ConsensusPubkey)
	k.Keeper.SetConsensusToMiner(ctx, consensusAddr, minerAddr)

	return &MsgRegisterValidatorPubkeyResponse{}, nil
}

func (k msgServer) SubmitPoW(goCtx context.Context, msg *MsgSubmitPoW) (*MsgSubmitPoWResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	minerAddr, err := sdk.AccAddressFromBech32(msg.Miner)
	if err != nil {
		return nil, sdkerrors.Wrapf(types.ErrInvalidCreator, "invalid miner address %q: %s", msg.Miner, err)
	}

	// A real, live-flagged gap (Gitty, Section 3 item 3): IsBanned was
	// only ever checked in Top-K qualification filtering -- nothing
	// here rejected a banned miner's submission itself, so they kept
	// minting the full real block reward forever, just permanently
	// excluded from ever becoming an active validator again. Gated on
	// BanEnforcementActivationHeight per the same discipline as the
	// other gates, even though a live-history audit found this was
	// never actually exercised (no miner has ever been banned) -- see
	// that constant's doc comment.
	if ctx.BlockHeight() >= BanEnforcementActivationHeight {
		if k.Keeper.IsBanned(ctx, minerAddr) {
			return nil, sdkerrors.Wrapf(types.ErrBannedMiner,
				"miner %s is permanently banned and may not submit further work", minerAddr.String())
		}
	}

	// A real, live-discovered gap: nothing previously limited how many
	// PoW submissions (native or AuxPoW) could be accepted within a
	// single block height. At low real difficulty -- particularly for
	// AuxPoW, whose self-consistency check doesn't require real
	// external mining work -- this allowed many rapid, cheap
	// submissions to each mint a full, uncapped block reward faster
	// than difficulty retargeting could react (retargeting itself
	// silently no-ops when elapsed <= 0, i.e. multiple submissions at
	// the same block timestamp). The chain was always implicitly
	// designed around roughly one accepted submission per block; this
	// makes that assumption an enforced rule instead of an unstated
	// one, closing both the minting and the retargeting blind spot at
	// their shared root cause.
	//
	// Gated on SubmissionCapActivationHeight -- the real height this
	// started running on the live seed -- not unconditional. This check
	// (and its tracking write below) is itself a real, live-discovered
	// instance of the same bug class as BootstrapPowerCorrectionHeight:
	// deployed with no activation gate, it makes a fresh node replaying
	// pre-deploy history pay gas for a Get/Set the seed's original
	// execution never performed, diverging gas_used (and therefore
	// LastResultsHash) on any historical tx that happens to hit the gas
	// limit. See SubmissionCapActivationHeight's doc comment.
	enforceCap := ctx.BlockHeight() >= SubmissionCapActivationHeight
	// From MergedMiningActivationHeight AuxPoW has its own slot, so each
	// block takes one native and one AuxPoW submission (change C of
	// docs/MERGED-MINING-PLAN.md). Before it both share the native slot.
	_, isAux := msg.Submission.(*MsgSubmitPoW_AuxPow)
	auxTrack := isAux && ctx.BlockHeight() >= MergedMiningActivationHeight
	if enforceCap && k.Keeper.submissionSlotTaken(ctx, auxTrack) {
		kind := "a PoW"
		if auxTrack {
			kind = "an AuxPoW"
		}
		return nil, sdkerrors.Wrapf(types.ErrTooManySubmissionsThisBlock,
			"%s submission has already been accepted at height %d; try again next block", kind, ctx.BlockHeight())
	}

	var resp *MsgSubmitPoWResponse
	switch submission := msg.Submission.(type) {
	case *MsgSubmitPoW_Native:
		resp, err = k.submitNativePoW(ctx, minerAddr, submission.Native)
	case *MsgSubmitPoW_AuxPow:
		resp, err = k.submitAuxPoW(ctx, minerAddr, submission.AuxPow)
	default:
		return nil, sdkerrors.Wrapf(types.ErrInvalidPoW, "submission must include either native or aux_pow data")
	}
	if err != nil {
		return nil, err
	}

	if enforceCap {
		k.Keeper.takeSubmissionSlot(ctx, auxTrack)
	}
	return resp, nil
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

	// From MergedMiningActivationHeight a native submission gives up the
	// merged share of the reward while merged mining is active.
	var err error
	if ctx.BlockHeight() >= MergedMiningActivationHeight {
		err = k.Keeper.DistributeReward(ctx, minerAddr, k.Keeper.NativeReward(ctx))
	} else {
		err = k.Keeper.DistributeBlockReward(ctx, minerAddr)
	}
	if err != nil {
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
// design (see auxpow-decision-addendum.md): earns a mining reward and
// retargets difficulty like a native submission (from
// MergedMiningActivationHeight, its own share and its own difficulty;
// see merged_mining.go), but deliberately does NOT call AddMiningWork -- AuxPoW work secures
// the chain and earns rewards, but never counts toward Top-K validator
// eligibility, bonding, tenure, or governance voting power. Only
// native, dedicated work does.
func (k msgServer) submitAuxPoW(ctx sdk.Context, minerAddr sdk.AccAddress, auxPow *AuxPowData) (*MsgSubmitPoWResponse, error) {
	// Below MergedMiningActivationHeight the signer is paid the full
	// reward, aux_block_hash is unchecked, and AuxPoW shares the native
	// difficulty, as it always did. From it, the proof must commit to a
	// recent block and a reward address, and that address is paid whoever
	// signs (change B); and AuxPoW runs on its own difficulty and earns
	// its share of the reward (change C, merged_mining.go).
	merged := ctx.BlockHeight() >= MergedMiningActivationHeight
	rewardAddr := minerAddr
	if merged {
		addr, err := k.checkAuxPoWTemplate(ctx, auxPow)
		if err != nil {
			return nil, err
		}
		rewardAddr = addr
	}

	var currentDifficulty uint64
	if merged {
		currentDifficulty = k.Keeper.GetAuxDifficulty(ctx).Uint64()
	} else {
		currentDifficulty = k.Keeper.GetDifficulty(ctx).Uint64()
	}
	if err := CheckAuxPow(auxPow, currentDifficulty, ctx.BlockHeight()); err != nil {
		return nil, sdkerrors.Wrapf(types.ErrInvalidPoW, "AuxPoW verification failed: %s", err)
	}

	if k.Keeper.IsWorkAccepted(ctx, auxPow.AuxBlockHash) {
		return nil, sdkerrors.Wrapf(types.ErrDuplicateWork, "this exact AuxPoW submission has already been accepted")
	}

	if merged {
		if err := k.Keeper.DistributeReward(ctx, rewardAddr, k.Keeper.AuxReward(ctx)); err != nil {
			return nil, sdkerrors.Wrapf(err, "failed to distribute block reward")
		}
		k.Keeper.SetAuxDifficulty(ctx, k.Keeper.AdjustAuxDifficulty(ctx))
		k.Keeper.SetAuxLastBlockTime(ctx, ctx.BlockTime().Unix())
	} else {
		if err := k.Keeper.DistributeBlockReward(ctx, rewardAddr); err != nil {
			return nil, sdkerrors.Wrapf(err, "failed to distribute block reward")
		}
		newDifficulty := k.Keeper.AdjustDifficulty(ctx)
		k.Keeper.SetDifficulty(ctx, newDifficulty)
		k.Keeper.SetLastBlockTime(ctx, ctx.BlockTime().Unix())
	}

	// Deliberately no AddMiningWork call here -- see function comment.

	k.Keeper.MarkWorkAccepted(ctx, auxPow.AuxBlockHash)

	return &MsgSubmitPoWResponse{}, nil
}

// checkAuxPoWTemplate checks that an AuxPoW submission commits to a
// recent Aether block and its reward address (change B of
// docs/MERGED-MINING-PLAN.md) and returns the address to pay. The same
// recency window as native work applies, and a banned address can't be
// paid by having someone else relay its proof.
func (k msgServer) checkAuxPoWTemplate(ctx sdk.Context, auxPow *AuxPowData) (sdk.AccAddress, error) {
	reward, err := sdk.AccAddressFromBech32(auxPow.RewardAddress)
	if err != nil {
		return nil, sdkerrors.Wrapf(types.ErrInvalidPoW, "invalid reward_address %q: %s", auxPow.RewardAddress, err)
	}
	if k.Keeper.IsBanned(ctx, reward) {
		return nil, sdkerrors.Wrapf(types.ErrBannedMiner, "reward address %s is permanently banned", reward)
	}
	blockHash, ok := k.Keeper.GetRecentHash(ctx, auxPow.TemplateHeight)
	if !ok {
		return nil, sdkerrors.Wrapf(types.ErrUnknownAncestor, "no known block at template height %d", auxPow.TemplateHeight)
	}
	if window := k.Keeper.GetRecencyWindowK(ctx); ctx.BlockHeight()-auxPow.TemplateHeight > window {
		return nil, sdkerrors.Wrapf(types.ErrStaleAncestor, "template height %d is more than %d blocks behind current height %d", auxPow.TemplateHeight, window, ctx.BlockHeight())
	}
	want := AuxPoWTemplateHash(ctx.ChainID(), auxPow.TemplateHeight, blockHash, reward)
	if !bytes.Equal(auxPow.AuxBlockHash, want) {
		return nil, sdkerrors.Wrapf(types.ErrInvalidPoW, "aux_block_hash is not the template for height %d and reward address %s", auxPow.TemplateHeight, reward)
	}
	return reward, nil
}

// UpdateParams is x/pow's authority-gated params-update handler --
// see MsgUpdateParams's proto comment for scope (five operational
// knobs only) and the full-replace convention. Reachable only via
// x/governance's generic MsgSubmitParamChangeProposal path: a regular
// user transaction can never satisfy the authority check below, since
// the governance module account has no private key to sign with. No
// separate activation-height gate is needed here -- this handler is
// already transitively unreachable before
// governance.ParamChangeGovernanceActivationHeight, since BOTH
// SubmitParamChangeProposal (submission) and ResolveProposal
// (execution) already refuse to operate before that height, and this
// message has no other path to ever execute.
func (k msgServer) UpdateParams(goCtx context.Context, msg *MsgUpdateParams) (*MsgUpdateParamsResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	if k.Keeper.GetAuthority() != msg.Authority {
		return nil, sdkerrors.Wrapf(types.ErrInvalidAuthority, "expected %s, got %s", k.Keeper.GetAuthority(), msg.Authority)
	}

	if msg.EpochLength <= 0 {
		return nil, sdkerrors.Wrapf(types.ErrInvalidParamValue, "epoch_length must be positive, got %d", msg.EpochLength)
	}
	if msg.TopKSize <= 0 {
		return nil, sdkerrors.Wrapf(types.ErrInvalidParamValue, "top_k_size must be positive, got %d", msg.TopKSize)
	}
	if msg.BondCooldown <= 0 {
		return nil, sdkerrors.Wrapf(types.ErrInvalidParamValue, "bond_cooldown must be positive, got %d", msg.BondCooldown)
	}
	if msg.RecencyWindowK <= 0 {
		return nil, sdkerrors.Wrapf(types.ErrInvalidParamValue, "recency_window_k must be positive, got %d", msg.RecencyWindowK)
	}
	if msg.BeaconRoundsPerBlock < 0 {
		return nil, sdkerrors.Wrapf(types.ErrInvalidParamValue, "beacon_rounds_per_block must not be negative, got %d", msg.BeaconRoundsPerBlock)
	}

	k.Keeper.SetEpochLength(ctx, msg.EpochLength)
	k.Keeper.SetTopKSize(ctx, msg.TopKSize)
	k.Keeper.SetBondCooldown(ctx, msg.BondCooldown)
	k.Keeper.SetRecencyWindowK(ctx, msg.RecencyWindowK)
	k.Keeper.SetBeaconRoundsPerBlock(ctx, msg.BeaconRoundsPerBlock)

	return &MsgUpdateParamsResponse{}, nil
}