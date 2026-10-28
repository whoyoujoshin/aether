package app

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/whoyoujoshin/aether/gates"
	"github.com/whoyoujoshin/aether/x/treasury"
)

// FeesToTreasuryActivationHeight is the first block whose end moves the
// transaction fees collected into the treasury (docs/MAINNET.md, "Fees").
//
// x/auth sends every fee to the fee_collector module account, and
// nothing on this chain spends from it: there is no x/distribution. So
// fees have piled up there since genesis. From this height, at the end
// of every block, everything fee_collector holds, in every denom, moves
// to the treasury module account; the uaeth part is credited to the
// treasury's ledger (FundTreasury), so governance can spend it. The
// first such block also moves the backlog.
//
// Fees may be paid in USDC as well as AETH: which denoms a node accepts
// is its minimum-gas-prices, and a fee passes if it covers any one of
// them. USDC fees land in the treasury account; governance's spends are
// uaeth only.
//
// It adds no store, so nothing halts at it, but it changes what each
// block does, so every validator needs a binary carrying the height
// before the chain reaches it. On a fresh chain it is on from block 1.
//
// A placeholder far past the testnet's tip, chosen with the operator.
const FeesToTreasuryActivationHeight int64 = 30_000_000

// feesToTreasuryActivationHeight is what the EndBlocker reads, so tests
// can cross the activation at a small height.
var feesToTreasuryActivationHeight = FeesToTreasuryActivationHeight

// EventTypeFeesToTreasury is emitted at the end of a block whose fees
// moved, with the amount.
const EventTypeFeesToTreasury = "fees_to_treasury"

// EventTypeTreasuryCredited reports uaeth the treasury held that its
// ledger didn't track (sent straight to its address), now credited.
const EventTypeTreasuryCredited = "treasury_ledger_credited"

// sweepFeesToTreasury moves everything the fee collector holds into the
// treasury, from the activation height on, and brings the treasury's
// ledger up to its uaeth bank balance: uaeth sent straight to the
// treasury's address lands without the ledger knowing, and Spend reads
// the ledger, so governance couldn't spend it. The ledger is only ever
// raised; one above the bank balance stays visible in the Balance query.
func (app *App) sweepFeesToTreasury(ctx sdk.Context) error {
	if !gates.On(ctx, feesToTreasuryActivationHeight) {
		return nil
	}
	fees := app.BankKeeper.GetAllBalances(ctx, authtypes.NewModuleAddress(authtypes.FeeCollectorName))
	if !fees.IsZero() {
		if err := app.BankKeeper.SendCoinsFromModuleToModule(ctx, authtypes.FeeCollectorName, treasury.ModuleName, fees); err != nil {
			return err
		}
		app.TreasuryKeeper.FundTreasury(ctx, fees.AmountOf("uaeth"))
		ctx.EventManager().EmitEvent(sdk.NewEvent(EventTypeFeesToTreasury,
			sdk.NewAttribute(sdk.AttributeKeyAmount, fees.String()),
		))
	}
	if untracked := app.TreasuryKeeper.GetRealBankBalance(ctx).Sub(app.TreasuryKeeper.GetTreasuryBalance(ctx)); untracked.IsPositive() {
		app.TreasuryKeeper.FundTreasury(ctx, untracked)
		ctx.EventManager().EmitEvent(sdk.NewEvent(EventTypeTreasuryCredited,
			sdk.NewAttribute(sdk.AttributeKeyAmount, sdk.NewCoin("uaeth", untracked).String()),
		))
	}
	return nil
}
