package main

import (
	"fmt"
	"math/big"
	"regexp"
	"strings"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// Amounts must name their unit. The chain counts in uaeth (1 AETH =
// 1,000,000 uaeth), and a language model mixing the two up is a
// million-fold error with real money -- so a bare number is refused
// rather than guessed at, and every result echoes both units.

const (
	baseDenom    = "uaeth"
	aethDecimals = 6
)

var (
	amountPattern = regexp.MustCompile(`^\s*([0-9]+)(?:\.([0-9]+))?\s*([A-Za-z]+)\s*$`)
	uaethPerAeth  = math.NewInt(1_000_000)
)

// parseAmount accepts "1.5 AETH", "1.5aeth", "1500000uaeth" or
// "1500000 uaeth" (units case-insensitive) and returns uaeth.
func parseAmount(s string) (math.Int, error) {
	m := amountPattern.FindStringSubmatch(s)
	if m == nil {
		if _, ok := parseDecimal(strings.TrimSpace(s)); ok {
			return math.Int{}, newError(codeInvalidAmount, fmt.Sprintf("amount %q has no unit: write e.g. \"1.5 AETH\" or \"1500000uaeth\" (1 AETH = 1,000,000 uaeth)", s))
		}
		return math.Int{}, newError(codeInvalidAmount, fmt.Sprintf("invalid amount %q: write e.g. \"1.5 AETH\" or \"1500000uaeth\"", s))
	}
	whole, frac, unit := m[1], m[2], strings.ToLower(m[3])

	var amount math.Int
	switch unit {
	case "aeth":
		if len(frac) > aethDecimals {
			return math.Int{}, newError(codeInvalidAmount, fmt.Sprintf("amount %q has more than %d decimal places; 0.000001 AETH (1 uaeth) is the smallest unit", s, aethDecimals))
		}
		v, ok := parseDecimal(whole + frac + strings.Repeat("0", aethDecimals-len(frac)))
		if !ok {
			return math.Int{}, newError(codeInvalidAmount, fmt.Sprintf("invalid amount %q", s))
		}
		amount = v
	case baseDenom:
		if frac != "" {
			return math.Int{}, newError(codeInvalidAmount, fmt.Sprintf("amount %q: uaeth is the smallest unit and can't be fractional", s))
		}
		v, ok := parseDecimal(whole)
		if !ok {
			return math.Int{}, newError(codeInvalidAmount, fmt.Sprintf("invalid amount %q", s))
		}
		amount = v
	default:
		return math.Int{}, newError(codeInvalidAmount, fmt.Sprintf("amount %q has unknown unit %q: use AETH or uaeth", s, m[3]))
	}
	if !amount.IsPositive() {
		return math.Int{}, newError(codeInvalidAmount, fmt.Sprintf("amount %q must be greater than zero", s))
	}
	if !amount.IsInt64() {
		return math.Int{}, newError(codeInvalidAmount, fmt.Sprintf("amount %q is too large", s))
	}
	return amount, nil
}

// parseDecimal is strictly base 10. math.NewIntFromString is not: it
// guesses the base from the prefix, so "0100000" would be octal 32768.
func parseDecimal(digits string) (math.Int, bool) {
	v, ok := new(big.Int).SetString(digits, 10)
	if !ok || v.BitLen() > 255 {
		return math.Int{}, false
	}
	return math.NewIntFromBigInt(v), true
}

// formatAeth renders uaeth as a decimal AETH string without trailing
// zeros: 1500000 -> "1.5", 1 -> "0.000001".
func formatAeth(uaeth math.Int) string {
	q, r := uaeth.Quo(uaethPerAeth), uaeth.Mod(uaethPerAeth)
	if r.IsZero() {
		return q.String()
	}
	frac := r.String()
	frac = strings.Repeat("0", aethDecimals-len(frac)) + frac
	return q.String() + "." + strings.TrimRight(frac, "0")
}

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
