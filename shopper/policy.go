package shopper

import (
	"fmt"
	"strings"

	sdkmath "cosmossdk.io/math"

	"github.com/whoyoujoshin/aether/wallet"
	"github.com/whoyoujoshin/aether/x402"
)

// Policy is the owner's standing limit. Caps are hard: a confirm
// cannot raise them. Auto is off unless the owner set it.
type Policy struct {
	MaxPerPayment sdkmath.Int // uaeth
	DailyCap      sdkmath.Int // uaeth
	// USDCMax and USDCDaily are zero when USDC has no numeric ceiling
	// beyond "a person must approve". A positive value is a hard cap.
	USDCMax   sdkmath.Int
	USDCDaily sdkmath.Int
	USDCDenom string
	Auto      bool
	// AllowPayees empty means every current listing's announcer.
	AllowPayees map[string]bool
	// AllowURLs empty means any URL under an allowed listing.
	AllowURLPrefixes []string
	Payer            string
	Approver         string
	Trust            map[string]bool
	// Spent is base units already counted today, by denom.
	Spent map[string]sdkmath.Int
}

// DefaultCaps are the testnet defaults from the design.
func DefaultCaps(usdcDenom string) Policy {
	return Policy{
		MaxPerPayment: sdkmath.NewInt(10_000),  // 0.01 AETH
		DailyCap:      sdkmath.NewInt(100_000), // 0.1 AETH
		USDCMax:       sdkmath.NewInt(1_000_000),
		USDCDaily:     sdkmath.NewInt(5_000_000),
		USDCDenom:     usdcDenom,
		Spent:         map[string]sdkmath.Int{},
	}
}

// Trusted is the payer, the approver, and any address the owner added.
func (p Policy) Trusted(rater string) bool {
	if rater == "" {
		return false
	}
	if p.Payer != "" && rater == p.Payer {
		return true
	}
	if p.Approver != "" && rater == p.Approver {
		return true
	}
	return p.Trust[rater]
}

func (p Policy) spent(denom string) sdkmath.Int {
	if v, ok := p.Spent[denom]; ok && !v.IsNil() {
		return v
	}
	return sdkmath.ZeroInt()
}

// Judge applies the cap and the allowlist to one selectable API offer.
// Jobs are not buyable. Nothing here signs.
func (p Policy) Judge(o Offer) Decision {
	if o.Rail == RailJob {
		return decide(CodeNotBuyable, NextPickAnother, "Custom job, not ready to buy yet.", o.QuoteID)
	}
	if !o.Selectable {
		msg := o.Reason
		if msg == "" {
			msg = "This offer can't be bought."
		}
		return decide(CodeRefused, NextPickAnother, msg, o.QuoteID)
	}
	amount, ok := sdkmath.NewIntFromString(o.Amount)
	if !ok || !amount.IsPositive() {
		return decide(CodeRefused, NextPickAnother, "The price is not a usable amount. Nothing was spent.", o.QuoteID)
	}
	if o.Asset == wallet.BaseDenom {
		if amount.GT(p.MaxPerPayment) {
			return decide(CodeOverCap, NextPickCheaper,
				fmt.Sprintf("That costs %s, more than your %s limit. Nothing was spent.", o.PriceDisplay, wallet.AETH.Format(p.MaxPerPayment)),
				o.QuoteID)
		}
		if p.spent(wallet.BaseDenom).Add(amount).GT(p.DailyCap) {
			return decide(CodeDailyCap, NextWaitOrEdit,
				fmt.Sprintf("That would pass today's %s limit. Nothing was spent.", wallet.AETH.Format(p.DailyCap)),
				o.QuoteID)
		}
	} else if d := p.judgeUSDCCap(o, amount); d.Code != "" {
		return d
	}
	if o.Unlisted || !p.payeeAllowed(o.PayTo) || !p.urlAllowed(o.URL) {
		return decide(CodeNeedsApproval, NextOpenApproval,
			fmt.Sprintf("This is outside the accounts you allowed. It costs %s. Nothing is spent until you approve it.", o.PriceDisplay),
			o.QuoteID)
	}
	if o.Asset != wallet.BaseDenom {
		return decide(CodeUSDCNeedsApproval, NextOpenApproval,
			fmt.Sprintf("This one is priced in test USDC (%s). Approve it to continue. Nothing was spent.", o.PriceDisplay),
			o.QuoteID)
	}
	if p.Auto {
		return decide(CodeInPolicy, NextBuy,
			fmt.Sprintf("Within your limit. A buy pays %s.", o.PriceDisplay), o.QuoteID)
	}
	return decide(CodeNeedsConfirm, NextConfirm,
		fmt.Sprintf("%s is within your limit. Nothing is spent until you confirm.", o.PriceDisplay), o.QuoteID)
}

// judgeUSDCCap returns a hard refuse when a USDC price is over its
// ceiling. A zero Code means the amount is inside the ceiling and the
// caller still requires a person.
func (p Policy) judgeUSDCCap(o Offer, amount sdkmath.Int) Decision {
	if p.USDCDenom == "" || o.Asset != p.USDCDenom {
		return decide(CodeRefused, NextPickAnother, "This price is in a coin this shopper doesn't know. Nothing was spent.", o.QuoteID)
	}
	if !p.USDCMax.IsNil() && p.USDCMax.IsPositive() && amount.GT(p.USDCMax) {
		return decide(CodeOverCap, NextPickCheaper,
			fmt.Sprintf("That costs %s, over the USDC limit. Nothing was spent.", o.PriceDisplay), o.QuoteID)
	}
	if !p.USDCDaily.IsNil() && p.USDCDaily.IsPositive() && p.spent(p.USDCDenom).Add(amount).GT(p.USDCDaily) {
		return decide(CodeDailyCap, NextWaitOrEdit, "That would pass today's USDC limit. Nothing was spent.", o.QuoteID)
	}
	return Decision{}
}

func (p Policy) payeeAllowed(payTo string) bool {
	if len(p.AllowPayees) == 0 {
		return true
	}
	return p.AllowPayees[payTo]
}

func (p Policy) urlAllowed(raw string) bool {
	if len(p.AllowURLPrefixes) == 0 {
		return true
	}
	for _, pre := range p.AllowURLPrefixes {
		if raw == pre || strings.HasPrefix(raw, strings.TrimSuffix(pre, "/")+"/") {
			return true
		}
	}
	return false
}

// freePath is a URL a paywall serves without payment.
func freePath(raw string) bool {
	u := strings.ToLower(strings.TrimRight(raw, "/"))
	return strings.HasSuffix(u, "/.well-known/x402") || strings.HasSuffix(u, "/help")
}

// splitsAsked matches x402.hasSplits: a non-empty splits array, or a
// splits value that is not an array, is refused. An empty array is not.
func splitsAsked(extra map[string]any) bool {
	v, ok := extra["splits"]
	if !ok || v == nil {
		return false
	}
	arr, ok := v.([]any)
	return !ok || len(arr) > 0
}

// pickExact returns the exact requirement for network, if the 402 offers one.
func pickExact(accepts []x402.PaymentRequirements, network string) *x402.PaymentRequirements {
	for i, a := range accepts {
		if a.Scheme == x402.SchemeExact && a.Network == network {
			return &accepts[i]
		}
	}
	return nil
}
