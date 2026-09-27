package app

import (
	"context"
	"time"

	tmproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

// ibcSelfConsensusStoreKey is a small store IBC's Tendermint light
// client needs to answer "what did Aether's own consensus state look
// like at block H" -- used when a counterparty chain opens a connection
// to Aether and Aether must validate the counterparty's record of it
// (see clienttypes.ConsensusHost: GetSelfConsensusState / ValidateSelfClient).
//
// A staking-based Cosmos SDK chain gets this for free from x/staking's
// own HistoricalInfo (TrackHistoricalInfo / GetHistoricalInfo). Aether
// has no x/staking, so this is that same mechanism, hand-rolled: the
// full block header is recorded every BeginBlock (once IBC is wired --
// see checkIBCActivation) and pruned to the last
// selfConsensusHistoryLength entries. A relayer only ever needs a
// header from very close to the current tip (it creates a client from a
// header it just queried), so this window is generous, not tight.
const ibcSelfConsensusStoreKey = "ibcselfconsensus"

// selfConsensusHistoryLength bounds the window, matching how
// x/staking's own HistoricalEntries param bounds its equivalent store
// (default 100 there; a little more headroom here since nothing prunes
// it except this fixed constant -- there's no governance param for it).
const selfConsensusHistoryLength = 200

var selfConsensusHeaderPrefix = []byte("h/")

func selfConsensusHeaderKey(height int64) []byte {
	return append(append([]byte{}, selfConsensusHeaderPrefix...), sdk.Uint64ToBigEndian(uint64(height))...)
}

// trackSelfConsensusInfo records the current block's header and prunes
// anything older than the retention window. Called from BeginBlocker,
// gated on IBC being wired.
func (app *App) trackSelfConsensusInfo(ctx sdk.Context) {
	store := ctx.KVStore(app.keys[ibcSelfConsensusStoreKey])
	header := ctx.BlockHeader()
	bz, err := header.Marshal()
	if err != nil {
		panic(err) // a Header always marshals; failure here means memory corruption
	}
	store.Set(selfConsensusHeaderKey(ctx.BlockHeight()), bz)

	if prune := ctx.BlockHeight() - selfConsensusHistoryLength; prune > 0 {
		store.Delete(selfConsensusHeaderKey(prune))
	}
}

// selfConsensusStakingShim implements the clienttypes.StakingKeeper
// interface ibc-go's Tendermint light client needs for self-client
// validation, without an x/staking module.
//
// See BondCooldownProduction's own doc comment in x/pow/types.go for
// why BondCooldown -- the window a PoW validator's escrow stays
// slashable for equivocation discovered after the fact -- is Aether's
// real analog of a bonded chain's unbonding period: both answer the
// same question IBC's light client cares about ("how long after
// misbehavior can a validator still be held accountable, and so how
// long can a client safely go without an update"). Recomputed from the
// live params on every call, deliberately not cached, matching x/pow's
// own accessor pattern; if governance ever widens BondCooldown or
// TargetBlockTime, the very next handshake immediately reflects it
// (already-open connections are unaffected -- this is only consulted at
// ConnOpenTry/ConnOpenAck, never per-packet).
type selfConsensusStakingShim struct {
	app *App
}

func (s selfConsensusStakingShim) UnbondingTime(ctx context.Context) (time.Duration, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cooldownBlocks := s.app.PowKeeper.GetBondCooldown(sdkCtx)
	blockTimeSeconds := s.app.PowKeeper.GetTargetBlockTime(sdkCtx)
	return time.Duration(cooldownBlocks) * time.Duration(blockTimeSeconds) * time.Second, nil
}

func (s selfConsensusStakingShim) GetHistoricalInfo(ctx context.Context, height int64) (stakingtypes.HistoricalInfo, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	store := sdkCtx.KVStore(s.app.keys[ibcSelfConsensusStoreKey])
	bz := store.Get(selfConsensusHeaderKey(height))
	if bz == nil {
		return stakingtypes.HistoricalInfo{}, stakingtypes.ErrNoHistoricalInfo
	}
	var header tmproto.Header
	if err := header.Unmarshal(bz); err != nil {
		return stakingtypes.HistoricalInfo{}, err
	}
	return stakingtypes.HistoricalInfo{Header: header}, nil
}
