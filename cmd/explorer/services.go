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
	Name        string       `json:"name"`
	Description string       `json:"description"`
	URL         string       `json:"url"`
	Price       string       `json:"price"` // uaeth per request
	PriceAeth   string       `json:"priceAeth"`
	Schemes     []string     `json:"schemes"`
	MinDeposit  string       `json:"minDeposit,omitempty"`
	PayTo       string       `json:"payTo"`
	Height      int64        `json:"listedAtHeight"`
	TxHash      string       `json:"txHash"`
	Activity    *activityDTO `json:"activity,omitempty"`
}

// activityDTO is what the chain shows of a service's use. A seller can
// pay itself from other accounts for free, so it's a hint, not proof;
// agents weigh ratings by accounts they trust.
type activityDTO struct {
	WindowBlocks int64   `json:"windowBlocks"`
	Payments     int     `json:"payments"`
	Payers       int     `json:"payers"`
	VolumeAeth   string  `json:"volumeAeth"`
	Ratings      int     `json:"ratings"`
	AverageScore float64 `json:"averageScore,omitempty"`
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
			ScanPayee: func(address string, since int64) ([]wallet.IncomingPayment, error) {
				c, err := wallet.NewClient(grpcEndpoint)
				if err != nil {
					return nil, err
				}
				defer c.Close()
				return c.GetIncomingPayments(address, since, 5000)
			},
			LatestHeight: func() (int64, error) {
				c, err := wallet.NewClient(grpcEndpoint)
				if err != nil {
					return 0, err
				}
				defer c.Close()
				return c.GetLatestHeight()
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
		s := serviceDTO{
			Name: l.Manifest.Name, Description: l.Manifest.Description, URL: l.URL,
			Price: l.Manifest.Price, PriceAeth: l.Manifest.PriceAeth, Schemes: l.Manifest.Schemes,
			MinDeposit: l.Manifest.MinDeposit, PayTo: l.Announcer, Height: l.Height, TxHash: l.TxHash,
		}
		if r := l.Reputation; r != nil {
			sum := directory.Summarize(r.Ratings, nil)
			s.Activity = &activityDTO{WindowBlocks: directory.DefaultWindow, Payments: r.Stats.Payments, Payers: r.Stats.Payers,
				VolumeAeth: wallet.FormatAeth(r.Stats.Volume), Ratings: sum.Count, AverageScore: sum.Average}
		}
		out = append(out, s)
	}
	writeJSON(w, http.StatusOK, map[string]any{"directoryAddress": directory.Address(), "services": out})
}
