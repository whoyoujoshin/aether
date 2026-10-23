package wallet

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	cometed25519 "github.com/cometbft/cometbft/crypto/ed25519"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cosmos/cosmos-sdk/client/grpc/cmtservice"
	"github.com/cosmos/cosmos-sdk/types/bech32"
)

// Consensus addresses here are upper-case hex, as CometBFT's RPC and logs
// print them (the seed's validator is F94934BD...).

// ConsensusAddressHex is the consensus address of an ed25519 consensus
// key (raw bytes), or "" if pubkey isn't one.
func ConsensusAddressHex(pubkey []byte) string {
	if len(pubkey) != cometed25519.PubKeySize {
		return ""
	}
	return strings.ToUpper(hex.EncodeToString(cometed25519.PubKey(pubkey).Address()))
}

// CommitSigners reports, for each validator in the set that committed
// height-1, whether its precommit for the block made it into the commit
// the block at height carries. A validator outside that set isn't listed.
func (c *Client) CommitSigners(ctx context.Context, height int64) (map[string]bool, error) {
	res, err := cmtservice.NewServiceClient(c.conn).GetBlockByHeight(ctx, &cmtservice.GetBlockByHeightRequest{Height: height})
	if err != nil {
		return nil, fmt.Errorf("block %d: %w", height, err)
	}
	var commit *cmtproto.Commit
	switch {
	case res.SdkBlock != nil:
		commit = res.SdkBlock.LastCommit
	case res.Block != nil:
		commit = res.Block.LastCommit
	}
	if commit == nil {
		return nil, fmt.Errorf("block %d has no last commit", height)
	}
	out := make(map[string]bool, len(commit.Signatures))
	for _, sig := range commit.Signatures {
		if len(sig.ValidatorAddress) == 0 {
			continue
		}
		out[strings.ToUpper(hex.EncodeToString(sig.ValidatorAddress))] = sig.BlockIdFlag == cmtproto.BlockIDFlagCommit
	}
	return out, nil
}

// ValidatorSet is each validator's voting power at height (0: the
// latest block), by consensus address, and the total.
func (c *Client) ValidatorSet(ctx context.Context, height int64) (map[string]int64, int64, error) {
	cmt := cmtservice.NewServiceClient(c.conn)
	var vals []*cmtservice.Validator
	if height > 0 {
		res, err := cmt.GetValidatorSetByHeight(ctx, &cmtservice.GetValidatorSetByHeightRequest{Height: height})
		if err != nil {
			return nil, 0, fmt.Errorf("validator set: %w", err)
		}
		vals = res.Validators
	} else {
		res, err := cmt.GetLatestValidatorSet(ctx, &cmtservice.GetLatestValidatorSetRequest{})
		if err != nil {
			return nil, 0, fmt.Errorf("validator set: %w", err)
		}
		vals = res.Validators
	}
	out := make(map[string]int64, len(vals))
	var total int64
	for _, v := range vals {
		_, addr, err := bech32.DecodeAndConvert(v.Address)
		if err != nil {
			return nil, 0, fmt.Errorf("validator address %q: %w", v.Address, err)
		}
		out[strings.ToUpper(hex.EncodeToString(addr))] = v.VotingPower
		total += v.VotingPower
	}
	return out, total, nil
}
