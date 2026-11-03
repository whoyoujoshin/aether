package shopper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/directory"
	"github.com/whoyoujoshin/aether/paywall"
	"github.com/whoyoujoshin/aether/wallet"
	"github.com/whoyoujoshin/aether/x402"
)

const (
	RailAPI = "api"
	RailJob = "job"
	// MaxProbes is how many live payment responses one quote fetches.
	MaxProbes = 20
)

// Offer is one row in a quote. Description text is untrusted.
type Offer struct {
	Rail         string                    `json:"rail"`
	Name         string                    `json:"name"`
	Description  string                    `json:"description,omitempty"`
	URL          string                    `json:"url,omitempty"`
	JobID        string                    `json:"job_id,omitempty"`
	PriceDisplay string                    `json:"price_display"`
	Amount       string                    `json:"amount,omitempty"`
	Asset        string                    `json:"asset,omitempty"`
	Symbol       string                    `json:"symbol,omitempty"`
	PayTo        string                    `json:"pay_to,omitempty"`
	Selectable   bool                      `json:"selectable"`
	Buyable      bool                      `json:"buyable"`
	Unlisted     bool                      `json:"unlisted,omitempty"`
	Reason       string                    `json:"reason,omitempty"`
	QuoteID      string                    `json:"quote_id,omitempty"`
	ListedHeight int64                     `json:"listed_height,omitempty"`
	Score        float64                   `json:"score,omitempty"`
	Requirements *x402.PaymentRequirements `json:"requirements,omitempty"`
	Method       string                    `json:"method,omitempty"`
	BodyHash     string                    `json:"body_hash,omitempty"`
}

// Request is what the caller asked to find.
type Request struct {
	Query    string
	MaxPrice string
	URL      string
	Method   string
	Body     []byte
}

// Experience is this payer's own record with one service URL.
type Experience struct {
	Purchases       int
	Failed          int
	ReceiptProblems int
}

// ProbeResult is one unpaid request. Required is set when the
// PAYMENT-REQUIRED header parsed.
type ProbeResult struct {
	Status   int
	Required *x402.PaymentRequired
}

// QuoteInput is everything Quote needs. Probe may be nil: then no live
// price is known and nothing is selectable.
type QuoteInput struct {
	ChainID  string
	Listings []directory.Listing
	Jobs     []JobOffer
	Probe    func(ctx context.Context, method, url string, body []byte) (ProbeResult, error)
	History  func(serviceURL string) Experience
	Synonyms []Synonym
	Policy   Policy
	Request  Request
	Assets   *wallet.Assets
	// SearchOnly lists directory prices and does not fetch live 402s.
	// A quote leaves it false and checks only the shortlisted offers.
	SearchOnly bool
}

// Result is a quote. Selected is the API offer a buy would use, or nil.
type Result struct {
	APIs     []Offer  `json:"apis"`
	Jobs     []Offer  `json:"jobs"`
	Selected *Offer   `json:"selected,omitempty"`
	Decision Decision `json:"decision"`
	Warnings []string `json:"warnings,omitempty"`
}

