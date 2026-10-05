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
	// Origin says where it comes from: "Aether" for AETH; for USDC, its
	// issuer and route, like "Noble over transfer/channel-3".
	Origin string `json:"origin"`
}

// AETH is Aether's own token.
var AETH = Asset{Symbol: "AETH", Denom: BaseDenom, BaseUnit: BaseDenom, Decimals: AethDecimals, Origin: "Aether"}

var (
	channelPattern = regexp.MustCompile(`^channel-[0-9]+$`)
	// One hop of an ICS-20 denom trace: a port (ICS-24 identifier) and a channel.
	hopPattern = regexp.MustCompile(`^[a-zA-Z0-9._+\-#\[\]<>]{2,128}/channel-[0-9]+$`)
	// The SDK's own rule for a coin denom.
	baseDenomPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9/:._-]{2,127}$`)
	// An issuer's name: one word, since tools label USDC by it ("USDC (Noble)").
	issuerPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$`)
)

// DefaultUSDCBaseDenom is USDC's denom on Noble, which issues it.
const DefaultUSDCBaseDenom = "uusdc"

// USDC is Noble's USDC as it exists on Aether after crossing channel, the
// Aether end of Aether's own channel to Noble. It's USDCAt with a single
// hop and Noble's base denom.
func USDC(channel string) (Asset, error) {
	if !channelPattern.MatchString(channel) {
		return Asset{}, fmt.Errorf("USDC channel %q: want Aether's end of its channel to Noble, like channel-3", channel)
	}
	return USDCAt(transfertypes.PortID+"/"+channel, DefaultUSDCBaseDenom)
}

// USDCAt is the USDC that reaches Aether along path, the ICS-20 denom
// trace as Aether records it (Aether's own hop first), with baseDenom its
// denom on the chain that issues it. For example:
//
//   - Noble's USDC over a direct channel: "transfer/channel-3", "uusdc".
//   - Noble's USDC through Osmosis: "transfer/channel-1/transfer/channel-4280", "uusdc".
//   - Circle's USDC on Injective, through Osmosis:
//     "transfer/channel-1/transfer/channel-10092",
//     "erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d".
//
// That's the only USDC these tools accept. The same token reaching Aether
// any other way has a different denom and isn't interchangeable with it,
// so it's never mistaken for USDC. baseDenom is case-sensitive: the IBC
// hash covers it exactly.
func USDCAt(path, baseDenom string) (Asset, error) {
	hops := strings.Split(path, "/")
	if path == "" || len(hops)%2 != 0 {
		return Asset{}, fmt.Errorf("USDC path %q: want port/channel hops from Aether's end, like transfer/channel-1/transfer/channel-4280", path)
	}
	for i := 0; i < len(hops); i += 2 {
		if hop := hops[i] + "/" + hops[i+1]; !hopPattern.MatchString(hop) {
			return Asset{}, fmt.Errorf("USDC path %q: hop %q isn't port/channel-N", path, hop)
		}
	}
	if strings.HasPrefix(baseDenom, "ibc/") || !baseDenomPattern.MatchString(baseDenom) {
		return Asset{}, fmt.Errorf("USDC base denom %q: want its denom on the chain that issues it, like uusdc, not an ibc/ hash", baseDenom)
	}
	issuer := baseDenom
	if baseDenom == DefaultUSDCBaseDenom {
		issuer = "Noble"
	}
	return Asset{
		Symbol:   "USDC",
		Denom:    transfertypes.DenomTrace{Path: path, BaseDenom: baseDenom}.IBCDenom(),
		BaseUnit: "uusdc",
		Decimals: 6,
		Origin:   issuer + " over " + path,
	}, nil
}

// USDCSetting is how a tool is told which USDC it accepts: Channel, as
// shorthand for Noble's USDC over Aether's direct channel to Noble, or
// Path and BaseDenom for any other route or issuer (BaseDenom defaults to
// uusdc). All empty: the tool knows only AETH. Issuer names the issuer in
// the asset's Origin, which tools label USDC by ("USDC (Injective)"); it
// defaults to Noble for uusdc and to the base denom otherwise.
type USDCSetting struct {
	Channel   string
	Path      string
	BaseDenom string
	Issuer    string
}

// Asset is the USDC the setting names, and false if it names none.
func (s USDCSetting) Asset() (Asset, bool, error) {
	usdc, ok, err := s.asset()
	if err != nil || s.Issuer == "" {
		return usdc, ok, err
	}
	if !ok {
		return Asset{}, false, fmt.Errorf("USDC issuer %q needs a USDC channel or path too", s.Issuer)
	}
	if !issuerPattern.MatchString(s.Issuer) {
		return Asset{}, false, fmt.Errorf("USDC issuer %q: want one word, like Injective", s.Issuer)
	}
	_, route, _ := strings.Cut(usdc.Origin, " over ")
	usdc.Origin = s.Issuer + " over " + route
	return usdc, true, nil
}

func (s USDCSetting) asset() (Asset, bool, error) {
	switch {
	case s.Channel != "" && (s.Path != "" || s.BaseDenom != ""):
		return Asset{}, false, fmt.Errorf("set either the USDC channel or the USDC path and base denom, not both")
	case s.Channel != "":
		usdc, err := USDC(s.Channel)
		return usdc, err == nil, err
	case s.Path != "":
		base := s.BaseDenom
		if base == "" {
			base = DefaultUSDCBaseDenom
		}
		usdc, err := USDCAt(s.Path, base)
		return usdc, err == nil, err
	case s.BaseDenom != "":
		return Asset{}, false, fmt.Errorf("USDC base denom %q needs a USDC path too", s.BaseDenom)
	}
	return Asset{}, false, nil
}

// Assets is the set of assets a tool accepts. AETH is always in it.
type Assets struct {
	list []Asset
}

// NewAssets is AETH, plus USDC over usdcChannel if it isn't empty.
func NewAssets(usdcChannel string) (*Assets, error) {
	return NewAssetsFor(USDCSetting{Channel: usdcChannel})
}

// NewAssetsFor is AETH, plus the USDC the setting names, if any.
func NewAssetsFor(s USDCSetting) (*Assets, error) {
	a := &Assets{list: []Asset{AETH}}
	usdc, ok, err := s.Asset()
	if err != nil {
		return nil, err
	}
	if ok {
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
