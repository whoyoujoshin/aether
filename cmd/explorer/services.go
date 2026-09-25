package main

import (
	"net/http"
	"sync"

	"github.com/whoyoujoshin/aether/directory"
	"github.com/whoyoujoshin/aether/wallet"
)

// --- GET /api/services ---
//
// Paid services listed in the on-chain directory (package directory),
// each verified against its own manifest. Manifests are fetched from
// URLs anyone can announce, so the fetcher refuses internal addresses:
// this server must not be usable to probe its own network.

var (
	chainID               string
	directoryAllowPrivate bool

	servicesOnce sync.Once
	services     *directory.Directory
)

type serviceDTO struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	URL         string   `json:"url"`
	Price       string   `json:"price"` // uaeth per request
	PriceAeth   string   `json:"priceAeth"`
	Schemes     []string `json:"schemes"`
	MinDeposit  string   `json:"minDeposit,omitempty"`
	PayTo       string   `json:"payTo"`
	Height      int64    `json:"listedAtHeight"`
	TxHash      string   `json:"txHash"`
}

func serviceDirectory() *directory.Directory {
	servicesOnce.Do(func() {
		services = &directory.Directory{
			Network: chainID,
			Fetch:   directory.SafeFetcher(directoryAllowPrivate),
			Scan: func() ([]wallet.IncomingPayment, error) {
				c, err := wallet.NewClient(grpcEndpoint)
				if err != nil {
					return nil, err
				}
				defer c.Close()
				return c.GetIncomingPayments(directory.Address(), 1, 5000)
			},
		}
	})
	return services
}

func handleServices(w http.ResponseWriter, r *http.Request) {
	listings, err := serviceDirectory().Listings(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	out := make([]serviceDTO, 0, len(listings))
	for _, l := range listings {
		out = append(out, serviceDTO{
			Name: l.Manifest.Name, Description: l.Manifest.Description, URL: l.URL,
			Price: l.Manifest.Price, PriceAeth: l.Manifest.PriceAeth, Schemes: l.Manifest.Schemes,
			MinDeposit: l.Manifest.MinDeposit, PayTo: l.Announcer, Height: l.Height, TxHash: l.TxHash,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"directoryAddress": directory.Address(), "services": out})
}
