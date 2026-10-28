package governance

import (
	"fmt"
	"regexp"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// SpendLedgerDenom is the denom x/treasury's ledger tracks; a spend of it
// goes through the ledger (Spend), any other through SpendCoin.
const SpendLedgerDenom = "uaeth"

// coinAmount is an integer then a denom, with nothing between: no
// decimals, no sign, one coin.
var coinAmount = regexp.MustCompile(`^([0-9]+)([a-zA-Z][a-zA-Z0-9/:._-]*)$`)

// ParseSpendAmount reads a treasury-spend proposal's amount from
// TreasuryCoinSpendActivationHeight: a plain integer is uaeth, as before;
// otherwise one coin of any valid denom, e.g. "1000000ibc/064D…" for 1
// USDC. The amount must be positive.
func ParseSpendAmount(amount string) (sdk.Coin, error) {
	if n, ok := math.NewIntFromString(amount); ok {
		if !n.IsPositive() {
			return sdk.Coin{}, fmt.Errorf("invalid or non-positive amount %q", amount)
		}
		return sdk.NewCoin(SpendLedgerDenom, n), nil
	}
	m := coinAmount.FindStringSubmatch(amount)
	if m == nil {
		return sdk.Coin{}, fmt.Errorf("invalid amount %q: a uaeth integer, or one coin such as 1000000ibc/<hash>", amount)
	}
	if err := sdk.ValidateDenom(m[2]); err != nil {
		return sdk.Coin{}, fmt.Errorf("invalid amount %q: %w", amount, err)
	}
	n, ok := math.NewIntFromString(m[1])
	if !ok || !n.IsPositive() {
		return sdk.Coin{}, fmt.Errorf("invalid or non-positive amount %q", amount)
	}
	return sdk.NewCoin(m[2], n), nil
}
