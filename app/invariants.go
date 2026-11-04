package app

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"

	"github.com/whoyoujoshin/aether/x/escrow"
	"github.com/whoyoujoshin/aether/x/governance"
	"github.com/whoyoujoshin/aether/x/pow"
)

// Invariants: what must hold after every block, whatever the
// transactions in it did (docs/REVIEW.md). Each is a reading of state,
// so checking them changes nothing on chain. Tests run them after their
// scenarios; a node runs them only when asked (see invariantsFromEnv),
// which is how a replay of the testnet from genesis or a devnet looks for
// the first block where one breaks.

// An Invariant reads state and says what is wrong, or "" if nothing is.
type Invariant struct {
	Name  string
	Check func(app *App, ctx sdk.Context) string
}

// Invariants is every invariant, in the order CheckInvariants runs them.
var Invariants = []Invariant{
	{"bank-total-supply", invBankSupply},
	{"pow-escrow-covered", invPowEscrow},
	{"escrow-covered", invEscrowModule},
	{"treasury-ledger-backed", invTreasuryLedger},
	{"governance-deposits-covered", invGovernanceDeposits},
	{"inflowcap-in-flight-totals", invInFlightTotals},
}

// CheckInvariants runs every invariant and joins what each finds.
func (app *App) CheckInvariants(ctx sdk.Context) error {
	var errs []error
	for _, inv := range Invariants {
		if msg := inv.Check(app, ctx); msg != "" {
			errs = append(errs, fmt.Errorf("%s: %s", inv.Name, msg))
		}
	}
	return errors.Join(errs...)
}

// The bank's own bookkeeping: its supply of each denom is the sum of
// every account's balance.
func invBankSupply(app *App, ctx sdk.Context) string {
	if msg, broken := bankkeeper.TotalSupply(app.BankKeeper)(ctx); broken {
		return strings.TrimSpace(msg)
	}
	return ""
}

func moduleBalance(app *App, ctx sdk.Context, module string) sdk.Coins {
	return app.BankKeeper.GetAllBalances(ctx, authtypes.NewModuleAddress(module))
}

// x/pow holds every validator's escrowed rewards (the bond) in its own
// module account: it must hold at least what the escrow records say, or a
// release or a forfeiture's burn would fail. Rewards are minted into it
// and paid out at once otherwise, so it holds nothing else on purpose.
func invPowEscrow(app *App, ctx sdk.Context) string {
	owed := sdkmath.ZeroInt()
	for _, addr := range app.PowKeeper.IterateEscrows(ctx) {
		owed = owed.Add(app.PowKeeper.GetEscrowBalance(ctx, addr))
	}
	held := moduleBalance(app, ctx, pow.ModuleName).AmountOf("uaeth")
	if held.LT(owed) {
		return fmt.Sprintf("module holds %suaeth, escrows owe %suaeth", held, owed)
	}
	return ""
}

// x/escrow: the module account covers every open escrow, every denom, so
// each can be released or refunded.
func invEscrowModule(app *App, ctx sdk.Context) string {
	if !app.escrowWired {
		return ""
	}
	owed := app.EscrowKeeper.TotalHeld(ctx)
	held := moduleBalance(app, ctx, escrow.ModuleName)
	if !held.IsAllGTE(owed) {
		return fmt.Sprintf("module holds %s, open escrows owe %s", held, owed)
	}
	return ""
}

// x/treasury: governance spends uaeth against the ledger, so the ledger
// may never promise more than the account holds. Below it is fine (uaeth
// sent straight to the address waits for the fee sweep to raise it).
func invTreasuryLedger(app *App, ctx sdk.Context) string {
	ledger := app.TreasuryKeeper.GetTreasuryBalance(ctx)
	bank := app.TreasuryKeeper.GetRealBankBalance(ctx)
	if ledger.GT(bank) {
		return fmt.Sprintf("ledger %suaeth is above the bank balance %suaeth", ledger, bank)
	}
	return ""
}

// x/governance holds the deposits of every proposal still in its deposit
// or voting period, each refunded or burned when it ends.
func invGovernanceDeposits(app *App, ctx sdk.Context) string {
	owed := sdkmath.ZeroInt()
	for _, p := range app.GovernanceKeeper.IterateProposals(ctx) {
		if p.Status != governance.ProposalStatus_PROPOSAL_STATUS_DEPOSIT_PERIOD &&
			p.Status != governance.ProposalStatus_PROPOSAL_STATUS_VOTING_PERIOD {
			continue
		}
		for _, d := range app.GovernanceKeeper.IterateDeposits(ctx, p.Id) {
			if amt, ok := sdkmath.NewIntFromString(d.Amount); ok && amt.IsPositive() {
				owed = owed.Add(amt)
			}
		}
	}
	held := moduleBalance(app, ctx, governance.ModuleName).AmountOf("uaeth")
	if held.LT(owed) {
		return fmt.Sprintf("module holds %suaeth, open proposals' deposits are %suaeth", held, owed)
	}
	return ""
}

// x/inflowcap: each voucher's in-flight total, which the cap counts, is
// the sum of its recorded sends, for every voucher with a record or a cap.
func invInFlightTotals(app *App, ctx sdk.Context) string {
	if !app.inflowCapWired {
		return ""
	}
	sums := map[string]sdkmath.Int{}
	for _, s := range app.InflowCapKeeper.InFlightSends(ctx) {
		if cur, ok := sums[s.VoucherDenom]; ok {
			sums[s.VoucherDenom] = cur.Add(s.Amount)
		} else {
			sums[s.VoucherDenom] = s.Amount
		}
	}
	for _, c := range app.InflowCapKeeper.Caps(ctx) {
		if _, ok := sums[c.VoucherDenom]; !ok {
			sums[c.VoucherDenom] = sdkmath.ZeroInt()
		}
	}
	var bad []string
	for voucher, sum := range sums {
		if total := app.InflowCapKeeper.InFlight(ctx, voucher); !total.Equal(sum) {
			bad = append(bad, fmt.Sprintf("%s total %s, records sum to %s", voucher, total, sum))
		}
	}
	sort.Strings(bad)
	return strings.Join(bad, "; ")
}

// Checking on a running node. AETHER_INVARIANTS_EVERY=N checks every
// invariant at the end of every Nth block and logs what breaks; with
// AETHER_INVARIANTS_HALT=yes a broken invariant also stops the node at
// that block, for a replay or a devnet. Unset (the default), nothing is
// checked: on a big state the bank's check reads every balance.
type invariantCheck struct {
	every int64
	halt  bool
}

func invariantsFromEnv() invariantCheck {
	n, err := strconv.ParseInt(os.Getenv("AETHER_INVARIANTS_EVERY"), 10, 64)
	if err != nil || n <= 0 {
		return invariantCheck{}
	}
	return invariantCheck{every: n, halt: os.Getenv("AETHER_INVARIANTS_HALT") == "yes"}
}

// checkInvariantsAtEnd runs at the end of EndBlocker when asked. It only
// reads state: whether it runs never changes a block's results.
func (app *App) checkInvariantsAtEnd(ctx sdk.Context) error {
	if app.invariants.every == 0 || ctx.BlockHeight()%app.invariants.every != 0 {
		return nil
	}
	err := app.CheckInvariants(ctx)
	if err == nil {
		return nil
	}
	app.Logger().Error("invariant broken", "height", ctx.BlockHeight(), "error", err)
	if app.invariants.halt {
		return fmt.Errorf("invariant broken at height %d: %w", ctx.BlockHeight(), err)
	}
	return nil
}
