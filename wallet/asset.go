package wallet

import (
	"fmt"
	"regexp"
	"strings"

	"cosmossdk.io/math"
	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
)

// Asset is a token the tools know by name. Amounts people and agents type
// name their asset by unit ("5 USDC", "1.5 AETH", "5000000uusdc"), so a
// USDC amount can never be read as AETH or the other way round.
type Asset struct {
	// Symbol is what people write: "AETH", "USDC".
	Symbol string `json:"symbol"`
	// Denom is the chain's name for it: "uaeth", or "ibc/<hash>" for a
	// token that arrived over IBC.
	Denom string `json:"denom"`
	// BaseUnit is the smallest unit's name, which people may also write:
	// "uaeth", "uusdc".
	BaseUnit string `json:"baseUnit"`
	// Decimals is how many base units make one Symbol, as a power of ten.
	Decimals int `json:"decimals"`
	// Origin says where it comes from: "Aether" for AETH, "Noble over
	// transfer/channel-3" for USDC.
	Origin string `json:"origin"`
}

// AETH is Aether's own token.
var AETH = Asset{Symbol: "AETH", Denom: BaseDenom, BaseUnit: BaseDenom, Decimals: AethDecimals, Origin: "Aether"}

var channelPattern = regexp.MustCompile(`^channel-[0-9]+$`)

// USDC is Noble's USDC as it exists on Aether after crossing channel, the
// Aether end of Aether's own channel to Noble. That's the only USDC these
// tools accept: the same token reaching Aether any other way (through
// Osmosis, say) has a different denom and isn't interchangeable with it.
func USDC(channel string) (Asset, error) {
	if !channelPattern.MatchString(channel) {
		return Asset{}, fmt.Errorf("USDC channel %q: want Aether's end of its channel to Noble, like channel-3", channel)
	}
	path := transfertypes.GetPrefixedDenom(transfertypes.PortID, channel, "uusdc")
	return Asset{
		Symbol:   "USDC",
		Denom:    transfertypes.ParseDenomTrace(path).IBCDenom(),
		BaseUnit: "uusdc",
		Decimals: 6,
		Origin:   "Noble over " + transfertypes.PortID + "/" + channel,
	}, nil
}

// Assets is the set of assets a tool accepts. AETH is always in it.
type Assets struct {
	list []Asset
}

// NewAssets is AETH, plus USDC over usdcChannel if it isn't empty.
func NewAssets(usdcChannel string) (*Assets, error) {
	a := &Assets{list: []Asset{AETH}}
	if usdcChannel != "" {
		usdc, err := USDC(usdcChannel)
		if err != nil {
			return nil, err
		}
		a.list = append(a.list, usdc)
	}
	return a, nil
}

// List is every asset, AETH first.
func (a *Assets) List() []Asset { return append([]Asset(nil), a.list...) }

// BySymbol finds an asset by symbol, case-insensitively.
func (a *Assets) BySymbol(symbol string) (Asset, bool) {
	for _, as := range a.list {
		if strings.EqualFold(as.Symbol, symbol) {
			return as, true
		}
	}
	return Asset{}, false
}

// ByDenom finds an asset by its chain denom.
func (a *Assets) ByDenom(denom string) (Asset, bool) {
	for _, as := range a.list {
		if as.Denom == denom {
			return as, true
		}
	}
	return Asset{}, false
}

func (a *Assets) units() string {
	var names []string
	for _, as := range a.list {
		names = append(names, as.Symbol, as.BaseUnit)
	}
	return strings.Join(names, ", ")
}

// Parse reads an amount with its unit: a symbol with up to Decimals
// places ("1.5 AETH", "2.25usdc") or a whole number of base units
// ("1500000uaeth", "2250000 uusdc"), case-insensitively. It returns the
// asset and a positive amount in base units that fits in an int64. A
// bare number, or a unit it doesn't know, is refused.
func (a *Assets) Parse(s string) (Asset, math.Int, error) {
	m := amountPattern.FindStringSubmatch(s)
	if m == nil {
		if _, ok := parseDecimal(strings.TrimSpace(s)); ok {
			return Asset{}, math.Int{}, fmt.Errorf("amount %q has no unit: write e.g. \"1.5 AETH\" or \"1500000uaeth\"", s)
		}
		return Asset{}, math.Int{}, fmt.Errorf("invalid amount %q: write e.g. \"1.5 AETH\" or \"1500000uaeth\"", s)
	}
	whole, frac, unit := m[1], m[2], m[3]
	for _, as := range a.list {
		switch {
		case strings.EqualFold(unit, as.Symbol):
			if len(frac) > as.Decimals {
				return Asset{}, math.Int{}, fmt.Errorf("amount %q has more than %d decimal places; %s is the smallest unit", s, as.Decimals, as.Format(math.OneInt()))
			}
			v, ok := parseDecimal(whole + frac + strings.Repeat("0", as.Decimals-len(frac)))
			if !ok {
				return Asset{}, math.Int{}, fmt.Errorf("invalid amount %q", s)
			}
			v, err := checkRange(s, v)
			return as, v, err
		case strings.EqualFold(unit, as.BaseUnit):
			if frac != "" {
				return Asset{}, math.Int{}, fmt.Errorf("amount %q: %s is the smallest unit and can't be fractional", s, as.BaseUnit)
			}
			v, ok := parseDecimal(whole)
			if !ok {
				return Asset{}, math.Int{}, fmt.Errorf("invalid amount %q", s)
			}
			v, err := checkRange(s, v)
			return as, v, err
		}
	}
	return Asset{}, math.Int{}, fmt.Errorf("amount %q has unknown unit %q: use %s", s, unit, a.units())
}

// Format renders base units as a decimal amount of the asset without
// trailing zeros, with its symbol: 1500000 -> "1.5 AETH".
func (as Asset) Format(base math.Int) string {
	return as.Decimal(base) + " " + as.Symbol
}

// Decimal renders base units as a decimal amount of the asset without
// trailing zeros or symbol: 1500000 -> "1.5", 1 -> "0.000001".
func (as Asset) Decimal(base math.Int) string {
	if as.Decimals == 0 {
		return base.String()
	}
	one := math.NewIntWithDecimal(1, as.Decimals)
	q, r := base.Quo(one), base.Mod(one)
	if r.IsZero() {
		return q.String()
	}
	frac := r.String()
	frac = strings.Repeat("0", as.Decimals-len(frac)) + frac
	return q.String() + "." + strings.TrimRight(frac, "0")
}
