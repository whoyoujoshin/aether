package pow

import (
	"bytes"
	"crypto/sha256"

	sdkerrors "cosmossdk.io/errors"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/x/pow/types"
)

// CheckedNativeSubmission is what CheckNativeSubmission found: the
// difficulty recorded at the claimed height (the seat work it earns) and
// the header's hash (its duplicate key).
type CheckedNativeSubmission struct {
	Difficulty math.Int
	HeaderHash []byte
}

// CheckNativeSubmission runs every check a native submission must pass
// before it is paid, and changes nothing: the claimed block is one this
// chain made, recent enough, the claimed difficulty is at least the one
// recorded there, the scrypt hash meets it, and this exact header hasn't
// been accepted. SubmitPoW runs it first; the app's fee check runs it in
// CheckTx too, so a fee-free submission only reaches the mempool with real
// proof-of-work behind it.
func (k Keeper) CheckNativeSubmission(ctx sdk.Context, minerAddr sdk.AccAddress, native *NativeSubmission) (CheckedNativeSubmission, error) {
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
	storedHash, ok := k.GetRecentHash(ctx, claimedHeight)
	if !ok {
		return CheckedNativeSubmission{}, sdkerrors.Wrapf(types.ErrUnknownAncestor, "no known block at height %d", claimedHeight)
	}
	if !bytes.Equal(storedHash, header.PrevHash) {
		return CheckedNativeSubmission{}, sdkerrors.Wrapf(types.ErrUnknownAncestor, "prevHash does not match the real block hash at height %d", claimedHeight)
	}

	recencyWindow := k.GetRecencyWindowK(ctx)
	if ctx.BlockHeight()-claimedHeight > recencyWindow {
		return CheckedNativeSubmission{}, sdkerrors.Wrapf(types.ErrStaleAncestor, "claimed height %d is more than %d blocks behind current height %d", claimedHeight, recencyWindow, ctx.BlockHeight())
	}

	historicalDifficulty, ok := k.GetRecentDifficulty(ctx, claimedHeight)
	if !ok {
		return CheckedNativeSubmission{}, sdkerrors.Wrapf(types.ErrUnknownAncestor, "no recorded difficulty at height %d", claimedHeight)
	}
	if native.Difficulty < historicalDifficulty.Uint64() {
		return CheckedNativeSubmission{}, sdkerrors.Wrapf(types.ErrInvalidPoW, "submitted difficulty %d below required difficulty %d at height %d", native.Difficulty, historicalDifficulty.Uint64(), claimedHeight)
	}

	if !k.VerifyMiningHeader(ctx, header) {
		return CheckedNativeSubmission{}, sdkerrors.Wrapf(types.ErrInvalidPoW, "proof of work verification failed for miner %s at height %d", minerAddr.String(), native.Height)
	}

	headerHash := sha256.Sum256(headerToBytes(header))
	if k.IsWorkAccepted(ctx, headerHash[:]) {
		return CheckedNativeSubmission{}, sdkerrors.Wrapf(types.ErrDuplicateWork, "this exact mining header has already been accepted")
	}
	return CheckedNativeSubmission{Difficulty: historicalDifficulty, HeaderHash: headerHash[:]}, nil
}
