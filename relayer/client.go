package relayer

import (
	"context"
	"fmt"
	"time"

	clientutils "github.com/cosmos/ibc-go/v8/modules/core/02-client/client/utils"
	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	commitmenttypes "github.com/cosmos/ibc-go/v8/modules/core/23-commitment/types"
	ibctm "github.com/cosmos/ibc-go/v8/modules/light-clients/07-tendermint"

	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/whoyoujoshin/aether/x/pow"
)

// blockTimeSampleBlocks is how far back AetherUnbondingPeriod looks to
// measure the chain's real block interval.
const blockTimeSampleBlocks = 1000

// AetherUnbondingPeriod is the unbonding period a light client of Aether
// should use: x/pow's BondCooldown (the blocks a validator's escrow
// stays slashable after its last activity) times the chain's MEASURED
// block interval. Not x/pow's TargetBlockTime: live blocks run ~5s
// against a 60s target, and converting with the target overstated the
// real lockup ~12x, letting a client keep trusting validators who had
// already withdrawn.
//
// This is purely the relayer's choice. ibc-go v8's connection handshake
// only verifies connection state; it never checks a client of Aether
// against Aether's own view (ValidateSelfClient is unused), so nothing
// on-chain enforces or advertises this value.
func AetherUnbondingPeriod(chain *Chain) (time.Duration, error) {
	resp, err := pow.NewQueryClient(chain.ClientCtx).Params(context.Background(), &pow.QueryParamsRequest{})
	if err != nil {
		return 0, fmt.Errorf("%s: querying pow params: %w", chain.Name, err)
	}
	perBlock, err := measuredBlockTime(chain)
	if err != nil {
		return 0, err
	}
	return time.Duration(resp.BondCooldown) * perBlock, nil
}

// measuredBlockTime averages the interval between the latest header and
// the one blockTimeSampleBlocks (or as many as exist) before it.
func measuredBlockTime(chain *Chain) (time.Duration, error) {
	node, err := chain.ClientCtx.GetNode()
	if err != nil {
		return 0, err
	}
	latest, err := node.Commit(context.Background(), nil)
	if err != nil {
		return 0, fmt.Errorf("%s: latest header: %w", chain.Name, err)
	}
	tip := latest.Height
	from := tip - blockTimeSampleBlocks
	if from < 1 {
		from = 1
	}
	if from >= tip {
		return 0, fmt.Errorf("%s: need at least 2 blocks to measure block time, have %d", chain.Name, tip)
	}
	old, err := node.Commit(context.Background(), &from)
	if err != nil {
		return 0, fmt.Errorf("%s: header %d: %w", chain.Name, from, err)
	}
	return latest.Time.Sub(old.Time) / time.Duration(tip-from), nil
}

// StakingUnbondingPeriod queries chain's real, live x/staking params
// for its actual UnbondingTime -- for the counterparty chain, which
// uses genuine cosmos-sdk staking rather than Aether's PoW analog.
func StakingUnbondingPeriod(chain *Chain) (time.Duration, error) {
	resp, err := stakingtypes.NewQueryClient(chain.ClientCtx).Params(context.Background(), &stakingtypes.QueryParamsRequest{})
	if err != nil {
		return 0, fmt.Errorf("%s: querying staking params: %w", chain.Name, err)
	}
	return resp.Params.UnbondingTime, nil
}

// CreateClient builds a 07-tendermint ClientState + ConsensusState from
// src's own real, current consensus state (queried live over RPC via
// ibc-go's own QueryTendermintHeader -- the same helper `aetherd query
// ibc client ...` uses) and submits MsgCreateClient on dst, so dst now
// tracks src. Returns the new client's ID as dst assigned it.
func CreateClient(src, dst *Chain, unbondingPeriod time.Duration) (string, error) {
	header, height, err := clientutils.QueryTendermintHeader(src.ClientCtx)
	if err != nil {
		return "", fmt.Errorf("querying %s's header: %w", src.Name, err)
	}

	consensusState := ibctm.NewConsensusState(
		header.SignedHeader.Header.Time,
		commitmenttypes.NewMerkleRoot(header.SignedHeader.Header.AppHash),
		header.SignedHeader.Header.NextValidatorsHash,
	)

	// TrustingPeriod must be strictly less than UnbondingPeriod (ibc-go
	// rejects an equal or larger one) -- 2/3 is the conventional
	// default every relayer uses.
	trustingPeriod := (unbondingPeriod * 2) / 3

	// A chain ID of the conventional "<name>-<revision>" form (e.g.
	// "aether-relayertest-1") carries a revision number IBC itself
	// parses back out of it; the client's latest height must use that
	// same revision or ibc-go rejects it as internally inconsistent
	// ("latest height revision number must match chain id revision
	// number"), even though nothing about the chain actually forked.
	clientState := ibctm.NewClientState(
		src.ChainID, ibctm.DefaultTrustLevel, trustingPeriod, unbondingPeriod, 10*time.Minute,
		clienttypes.NewHeight(clienttypes.ParseChainID(src.ChainID), uint64(height)), commitmenttypes.GetSDKSpecs(), nil,
	)

	msg, err := clienttypes.NewMsgCreateClient(clientState, consensusState, dst.FromAddrStr)
	if err != nil {
		return "", err
	}

	events, err := dst.SignAndBroadcast(msg)
	if err != nil {
		return "", fmt.Errorf("creating client for %s on %s: %w", src.Name, dst.Name, err)
	}
	clientID, err := EventAttr(events, clienttypes.EventTypeCreateClient, clienttypes.AttributeKeyClientID)
	if err != nil {
		return "", err
	}
	return clientID, nil
}