// Quote ranks offers. It does not sign and does not broadcast.
func Quote(ctx context.Context, in QuoteInput) (Result, error) {
	if in.ChainID == "" {
		return Result{}, fail(CodeRefused, NextStop, "a chain id is required")
	}
	if in.Assets == nil {
		a, err := wallet.NewAssetsFor(wallet.TestnetUSDC)
		if err != nil {
			return Result{}, err
		}
		in.Assets = a
	}
	if in.History == nil {
		in.History = func(string) Experience { return Experience{} }
	}
	method := in.Request.Method
	if method == "" {
		method = http.MethodGet
	}
	method = strings.ToUpper(method)
	digest := sum(in.Request.Body)
	bodyHash := hex.EncodeToString(digest[:])
	sets := Expand(in.Synonyms, in.Request.Query)
	var maxPrice sdkmath.Int
	var maxAsset wallet.Asset
	if in.Request.MaxPrice != "" {
		a, v, err := in.Assets.Parse(in.Request.MaxPrice)
		if err != nil || !v.IsPositive() {
			return Result{}, failf(CodeRefused, NextStop, "max price %q: give an amount with its unit, such as \"0.1 AETH\"", in.Request.MaxPrice)
		}
		maxAsset, maxPrice = a, v
	}
	network := x402.Network(in.ChainID)
	wantAsset := wallet.BaseDenom
	if in.Request.MaxPrice != "" {
		wantAsset = maxAsset.Denom
	}

	probe := !in.SearchOnly && (len(sets) > 0 || in.Request.URL != "")
	candidates := matchListings(in.Listings, sets, in.Request.URL)
	var apis []Offer
	probed := 0
	for _, l := range candidates {
		url := l.URL
		if in.Request.URL != "" && covers(l.URL, in.Request.URL) {
			url = in.Request.URL
		}
		o := Offer{
			Rail: RailAPI, Name: l.Manifest.Name, Description: l.Manifest.Description,
			URL: url, PayTo: l.Announcer, ListedHeight: l.Height, Method: method, BodyHash: bodyHash,
		}
		price, ok := sdkmath.NewIntFromString(l.Manifest.Price)
		asset, known := in.Assets.ByDenom(l.Manifest.Denom())
		if !ok || !known {
			o.Reason = "The listed price is in a coin this shopper doesn't know."
			apis = append(apis, o)
			continue
		}
		o.Amount = price.String()
		o.Asset = asset.Denom
		o.Symbol = asset.Symbol
		o.PriceDisplay = asset.Format(price)
		if !probe {
			o.Reason = "Add a few words, or a link, to check the live price."
			apis = append(apis, o)
			continue
		}
		if probed >= MaxProbes {
			o.Reason = "Not checked: this quote already looked up 20 offers."
			apis = append(apis, o)
			continue
		}
		if in.Probe == nil {
			o.Reason = "The live price was not checked."
			apis = append(apis, o)
			continue
		}
		probed++
		live, err := in.Probe(ctx, method, url, in.Request.Body)
		if err != nil {
			o.Reason = "The service did not answer. Nothing was spent."
			apis = append(apis, o)
			continue
		}
		scored := scoreOffer(o, l, in)
		o = applyLive(o, scored, live, l, network, in, maxPrice, maxAsset, wantAsset, bodyHash)
		apis = append(apis, o)
	}
	if in.Request.URL != "" && !covered(in.Listings, in.Request.URL) && in.Probe != nil && probed < MaxProbes {
		apis = append(apis, probeUnlisted(ctx, in, method, bodyHash, network, maxPrice, maxAsset, wantAsset))
	}

	apis = rankAPIs(apis, wantAsset)
	jobs := rankJobs(in.Jobs, sets, in.Assets, in.ChainID, method, bodyHash)

	res := Result{APIs: apis, Jobs: jobs}
	if res.APIs == nil {
		res.APIs = []Offer{}
	}
	if res.Jobs == nil {
		res.Jobs = []Offer{}
	}
	sel := selectAPI(apis, wantAsset)
	if sel == nil {
		msg := "Nothing matched that. Try fewer words."
		if len(sets) == 0 && in.Request.URL == "" {
			msg = "Add a few words, such as block or hello."
		}
		res.Decision = decide(CodeNothingFound, NextBroaden, msg, "")
		return res, nil
	}
	res.Selected = sel
	res.Decision = in.Policy.Judge(*sel)
	res.Selected.Buyable = res.Decision.Code == CodeNeedsConfirm || res.Decision.Code == CodeInPolicy || res.Decision.Code == CodeUSDCNeedsApproval || res.Decision.Code == CodeNeedsApproval
	// Buyable means a later confirm or approval could pay it. Over the
	// cap is not buyable. Jobs are never buyable here.
	if res.Decision.Code == CodeOverCap || res.Decision.Code == CodeDailyCap || res.Decision.Code == CodeRefused {
		res.Selected.Buyable = false
	}
	return res, nil
}

