package app

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/whoyoujoshin/aether/x/escrow"
)

// EscrowActivationHeight is the first block that executes with x/escrow
// live. Same mechanism as AccountAuthActivationHeight (see
// app/authz_feegrant.go for why a new store needs a height-gated,
// restart-driven activation): an un-restarted node halts at it rather
// than run it without the store.
//
// PLACEHOLDER: replace with a height agreed with the operators and
// confirmed against the live tip immediately before the cutover.
const EscrowActivationHeight int64 = 1_000_000

// escrowActivationHeight is what New() reads, so tests can cross the
// activation at a small height.
var escrowActivationHeight = EscrowActivationHeight

var escrowStoreKeys = []string{escrow.StoreKey}

type escrowPlan struct {
	wire      bool // mount the store and register keeper, module and messages
	addStores bool // this is the one startup that creates the store
}

// planEscrow mirrors planAccountAuth.
func planEscrow(lastCommittedHeight, activationHeight int64) escrowPlan {
	if lastCommittedHeight < activationHeight-1 {
		return escrowPlan{}
	}
	return escrowPlan{wire: true, addStores: lastCommittedHeight == activationHeight-1}
}

// escrowMarkerKey keeps the new store non-empty from its first block,
// for the reason accountAuthMarkerKey does. 0xff is no prefix the
// module uses (0x01..0x05).
var escrowMarkerKey = []byte("\xffaether/escrow-activated")

// checkEscrowActivation halts a node that reaches the activation height
// without the store, like checkAccountAuthActivation, and at that height
// sets up the module account the escrowed money lives in.
func (app *App) checkEscrowActivation(ctx sdk.Context) error {
	if app.escrowWired {
		if ctx.BlockHeight() == escrowActivationHeight {
			for _, name := range escrowStoreKeys {
				ctx.KVStore(app.keys[name]).Set(escrowMarkerKey, []byte{1})
			}
			app.ensureEscrowModuleAccount(ctx)
		}
		return nil
	}
	if ctx.BlockHeight() < escrowActivationHeight {
		return nil
	}
	return fmt.Errorf(
		"x/escrow activates at height %d: restart this node (e.g. `systemctl restart aetherd`) to add its store, then it will resume from this block",
		escrowActivationHeight,
	)
}

// ensureEscrowModuleAccount makes the escrow address a module account.
// Bank blocks no addresses on this chain, so before activation anyone
// could send coins to that address and leave an ordinary account there;
// the first escrow would then panic ("is not a module account"). Such an
// account can't have a key, so it's converted in place, keeping its
// number and its coins.
func (app *App) ensureEscrowModuleAccount(ctx sdk.Context) {
	addr := authtypes.NewModuleAddress(escrow.ModuleName)
	acc := app.AccountKeeper.GetAccount(ctx, addr)
	switch acc.(type) {
	case nil:
		app.AccountKeeper.GetModuleAccount(ctx, escrow.ModuleName) // creates it
	case sdk.ModuleAccountI:
	default:
		base := authtypes.NewBaseAccount(addr, nil, acc.GetAccountNumber(), acc.GetSequence())
		app.AccountKeeper.SetAccount(ctx, authtypes.NewModuleAccount(base, escrow.ModuleName))
	}
}
