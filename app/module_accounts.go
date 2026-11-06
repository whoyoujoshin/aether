package app

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	icatypes "github.com/cosmos/ibc-go/v8/modules/apps/27-interchain-accounts/types"
	ibctransfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"

	"github.com/whoyoujoshin/aether/gates"
	"github.com/whoyoujoshin/aether/x/escrow"
	"github.com/whoyoujoshin/aether/x/governance"
	"github.com/whoyoujoshin/aether/x/pow"
	"github.com/whoyoujoshin/aether/x/treasury"
)

// ModuleAccountsActivationHeight is where every module account is made to
// exist, found by FuzzMoneySequences (docs/REVIEW.md, F5). Bank blocks no
// addresses on this chain (the treasury takes money sent straight to it),
// and a module account is created only the first time its module uses
// it: on a fresh chain pow, treasury, governance, transfer and
// interchainaccounts don't exist after genesis. Until then anyone can send
// coins to one of those addresses and leave an ordinary account there,
// and the module panics on its next use ("account is not a module
// account"). For the treasury that is the fee sweep in EndBlocker, so one
// 1 uaeth send to its address before the first block reward, whose own fee
// reaches the fee collector, stops every node at that block for good; for
// pow, every mining reward fails; for governance, every proposal.
//
// At this height each one is created, or a squatted one converted in place
// keeping its number and coins, as x/escrow's own account already was at
// its activation. On fresh chains that is block 1, before any transaction.
// On the testnet all seven have long existed as module accounts (rewards,
// fees and vouchers created them; checked on the seed at 609,151), so its
// height, 712,080 with AuxPoWInputGuardHeight (docs/UPGRADE-INPUT-GUARDS.md;
// earlier binaries carried a 50,000,000 placeholder), changes no block
// there: TestModuleAccounts_NoChangeWhereTheyAllExist.
const ModuleAccountsActivationHeight int64 = 712_080

var moduleAccountsActivationHeight = ModuleAccountsActivationHeight

// moduleAccountNames is every account maccPerms registers, in the fixed
// order they are created (account numbers follow it).
var moduleAccountNames = []string{
	authtypes.FeeCollectorName,
	pow.ModuleName,
	treasury.ModuleName,
	governance.ModuleName,
	ibctransfertypes.ModuleName,
	icatypes.ModuleName,
	escrow.ModuleName,
}

func (app *App) ensureModuleAccountsAt(ctx sdk.Context) {
	if !gates.At(ctx, moduleAccountsActivationHeight) {
		return
	}
	for _, name := range moduleAccountNames {
		app.ensureModuleAccount(ctx, name)
	}
}

// ensureModuleAccount makes name's address a module account: created if
// missing, converted in place if an ordinary account was left there (it
// can't have a key: no one has the preimage of a module address).
func (app *App) ensureModuleAccount(ctx sdk.Context, name string) {
	addr := authtypes.NewModuleAddress(name)
	acc := app.AccountKeeper.GetAccount(ctx, addr)
	switch acc.(type) {
	case nil:
		app.AccountKeeper.GetModuleAccount(ctx, name) // creates it
	case sdk.ModuleAccountI:
	default:
		base := authtypes.NewBaseAccount(addr, nil, acc.GetAccountNumber(), acc.GetSequence())
		app.AccountKeeper.SetAccount(ctx, authtypes.NewModuleAccount(base, name))
	}
}
