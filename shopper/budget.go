package shopper

import (
	"fmt"
	"strings"

	sdkmath "cosmossdk.io/math"

	"github.com/whoyoujoshin/aether/wallet"
)

// AgentCap is a tighter daily limit for one named caller. It cannot
// exceed the account's own daily cap; a larger figure is ignored.
type AgentCap struct {
	Name  string
	Daily sdkmath.Int
}

// ForAgent returns a policy whose daily cap is the tighter of the
// account cap and this caller's cap. An unknown name keeps the account cap.
func (p Policy) ForAgent(name string, caps []AgentCap) Policy {
	out := p
	name = strings.TrimSpace(name)
	if name == "" {
		return out
	}
	for _, c := range caps {
		if c.Name != name || c.Daily.IsNil() || !c.Daily.IsPositive() {
			continue
		}
		if c.Daily.LT(out.DailyCap) {
			out.DailyCap = c.Daily
		}
		return out
	}
	return out
}

// ParseAgentCaps reads repeated name=amount values, such as "research=0.02 AETH".
func ParseAgentCaps(raw []string, assets *wallet.Assets) ([]AgentCap, error) {
	var out []AgentCap
	for _, s := range raw {
		name, amt, ok := strings.Cut(s, "=")
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("agent budget %q: want name=amount, such as research=0.02 AETH", s)
		}
		asset, v, err := assets.Parse(strings.TrimSpace(amt))
		if err != nil || asset.Denom != wallet.BaseDenom || !v.IsPositive() {
			return nil, fmt.Errorf("agent budget %q: give a positive AETH amount", s)
		}
		out = append(out, AgentCap{Name: strings.TrimSpace(name), Daily: v})
	}
	return out, nil
}