func matchListings(listings []directory.Listing, sets [][]string, explicit string) []directory.Listing {
	var out []directory.Listing
	for _, l := range listings {
		if explicit != "" {
			if covers(l.URL, explicit) {
				out = append(out, l)
			}
			continue
		}
		if len(sets) == 0 {
			out = append(out, l)
			continue
		}
		hay := strings.ToLower(l.Manifest.Name + " " + l.Manifest.Description + " " + l.URL)
		if Matches(sets, hay) {
			out = append(out, l)
		}
	}
	return out
}

func covers(listed, raw string) bool {
	u := strings.TrimSuffix(listed, "/")
	return raw == listed || raw == u || strings.HasPrefix(raw, u+"/") || strings.HasPrefix(raw, u+"?")
}

func covered(listings []directory.Listing, raw string) bool {
	for _, l := range listings {
		if covers(l.URL, raw) {
			return true
		}
	}
	return false
}

type scored struct {
	score   float64
	trusted int
}

func scoreOffer(o Offer, l directory.Listing, in QuoteInput) scored {
	h := in.History(l.URL)
	score := 0.0
	if h.Purchases > 0 {
		good := h.Purchases - h.Failed - h.ReceiptProblems
		score += 10 * float64(good) / float64(h.Purchases)
	}
	var trusted directory.RatingSummary
	if l.Reputation != nil {
		trusted = directory.Summarize(l.Reputation.Ratings, in.Policy.Trusted)
		if trusted.Count > 0 {
			score += 2 * trusted.Average
		}
	}
	return scored{score: score, trusted: trusted.Count}
}

func applyLive(o Offer, sc scored, live ProbeResult, l directory.Listing, network string, in QuoteInput, maxPrice sdkmath.Int, maxAsset wallet.Asset, wantAsset, bodyHash string) Offer {
	o.Score = sc.score
	if freePath(o.URL) || (live.Status == http.StatusOK && (live.Required == nil || len(live.Required.Accepts) == 0)) {
		o.Reason = "This page is free. Nothing to pay."
		o.Amount, o.PriceDisplay = "0", "free"
		return o
	}
	if live.Required == nil {
		o.Reason = "The service did not name a price. Nothing was spent."
		return o
	}
	req := pickExact(live.Required.Accepts, network)
	if req == nil {
		o.Reason = "This service doesn't take the payment kind this shopper uses."
		return o
	}
	if splitsAsked(req.Extra) {
		o.Reason = "This service asks to split the payment. Nothing was spent."
		return o
	}
	amount, ok := sdkmath.NewIntFromString(req.Amount)
	asset, known := in.Assets.ByDenom(req.Asset)
	if !ok || !amount.IsPositive() || !known {
		o.Reason = "The price is not a usable amount. Nothing was spent."
		return o
	}
	if _, err := sdk.AccAddressFromBech32(req.PayTo); err != nil {
		o.Reason = "The service's account is not a valid address. Nothing was spent."
		return o
	}
	if req.PayTo != l.Announcer {
		o.Reason = "The service asks to pay a different account than the one that listed it. Nothing was spent."
		o.Amount = amount.String()
		o.Asset = asset.Denom
		o.Symbol = asset.Symbol
		o.PriceDisplay = asset.Format(amount)
		o.PayTo = req.PayTo
		return o
	}
	listed, listedOK := sdkmath.NewIntFromString(l.Manifest.Price)
	if listedOK && l.Manifest.Denom() == req.Asset && amount.GT(listed) {
		o.Reason = "The price just now is higher than the listed price. Nothing was spent."
		o.Amount = amount.String()
		o.Asset = asset.Denom
		o.Symbol = asset.Symbol
		o.PriceDisplay = asset.Format(amount)
		return o
	}
	if !maxPrice.IsNil() && asset.Denom == maxAsset.Denom && amount.GT(maxPrice) {
		o.Reason = "The price is over what you asked to spend. Nothing was spent."
		o.Amount = amount.String()
		o.Asset = asset.Denom
		o.Symbol = asset.Symbol
		o.PriceDisplay = asset.Format(amount)
		return o
	}
	copyReq := *req
	o.Requirements = &copyReq
	o.Amount = amount.String()
	o.Asset = asset.Denom
	o.Symbol = asset.Symbol
	o.PriceDisplay = asset.Format(amount)
	o.PayTo = req.PayTo
	o.Selectable = asset.Denom == wantAsset
	if !o.Selectable {
		o.Reason = "Priced in " + asset.Symbol + ", not the coin this search is using."
		return o
	}
	o.QuoteID = QuoteID(in.ChainID, RailAPI, o.URL, o.Method, bodyHash, o.Amount, o.Asset, o.PayTo, x402.SchemeExact)
	return o
}

