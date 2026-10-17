package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// poolAtCreation is Osmosis testnet's answer for pool 1370 on
// 2026-10-17, as the pool probe recorded it (docs/OSMOSIS-POOL.md).
const poolAtCreation = `{"pool":{"@type":"/osmosis.gamm.v1beta1.Pool","address":"osmo14fltplmxyrlsj20d34kxfc55ungs0smtyw3u7d7z9w3mlevkhz2qday49j","id":"1370","pool_params":{"swap_fee":"0.003000000000000000","exit_fee":"0.000000000000000000","smooth_weight_change_params":null},"future_pool_governor":"","total_shares":{"denom":"gamm/pool/1370","amount":"100000000000000000000"},"pool_assets":[{"token":{"denom":"ibc/8277E1557EB379889BA54DFD0F19EDCFF907E4B3414E6B99D098F241CAED3BE1","amount":"100000000"},"weight":"1073741824"},{"token":{"denom":"uosmo","amount":"40000000"},"weight":"1073741824"}],"total_weight":"2147483648"}}`

// estimateFor1AETH is the probe's quote for 1 AETH, which the first real
// swap then filled exactly: 394,472 uosmo.
const estimateFor1AETH = `{"token_out_amount":"394472"}`

// fakeOsmosis answers the two routes quote_swap reads and counts calls.
type fakeOsmosis struct {
	*httptest.Server
	calls    atomic.Int32
	tokenIn  atomic.Value
	estimate string
	status   int
}

