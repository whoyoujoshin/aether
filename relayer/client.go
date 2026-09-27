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

// AetherUnbondingPeriod queries chain's real, live x/pow params and
// derives IBC's "unbonding period" analog from them -- see
// app/ibc_self_consensus.go's selfConsensusStakingShim.UnbondingTime
// doc comment for why BondCooldown*TargetBlockTime is Aether's genuine
// equivalent (never hardcode this: governance can change either param).
func AetherUnbondingPeriod(chain *Chain) (time.Duration, error) {
	resp, err := pow.NewQueryClient(chain.ClientCtx).Params(context.Background(), &pow.QueryParamsRequest{})
	if err != nil {
		return 0, fmt.Errorf("%s: querying pow params: %w", chain.Name, err)
	}
	return time.Duration(resp.BondCooldown) * time.Duration(resp.TargetBlockTime) * time.Second, nil
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