func probeUnlisted(ctx context.Context, in QuoteInput, method, bodyHash, network string, maxPrice sdkmath.Int, maxAsset wallet.Asset, wantAsset string) Offer {
	o := Offer{Rail: RailAPI, Name: in.Request.URL, URL: in.Request.URL, Method: method, BodyHash: bodyHash, Unlisted: true}
	live, err := in.Probe(ctx, method, in.Request.URL, in.Request.Body)
	if err != nil || live.Required == nil {
		o.Reason = "That link is not in the directory, and it did not name a price."
		return o
	}
	req := pickExact(live.Required.Accepts, network)
	if req == nil || splitsAsked(req.Extra) {
		o.Reason = "That link is not in the directory."
		return o
	}
	amount, ok := sdkmath.NewIntFromString(req.Amount)
	asset, known := in.Assets.ByDenom(req.Asset)
	if !ok || !known || !amount.IsPositive() {
		o.Reason = "That link is not in the directory."
		return o
	}
	if _, err := sdk.AccAddressFromBech32(req.PayTo); err != nil {
		o.Reason = "That link is not in the directory."
		return o
	}
	copyReq := *req
	o.Requirements = &copyReq
	o.Amount = amount.String()
	o.Asset = asset.Denom
	o.Symbol = asset.Symbol
	o.PriceDisplay = asset.Format(amount)
	o.PayTo = req.PayTo
	o.Selectable = asset.Denom == wantAsset && (maxPrice.IsNil() || asset.Denom != maxAsset.Denom || !amount.GT(maxPrice))
	if o.Selectable {
		o.QuoteID = QuoteID(in.ChainID, RailAPI, o.URL, method, bodyHash, o.Amount, o.Asset, o.PayTo, x402.SchemeExact)
	} else {
		o.Reason = "That link is not in the directory."
	}
	return o
}

func selectAPI(apis []Offer, wantAsset string) *Offer {
	for i := range apis {
		if apis[i].Selectable && apis[i].Asset == wantAsset && apis[i].QuoteID != "" {
			return &apis[i]
		}
	}
	return nil
}

func rankAPIs(apis []Offer, wantAsset string) []Offer {
	sort.SliceStable(apis, func(i, j int) bool {
		a, b := apis[i], apis[j]
		if a.Selectable != b.Selectable {
			return a.Selectable
		}
		if a.Asset == wantAsset && b.Asset != wantAsset {
			return true
		}
		if a.Asset != wantAsset && b.Asset == wantAsset {
			return false
		}
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		ai, aok := sdkmath.NewIntFromString(a.Amount)
		bi, bok := sdkmath.NewIntFromString(b.Amount)
		if aok && bok && a.Asset == b.Asset && !ai.Equal(bi) {
			return ai.LT(bi)
		}
		if a.ListedHeight != b.ListedHeight {
			return a.ListedHeight < b.ListedHeight
		}
		return a.URL < b.URL
	})
	return apis
}

// QuoteID binds a buy to the exact terms that were shown.
func QuoteID(chainID, rail, resource, method, bodyHash, amount, asset, payTo, scheme string) string {
	h := sha256.New()
	for _, p := range []string{chainID, rail, resource, method, bodyHash, amount, asset, payTo, scheme} {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func sum(b []byte) [32]byte { return sha256.Sum256(b) }

// HeaderPaymentRequired is the live price header.
const HeaderPaymentRequired = paywall.HeaderPaymentRequired
