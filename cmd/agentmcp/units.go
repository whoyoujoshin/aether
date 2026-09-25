package main

import (
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/wallet"
)

// Amounts must carry a unit (see wallet.ParseAmount), and every result
// echoes both units so an agent never has to convert.

const baseDenom = wallet.BaseDenom

func parseAmount(s string) (math.Int, error) {
	amount, err := wallet.ParseAmount(s)
	if err != nil {
		return math.Int{}, newError(codeInvalidAmount, err.Error())
	}
	return amount, nil
}

func formatAeth(uaeth math.Int) string { return wallet.FormatAeth(uaeth) }

// amountDTO states an amount in both units, so an agent never has to
// convert (and can check the one it meant).
type amountDTO struct {
	Uaeth string `json:"uaeth" jsonschema:"amount in uaeth, the chain's base unit"`
	Aeth  string `json:"aeth" jsonschema:"the same amount in AETH (1 AETH = 1,000,000 uaeth)"`
}

func newAmountDTO(uaeth math.Int) amountDTO {
	return amountDTO{Uaeth: uaeth.String(), Aeth: formatAeth(uaeth)}
}

// coinsAmountDTO converts a coin string like "150000uaeth" (as the
// chain reports amounts); ok is false if it holds no uaeth.
func coinsAmountDTO(coins string) (amountDTO, bool) {
	c, err := sdk.ParseCoinsNormalized(coins)
	if err != nil || c.AmountOf(baseDenom).IsZero() {
		return amountDTO{}, false
	}
	return newAmountDTO(c.AmountOf(baseDenom)), true
}
