package wallet

import (
	"fmt"
	"math/big"
	"regexp"
	"strings"

	"cosmossdk.io/math"
)

// Amounts people and agents type must name their unit. The chain
// counts in uaeth (1 AETH = 1,000,000 uaeth), and mixing the two up is
// a million-fold error with real money -- so a bare number is refused
// rather than guessed at.

const (
	BaseDenom    = "uaeth"
	AethDecimals = 6
)

var (
	amountPattern = regexp.MustCompile(`^\s*([0-9]+)(?:\.([0-9]+))?\s*([A-Za-z]+)\s*$`)
	uaethPerAeth  = math.NewInt(1_000_000)
)

// ParseAmount accepts "1.5 AETH", "1.5aeth", "1500000uaeth" or
// "1500000 uaeth" (units case-insensitive) and returns a positive
// uaeth amount that fits in an int64.
func ParseAmount(s string) (math.Int, error) {
	m := amountPattern.FindStringSubmatch(s)
	if m == nil {
		if _, ok := parseDecimal(strings.TrimSpace(s)); ok {
			return math.Int{}, fmt.Errorf("amount %q has no unit: write e.g. \"1.5 AETH\" or \"1500000uaeth\" (1 AETH = 1,000,000 uaeth)", s)
		}
		return math.Int{}, fmt.Errorf("invalid amount %q: write e.g. \"1.5 AETH\" or \"1500000uaeth\"", s)
	}
	whole, frac, unit := m[1], m[2], strings.ToLower(m[3])

	var amount math.Int
	switch unit {
	case "aeth":
		if len(frac) > AethDecimals {
			return math.Int{}, fmt.Errorf("amount %q has more than %d decimal places; 0.000001 AETH (1 uaeth) is the smallest unit", s, AethDecimals)
		}
		v, ok := parseDecimal(whole + frac + strings.Repeat("0", AethDecimals-len(frac)))
		if !ok {
			return math.Int{}, fmt.Errorf("invalid amount %q", s)
		}
		amount = v
	case BaseDenom:
		if frac != "" {
			return math.Int{}, fmt.Errorf("amount %q: uaeth is the smallest unit and can't be fractional", s)
		}
		v, ok := parseDecimal(whole)
		if !ok {
			return math.Int{}, fmt.Errorf("invalid amount %q", s)
		}
		amount = v
	default:
		return math.Int{}, fmt.Errorf("amount %q has unknown unit %q: use AETH or uaeth", s, m[3])
	}
	return checkRange(s, amount)
}

// ParseUaeth reads a whole number of uaeth with no unit, as wire
// formats carry it (e.g. "1500000"), strictly in base 10.
func ParseUaeth(s string) (math.Int, error) {
	v, ok := parseDecimal(s)
	if !ok {
		return math.Int{}, fmt.Errorf("invalid amount %q: must be a whole number of uaeth", s)
	}
	return checkRange(s, v)
}

func checkRange(s string, amount math.Int) (math.Int, error) {
	if !amount.IsPositive() {
		return math.Int{}, fmt.Errorf("amount %q must be greater than zero", s)
	}
	if !amount.IsInt64() {
		return math.Int{}, fmt.Errorf("amount %q is too large", s)
	}
	return amount, nil
}

// parseDecimal is strictly base 10. math.NewIntFromString is not: it
// guesses the base from the prefix, so "0100000" would be octal 32768.
func parseDecimal(digits string) (math.Int, bool) {
	for _, c := range digits {
		if c < '0' || c > '9' {
			return math.Int{}, false
		}
	}
	v, ok := new(big.Int).SetString(digits, 10)
	if !ok || v.BitLen() > 255 {
		return math.Int{}, false
	}
	return math.NewIntFromBigInt(v), true
}

// FormatAeth renders uaeth as a decimal AETH string without trailing
// zeros: 1500000 -> "1.5", 1 -> "0.000001".
func FormatAeth(uaeth math.Int) string {
	q, r := uaeth.Quo(uaethPerAeth), uaeth.Mod(uaethPerAeth)
	if r.IsZero() {
		return q.String()
	}
	frac := r.String()
	frac = strings.Repeat("0", AethDecimals-len(frac)) + frac
	return q.String() + "." + strings.TrimRight(frac, "0")
}
