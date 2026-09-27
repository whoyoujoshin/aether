package app

import (
	"context"

	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdked25519 "github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	capabilitykeeper "github.com/cosmos/ibc-go/modules/capability/keeper"
	ibckeeper "github.com/cosmos/ibc-go/v8/modules/core/keeper"
	ibctestingtypes "github.com/cosmos/ibc-go/v8/testing/types"

	"github.com/whoyoujoshin/aether/x/pow"
)

// The methods in this file exist so *App implements ibctesting.TestingApp,
// the Cosmos ecosystem's standard interface for driving an app inside a
// simulated multi-chain IBC test (see app/ibc_handshake_test.go). This is
// the conventional place a real chain implements it: ibc-go's own simapp
// does the same, directly in its production app.go, not behind a build tag.

func (app *App) GetBaseApp() *baseapp.BaseApp { return app.BaseApp }
func (app *App) AppCodec() codec.Codec        { return app.cdc }
func (app *App) GetTxConfig() client.TxConfig { return app.txConfig }

func (app *App) GetIBCKeeper() *ibckeeper.Keeper                   { return app.IBCKeeper }
func (app *App) GetScopedIBCKeeper() capabilitykeeper.ScopedKeeper { return app.ScopedIBCKeeper }

// GetStakingKeeper satisfies ibctesting.TestingApp's own need for a
// validator set "as of" a past height, to build IBC client-update
// headers inside a simulated test.
//
// This is NOT the real production self-client-validation path -- that's
// selfConsensusStakingShim (see ibc_self_consensus.go), which only ever
// reads Header fields and deliberately leaves Valset empty. This view
// instead always reports the CURRENT active validator set, regardless
// of which height is asked for. That would be wrong for a chain whose
// validator set actually rotates over time, so this type must never be
// wired into ibckeeper.NewKeeper -- but it's exactly right for a
// fixed-single-validator test that never rotates validators across its
// short lifetime, which is what this project's IBC tests are.
func (app *App) GetStakingKeeper() ibctestingtypes.StakingKeeper {
	return currentValidatorSetView{app: app}
}

type currentValidatorSetView struct{ app *App }

func (v currentValidatorSetView) GetHistoricalInfo(ctx context.Context, height int64) (stakingtypes.HistoricalInfo, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	var validators []stakingtypes.Validator
	for _, addr := range v.app.PowKeeper.IterateActiveValidators(sdkCtx) {
		rawPubkey, ok := v.app.PowKeeper.GetValidatorPubkey(sdkCtx, addr)
		if !ok {
			continue
		}
		anyPubkey, err := codectypes.NewAnyWithValue(&sdked25519.PubKey{Key: rawPubkey})
		if err != nil {
			return stakingtypes.HistoricalInfo{}, err
		}
		validators = append(validators, stakingtypes.Validator{
			ConsensusPubkey: anyPubkey,
			Status:          stakingtypes.Bonded,
			Tokens:          sdk.TokensFromConsensusPower(pow.ValidatorVotingPower, sdk.DefaultPowerReduction),
		})
	}
	return stakingtypes.HistoricalInfo{Valset: validators}, nil
}
