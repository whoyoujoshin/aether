package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// clients/testdata/swapquote.json: Osmosis's recorded answers and what
// quote_swap makes of them. The TypeScript and Python clients test
// against the same file, so all three quote alike. UPDATE_SWAPQUOTE=1
// rewrites the expected outputs from this implementation.

type swapQuoteVector struct {
	Name     string          `json:"name"`
	Pool     json.RawMessage `json:"pool"`
	Estimate json.RawMessage `json:"estimate"`
	Input    quoteSwapInput  `json:"input"`
	Want     map[string]any  `json:"want"`
}

func TestQuoteSwap_SharedVectors(t *testing.T) {
	path := filepath.Join("..", "..", "clients", "testdata", "swapquote.json")
	bz, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Note    string            `json:"note"`
		Vectors []swapQuoteVector `json:"vectors"`
	}
	if err := json.Unmarshal(bz, &file); err != nil {
		t.Fatal(err)
	}
	for i, v := range file.Vectors {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/osmosis/poolmanager/v1beta1/pools/1370":
				w.Write(v.Pool)
			case "/osmosis/poolmanager/v1beta1/1370/estimate/single_pool_swap_exact_amount_in":
				w.Write(v.Estimate)
			default:
				http.NotFound(w, r)
			}
		}))
		useOsmosis(t, srv.URL)
		q, err := quoteSwap(context.Background(), v.Input)
		srv.Close()
		if err != nil {
			t.Fatalf("%s: %v", v.Name, err)
		}
		got := map[string]any{
			"out_uosmo": q.Out.Uosmo, "out_osmo": q.Out.OSMO, "denom_on_aether": q.Out.OnAether,
			"spot_price": q.SpotPrice, "price": q.Price, "cost_percent": q.CostPercent, "swap_fee_percent": q.SwapFeePercent,
			"reserves_aeth": q.Reserves.AETH, "reserves_osmo": q.Reserves.OSMO,
			"slippage_percent": q.SlippagePercent, "min_output_uosmo": q.MinOutput.Uosmo,
		}
		if os.Getenv("UPDATE_SWAPQUOTE") != "" {
			file.Vectors[i].Want = got
			continue
		}
		for k, want := range v.Want {
			if got[k] != want {
				t.Errorf("%s: %s = %v, want %v", v.Name, k, got[k], want)
			}
		}
		if len(v.Want) != len(got) {
			t.Errorf("%s: want has %d fields, got %d", v.Name, len(v.Want), len(got))
		}
	}
	if os.Getenv("UPDATE_SWAPQUOTE") != "" {
		out, _ := json.MarshalIndent(file, "", "  ")
		if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
