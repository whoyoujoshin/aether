package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/whoyoujoshin/aether/directory"
	"github.com/whoyoujoshin/aether/shopper"
	"github.com/whoyoujoshin/aether/wallet"
	"github.com/whoyoujoshin/aether/x402"
)

// Shop search reads the on-chain directory cache. It does not fetch a
// live price from every paywall. A buy re-checks that one price.

type shopOfferDTO struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url"`
	Price       string `json:"price"`
	Amount      string `json:"amount,omitempty"`
	Asset       string `json:"asset,omitempty"`
	PayTo       string `json:"payTo,omitempty"`
	Buyable     bool   `json:"buyable"`
	Reason      string `json:"reason,omitempty"`
}

type shopSearchResponse struct {
	Query    string         `json:"query"`
	Services []shopOfferDTO `json:"services"`
	Note     string         `json:"note"`
}

type shopPriceResponse struct {
	URL          string                    `json:"url"`
	Price        string                    `json:"price,omitempty"`
	Amount       string                    `json:"amount,omitempty"`
	Asset        string                    `json:"asset,omitempty"`
	PayTo        string                    `json:"payTo,omitempty"`
	Requirements *x402.PaymentRequirements `json:"requirements,omitempty"`
	Code         string                    `json:"code"`
	Message      string                    `json:"message"`
}

func handleShopSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("use GET"))
		return
	}
	listings, err := serviceDirectory().Listings(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	res, err := shopper.Quote(r.Context(), shopper.QuoteInput{
		ChainID: chainID, Listings: listings, SearchOnly: true, Assets: assets,
		Policy: shopper.DefaultCaps(usdcDenomOf(assets)), Request: shopper.Request{Query: q},
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	out := shopSearchResponse{Query: q, Services: []shopOfferDTO{}, Note: "Listed prices. The live price is checked when you buy, for that one service."}
	for _, o := range res.APIs {
		out.Services = append(out.Services, shopOfferDTO{
			Name: o.Name, Description: o.Description, URL: o.URL, Price: o.PriceDisplay,
			Amount: o.Amount, Asset: o.Asset, PayTo: o.PayTo, Buyable: false, Reason: o.Reason,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func handleShopPrice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("use GET"))
		return
	}
	raw := strings.TrimSpace(r.URL.Query().Get("url"))
	if _, err := url.ParseRequestURI(raw); err != nil || raw == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("url must be an http(s) link"))
		return
	}
	listings, err := serviceDirectory().Listings(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	var match *directory.Listing
	for i := range listings {
		if shopCovers(listings[i].URL, raw) {
			match = &listings[i]
			break
		}
	}
	if match == nil {
		writeJSON(w, http.StatusNotFound, shopPriceResponse{URL: raw, Code: "NOT_LISTED", Message: "That service is not in the directory. Nothing was spent."})
		return
	}
	client := directory.SafeHTTPClient(directoryAllowPrivate, 15*time.Second)
	res, err := shopper.Quote(r.Context(), shopper.QuoteInput{
		ChainID: chainID, Listings: []directory.Listing{*match}, Assets: assets,
		Policy:  shopper.DefaultCaps(usdcDenomOf(assets)),
		Request: shopper.Request{URL: raw},
		Probe: func(ctx context.Context, method, u string, body []byte) (shopper.ProbeResult, error) {
			return shopper.ProbePayment(ctx, client, method, u, body)
		},
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if res.Selected == nil {
		msg := "The live price could not be used."
		if len(res.APIs) > 0 && res.APIs[0].Reason != "" {
			msg = res.APIs[0].Reason
		}
		writeJSON(w, http.StatusConflict, shopPriceResponse{URL: raw, Code: "PRICE_CHANGED", Message: msg})
		return
	}
	o := res.Selected
	writeJSON(w, http.StatusOK, shopPriceResponse{
		URL: raw, Price: o.PriceDisplay, Amount: o.Amount, Asset: o.Asset, PayTo: o.PayTo,
		Requirements: o.Requirements,
		Code:         res.Decision.Code, Message: res.Decision.Message,
	})
}

func shopCovers(listed, raw string) bool {
	u := strings.TrimSuffix(listed, "/")
	return raw == listed || raw == u || strings.HasPrefix(raw, u+"/") || strings.HasPrefix(raw, u+"?")
}

func usdcDenomOf(a *wallet.Assets) string {
	if a == nil {
		return ""
	}
	if u, ok := a.BySymbol("USDC"); ok {
		return u.Denom
	}
	return ""
}
