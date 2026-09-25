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
	X402Version int      `json:"x402Version"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Network     string   `json:"network"`
	PayTo       string   `json:"payTo"`
	Price       string   `json:"price"` // uaeth per request
	PriceAeth   string   `json:"priceAeth"`
	Schemes     []string `json:"schemes"`
	MinDeposit  string   `json:"minDeposit,omitempty"` // uaeth, aether-prepaid
}

// Manifest describes this paywall.
func (p *Paywall) Manifest(name, description string) Manifest {
	m := Manifest{
		X402Version: X402Version, Name: name, Description: description, Network: p.cfg.Network,
		PayTo: p.cfg.PayTo, Price: p.cfg.Price.String(), PriceAeth: wallet.FormatAeth(p.cfg.Price), Schemes: p.schemes(),
	}
	if p.cfg.Prepaid != nil {
		m.MinDeposit = p.cfg.Prepaid.MinDeposit.String()
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
