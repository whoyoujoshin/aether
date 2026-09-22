package app

import (
	"context"

	tmproto "github.com/cometbft/cometbft/proto/tendermint/types"

	"github.com/cosmos/cosmos-sdk/baseapp"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/x/governance"
)

// consensusParamsMigratingStore adapts the real x/consensus keeper's
// ParamsStore (collections-backed, a different storage key than the
// old hand-rolled consensusParamsStore ever used) to fall back to that
// old store for any read that finds nothing yet in the new location.
//
// This closes the one real risk in swapping storage backends for
// something baseapp reads every single block to enforce real
// consensus rules (block.max_gas among them): without a fallback, the
// first block processed after this binary deploys could momentarily
// see a zero-valued ConsensusParams -- before MigrateConsensusParams
// (called from BeginBlock) gets a chance to copy the real values
// across -- which risks halting the chain outright, a far worse
// failure mode than anything else this project has hit. With the
// fallback, every read is correct regardless of exactly when in a
// block's processing the migration itself runs: reads fall through to
// the old store until the copy has genuinely happened, then read from
// the new store forever after.
//
// All writes go to the new store only -- once MsgUpdateParams starts
// being used for real, the old store is never touched again.
type consensusParamsMigratingStore struct {
	newStore baseapp.ParamStore
	oldStore consensusParamsStore
}

func (s consensusParamsMigratingStore) Get(ctx context.Context) (tmproto.ConsensusParams, error) {
	has, err := s.newStore.Has(ctx)
	if err != nil {
		return tmproto.ConsensusParams{}, err
	}
	if has {
		return s.newStore.Get(ctx)
	}
	return s.oldStore.Get(ctx)
}

func (s consensusParamsMigratingStore) Has(ctx context.Context) (bool, error) {
	has, err := s.newStore.Has(ctx)
	if err != nil {
		return false, err
	}
	if has {
		return true, nil
	}
	return s.oldStore.Has(ctx)
}

func (s consensusParamsMigratingStore) Set(ctx context.Context, cp tmproto.ConsensusParams) error {
	return s.newStore.Set(ctx, cp)
}

// MigrateConsensusParamsToNewStore performs the one-time copy from the
// old hand-rolled store into the real x/consensus keeper's store, the
// first time it's called after this binary deploys.
//
// Height-gated at governance.ParamChangeGovernanceActivationHeight --
// reversing this function's original design, which reasoned (correctly,
// but incompletely) that consensusParamsMigratingStore's read fallback
// makes correctness independent of exactly when the copy runs, and
// concluded no gate was needed. That reasoning only covered read
// correctness. It missed that the copy is a WRITE into the "consensus"
// KVStore, which is part of the committed multistore that feeds
// AppHash: the instant any single node runs this binary and processes
// one block, it performs this write while every still-old-binary peer
// does not, computing a different AppHash for that height -- the same
// divergence mechanism that already forced peer-1's solo-upgrade
// rollback (see BanEnforcementActivationHeight's doc comment in
// x/pow/types.go), just via this migration instead of a gate check.
// Gating the write itself here means no node performs it before every
// node is expected to have upgraded, making a rolling (non-instantaneous)
// fleet cutover safe rather than requiring literal single-block
// simultaneity across independently-run machines.
func (app *App) MigrateConsensusParamsToNewStore(ctx sdk.Context) error {
	if ctx.BlockHeight() < governance.ParamChangeGovernanceActivationHeight {
		return nil
	}

	has, err := app.ConsensusParamsKeeper.ParamsStore.Has(ctx)
	if err != nil {
		return err
	}
	if has {
		return nil
	}

	old := consensusParamsStore{storeKey: app.keys["consensus"]}
	oldHas, err := old.Has(ctx)
	if err != nil {
		return err
	}
	if !oldHas {
		// A genuinely fresh chain (never had the old store populated
		// either, e.g. a brand-new devnet) -- nothing to migrate, the
		// new store will be populated the normal way, by InitChain.
		return nil
	}

	cp, err := old.Get(ctx)
	if err != nil {
		return err
	}
	return app.ConsensusParamsKeeper.ParamsStore.Set(ctx, cp)
}
