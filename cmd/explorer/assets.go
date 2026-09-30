package main

import (
	"net/http"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/wallet"
)

// assets are the tokens the explorer names: AETH, and Noble USDC over
// --usdc-channel once it's set. Any other token is shown by its bare
// denom: one that only looks like USDC (it arrived some other way) is
// never labeled USDC.
var assets, _ = wallet.NewAssets("")

// --- GET /api/assets ---

type assetsResponse struct {
	Assets []wallet.Asset `json:"assets"`
}

func handleAssets(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, assetsResponse{Assets: assets.List()})
}

// coinDTO is an amount of one token, in its base units, with its asset
// when the explorer knows it.
type coinDTO struct {
	Denom  string `json:"denom"`
	Amount string `json:"amount"`
	// Symbol, Decimals and Origin are set only for a known asset.
	Symbol   string `json:"symbol,omitempty"`
	Decimals int    `json:"decimals,omitempty"`
	Origin   string `json:"origin,omitempty"`
}

// coinDTOs lists coins with their assets: AETH first (zero included, as
// the page always showed it), then the other known assets, then anything
// else by denom.
func coinDTOs(coins sdk.Coins) []coinDTO {
	var out []coinDTO
	for _, a := range assets.List() {
		if amt := coins.AmountOf(a.Denom); amt.IsPositive() || a.Denom == wallet.BaseDenom {
			out = append(out, coinDTO{Denom: a.Denom, Amount: amt.String(), Symbol: a.Symbol, Decimals: a.Decimals, Origin: a.Origin})
		}
	}
	for _, c := range coins {
		if _, ok := assets.ByDenom(c.Denom); !ok {
			out = append(out, coinDTO{Denom: c.Denom, Amount: c.Amount.String()})
		}
	}
	return out
}
