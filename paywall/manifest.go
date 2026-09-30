package paywall

import (
	"encoding/json"
	"net/http"

	"github.com/whoyoujoshin/aether/wallet"
)

// ManifestPath is where a paid service describes itself, so agents
// (and package directory) can learn what it sells without paying.
const ManifestPath = "/.well-known/x402"

// Manifest is a paid service's self-description.
type Manifest struct {
	X402Version int    `json:"x402Version"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Network     string `json:"network"`
	PayTo       string `json:"payTo"`
	Price       string `json:"price"` // per request, in the asset's base unit
	PriceAeth   string `json:"priceAeth,omitempty"`
	// Asset is the denom prices are in; Symbol and PriceAmount state the
	// price in its own unit.
	Asset       string   `json:"asset,omitempty"`
	Symbol      string   `json:"symbol,omitempty"`
	PriceAmount string   `json:"priceAmount,omitempty"`
	Schemes     []string `json:"schemes"`
	MinDeposit  string   `json:"minDeposit,omitempty"` // uaeth, aether-prepaid
	// WithdrawPath is set if unspent prepaid balances can be withdrawn.
	WithdrawPath string `json:"withdrawPath,omitempty"`
}

// Denom is the denom the manifest's amounts are in: uaeth for a
// manifest from before other assets.
func (m Manifest) Denom() string {
	if m.Asset == "" {
		return Asset
	}
	return m.Asset
}

// Manifest describes this paywall.
func (p *Paywall) Manifest(name, description string) Manifest {
	m := Manifest{
		X402Version: X402Version, Name: name, Description: description, Network: p.cfg.Network,
		PayTo: p.cfg.PayTo, Price: p.cfg.Price.String(), Schemes: p.schemes(),
		Asset: p.cfg.Asset.Denom, Symbol: p.cfg.Asset.Symbol, PriceAmount: p.cfg.Asset.Decimal(p.cfg.Price),
	}
	if p.cfg.Asset.Denom == Asset {
		m.PriceAeth = wallet.FormatAeth(p.cfg.Price)
	}
	if p.cfg.Prepaid != nil {
		m.MinDeposit = p.cfg.Prepaid.MinDeposit.String()
		if p.cfg.Prepaid.Payout != nil {
			m.WithdrawPath = WithdrawPath
		}
	}
	return m
}

// ManifestHandler serves the manifest (free) at ManifestPath.
func (p *Paywall) ManifestHandler(name, description string) http.Handler {
	bz, _ := json.Marshal(p.Manifest(name, description))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "use GET", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "max-age=300")
		_, _ = w.Write(bz)
	})
}