func newFakeOsmosis(t *testing.T) *fakeOsmosis {
	f := &fakeOsmosis{estimate: estimateFor1AETH, status: http.StatusOK}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		if f.status != http.StatusOK {
			w.WriteHeader(f.status)
			w.Write([]byte(`{"code":3,"message":"token amount calculated (0) must be positive: invalid request","details":[]}`))
			return
		}
		switch {
		case r.URL.Path == "/osmosis/poolmanager/v1beta1/pools/1370":
			w.Write([]byte(poolAtCreation))
		case r.URL.Path == "/osmosis/poolmanager/v1beta1/1370/estimate/single_pool_swap_exact_amount_in":
			f.tokenIn.Store(r.URL.Query().Get("token_in"))
			if got := r.URL.Query().Get("token_out_denom"); got != "uosmo" {
				t.Errorf("token_out_denom = %q", got)
			}
			w.Write([]byte(f.estimate))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

// useOsmosis points quote_swap at bases with an empty cache.
func useOsmosis(t *testing.T, rest string) {
	prevREST, prevPool, prevCache := osmosisREST, osmosisPool, osmosisQuotes
	osmosisREST, osmosisPool, osmosisQuotes = rest, 0, &quoteCache{answers: map[string]rawQuote{}}
	t.Cleanup(func() { osmosisREST, osmosisPool, osmosisQuotes = prevREST, prevPool, prevCache })
}

func wantCode(t *testing.T, err error, code string) *agentError {
	t.Helper()
	var ae *agentError
	if !errors.As(err, &ae) || ae.Code != code {
		t.Fatalf("err = %v, want code %s", err, code)
	}
	return ae
}

func TestQuoteSwap_ReadsOsmosisEstimate(t *testing.T) {
	f := newFakeOsmosis(t)
	useOsmosis(t, f.URL)

	q, err := quoteSwap(context.Background(), quoteSwapInput{Amount: "1 AETH"})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.tokenIn.Load(); got != "1000000ibc/8277E1557EB379889BA54DFD0F19EDCFF907E4B3414E6B99D098F241CAED3BE1" {
		t.Errorf("token_in = %v", got)
	}
	checks := []struct{ name, got, want string }{
		{"chain", q.Chain, "osmo-test-5"},
		{"in", q.In.Base + q.In.Denom, "1000000uaeth"},
		{"out uosmo", q.Out.Uosmo, "394472"},
		{"out osmo", q.Out.OSMO, "0.394472"},
		{"arrives as", q.Out.OnAether, "ibc/47BD209179859CDE4A2806763D7189B6E6FE13A17880FE2B42DE1E6C1E329E23"},
		// 40 OSMO / 100 AETH, equal weights.
		{"spot", q.SpotPrice, "0.400000"},
		{"price", q.Price, "0.394472"},
		// 1 - 0.394472/0.4 = 1.382%: the 0.3% pool fee, Osmosis's 0.1%
		// taker fee and ~1% price impact in a 100 AETH pool.
		{"cost", q.CostPercent, "1.38"},
		{"swap fee", q.SwapFeePercent, "0.30"},
		{"reserves", q.Reserves.AETH + " / " + q.Reserves.OSMO, "100 / 40"},
		{"slippage", q.SlippagePercent, "1.00"},
		// floor(394472 * 0.99)
		{"min output", q.MinOutput.Uosmo, "390527"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
	if q.Pool != 1370 {
		t.Errorf("pool = %d", q.Pool)
	}

	q, err = quoteSwap(context.Background(), quoteSwapInput{Amount: "1000000uaeth", SlippagePercent: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	if q.MinOutput.Uosmo != "392499" { // floor(394472 * 0.995)
		t.Errorf("min output at 0.5%% = %s", q.MinOutput.Uosmo)
	}
}

func TestQuoteSwap_CachesAndPacesOsmosis(t *testing.T) {
	f := newFakeOsmosis(t)
	useOsmosis(t, f.URL)

	for range 3 {
		if _, err := quoteSwap(context.Background(), quoteSwapInput{Amount: "1 AETH"}); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.calls.Load(); n != 2 { // one pool read and one estimate, then the cache
		t.Errorf("Osmosis was asked %d times, want 2", n)
	}

	// A full minute of upstream calls: the next new amount waits.
	now := time.Now()
	for range quoteUpstreamPerMin {
		osmosisQuotes.upstream = append(osmosisQuotes.upstream, now)
	}
	_, err := quoteSwap(context.Background(), quoteSwapInput{Amount: "2 AETH"})
	ae := wantCode(t, err, codeQuoteUnavailable)
	if !ae.Retryable || ae.RetryAfterSeconds < 1 || ae.RetryAfterSeconds > 61 {
		t.Errorf("paced error = %+v", ae)
	}
	// The cached amount is still served.
	if _, err := quoteSwap(context.Background(), quoteSwapInput{Amount: "1 AETH"}); err != nil {
		t.Errorf("cached quote while paced: %v", err)
	}
}

func TestQuoteSwap_FallsBackToTheNextEndpoint(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden) // what Osmosis's RPCs answered the seed
	}))
	defer down.Close()
	f := newFakeOsmosis(t)
	useOsmosis(t, down.URL+", "+f.URL+"/")

	q, err := quoteSwap(context.Background(), quoteSwapInput{Amount: "1 AETH"})
	if err != nil {
		t.Fatal(err)
	}
	if q.Out.Uosmo != "394472" {
		t.Errorf("out = %s", q.Out.Uosmo)
	}
}

func TestQuoteSwap_Failures(t *testing.T) {
	t.Run("no endpoint answers", func(t *testing.T) {
		down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer down.Close()
		useOsmosis(t, down.URL)
		_, err := quoteSwap(context.Background(), quoteSwapInput{Amount: "1 AETH"})
		if ae := wantCode(t, err, codeQuoteUnavailable); !ae.Retryable {
			t.Error("QUOTE_UNAVAILABLE should be retryable")
		}
	})

	t.Run("Osmosis refuses the amount", func(t *testing.T) {
		f := newFakeOsmosis(t)
		f.status = http.StatusBadRequest
		other := newFakeOsmosis(t)
		useOsmosis(t, f.URL+","+other.URL)
		_, err := quoteSwap(context.Background(), quoteSwapInput{Amount: "1 AETH"})
		ae := wantCode(t, err, codeInvalidAmount)
		if !strings.Contains(ae.Message, "must be positive") {
			t.Errorf("message = %q", ae.Message)
		}
		if other.calls.Load() != 0 {
			t.Error("a refused amount was retried on the next endpoint")
		}
	})

	f := newFakeOsmosis(t)
	useOsmosis(t, f.URL)
	for _, c := range []struct {
		name string
		in   quoteSwapInput
		code string
	}{
		{"USDC", quoteSwapInput{Amount: "5 USDC"}, codeInvalidAmount},
		{"bare number", quoteSwapInput{Amount: "1"}, codeInvalidAmount},
		{"zero", quoteSwapInput{Amount: "0 AETH"}, codeInvalidAmount},
		{"slippage too high", quoteSwapInput{Amount: "1 AETH", SlippagePercent: 51}, codeInvalidArgument},
		{"negative slippage", quoteSwapInput{Amount: "1 AETH", SlippagePercent: -1}, codeInvalidArgument},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := quoteSwap(context.Background(), c.in)
			wantCode(t, err, c.code)
		})
	}
	if n := f.calls.Load(); n != 0 {
		t.Errorf("bad input reached Osmosis %d times", n)
	}

	t.Run("off", func(t *testing.T) {
		useOsmosis(t, "off")
		_, err := quoteSwap(context.Background(), quoteSwapInput{Amount: "1 AETH"})
		wantCode(t, err, codeSwapNotConfigured)
	})

	t.Run("default only on the testnet", func(t *testing.T) {
		useOsmosis(t, "")
		prev := chainID
		t.Cleanup(func() { chainID = prev })
		chainID = "aether-devnet"
		if got := osmosisRESTList(); got != nil {
			t.Errorf("devnet endpoints = %v", got)
		}
		chainID = "aether-testnet-1"
		if got := osmosisRESTList(); len(got) == 0 {
			t.Error("no testnet default")
		}
	})
}

// Over MCP, on the public read-only server: what an agent actually gets.
func TestQuoteSwap_OverMCP(t *testing.T) {
	f := newFakeOsmosis(t)
	useOsmosis(t, f.URL)

	ctx := context.Background()
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := newPublicServer().Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "quote-test"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "quote_swap", Arguments: map[string]any{"amount": "1 AETH", "slippagePercent": 2}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	bz, _ := json.Marshal(res.StructuredContent)
	var q quoteSwapOutput
	if err := json.Unmarshal(bz, &q); err != nil {
		t.Fatal(err)
	}
	// floor(394472 * 0.98)
	if q.Out.Uosmo != "394472" || q.MinOutput.Uosmo != "386582" || q.SlippagePercent != "2.00" {
		t.Errorf("quote = %s", bz)
	}

	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "quote_swap", Arguments: map[string]any{"amount": "5 USDC"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(fmt.Sprint(res.Content[0]), codeInvalidAmount) {
		t.Errorf("USDC: %+v", res.Content)
	}
}
