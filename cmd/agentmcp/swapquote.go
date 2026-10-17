package main

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"cosmossdk.io/math"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/whoyoujoshin/aether/wallet"
)

// quote_swap: what a given amount of AETH would fetch in OSMO from the
// AETH/OSMO pool on Osmosis testnet (docs/OSMOSIS-POOL.md), from
// Osmosis's own swap estimate, which includes its taker fee as well as
// the pool's swap fee. Read-only: it signs nothing and moves nothing.
// AETH -> OSMO only, the direction contracts/aether-swap swaps.

// Testnet defaults (aether-testnet-1). The two channel ends are the
// path both denoms are derived from.
const (
	testnetOsmosisPool         = 1370
	testnetOsmosisChannel      = "channel-3"     // on Aether
	testnetOsmosisCounterparty = "channel-11852" // on Osmosis
	osmosisChainID             = "osmo-test-5"
)

// testnetOsmosisREST are tried in order. The first is the one the pool
// probe used from sync3.
var testnetOsmosisREST = []string{"https://lcd.osmotest5.osmosis.zone", "https://lcd.testnet.osmosis.zone"}

var (
	// osmosisREST is --osmosis-rest: "" picks the testnet defaults on
	// aether-testnet-1, "off" turns quote_swap off.
	osmosisREST string
	osmosisPool uint64

	osmosisHTTP = &http.Client{Timeout: 10 * time.Second}
)

const (
	quoteTTL            = 10 * time.Second
	quoteUpstreamPerMin = 30 // Osmosis's public endpoints ban callers that poll hard
	quoteDefaultSlip    = 1.0
	quoteMaxSlip        = 50.0
)

// osmosisRESTList is --osmosis-rest resolved: nil means quotes are off.
func osmosisRESTList() []string {
	switch osmosisREST {
	case "off":
		return nil
	case "":
		if chainID == "aether-testnet-1" {
			return testnetOsmosisREST
		}
		return nil
	}
	var out []string
	for _, u := range strings.Split(osmosisREST, ",") {
		if u = strings.TrimRight(strings.TrimSpace(u), "/"); u != "" {
			out = append(out, u)
		}
	}
	return out
}

func osmosisPoolID() uint64 {
	if osmosisPool != 0 {
		return osmosisPool
	}
	return testnetOsmosisPool
}

func ibcDenom(port, channel, base string) string {
	return fmt.Sprintf("ibc/%X", sha256.Sum256([]byte(port+"/"+channel+"/"+base)))
}

// aethOnOsmosis is AETH's denom on Osmosis; osmoOnAether is OSMO's on Aether.
var (
	aethOnOsmosis = ibcDenom("transfer", testnetOsmosisCounterparty, wallet.BaseDenom)
	osmoOnAether  = ibcDenom("transfer", testnetOsmosisChannel, "uosmo")
)

type quoteSwapInput struct {
	Amount          string  `json:"amount" jsonschema:"the AETH to swap, with its unit: \"1 AETH\" or \"1000000uaeth\""`
	SlippagePercent float64 `json:"slippagePercent,omitempty" jsonschema:"how far below this quote a swap may fill, for minOutput (default 1, at most 50)"`
}

type osmoAmountDTO struct {
	OSMO     string `json:"osmo" jsonschema:"in OSMO"`
	Uosmo    string `json:"uosmo" jsonschema:"in uosmo (1 OSMO = 1,000,000)"`
	OnAether string `json:"denomOnAether" jsonschema:"the denom it arrives as on Aether"`
}

type poolReservesDTO struct {
	AETH string `json:"aeth" jsonschema:"AETH in the pool"`
	OSMO string `json:"osmo" jsonschema:"OSMO in the pool"`
}

type quoteSwapOutput struct {
	Chain           string          `json:"chain" jsonschema:"where the pool is: Osmosis testnet"`
	Pool            uint64          `json:"pool"`
	In              amountDTO       `json:"in"`
	Out             osmoAmountDTO   `json:"out" jsonschema:"Osmosis's estimate of what the swap returns, after its taker fee and the pool's swap fee"`
	SpotPrice       string          `json:"spotPrice" jsonschema:"OSMO per AETH before this swap, from the reserves"`
	Price           string          `json:"price" jsonschema:"OSMO per AETH this swap gets"`
	CostPercent     string          `json:"costPercent" jsonschema:"how far price is below spotPrice: fees and price impact together"`
	SwapFeePercent  string          `json:"swapFeePercent" jsonschema:"the pool's own fee; Osmosis's taker fee comes on top"`
	Reserves        poolReservesDTO `json:"reserves"`
	SlippagePercent string          `json:"slippagePercent"`
	MinOutput       osmoAmountDTO   `json:"minOutput" jsonschema:"the least to accept: the estimate less slippagePercent. A swap set to this fails (and refunds) rather than fill below it"`
	QuotedAt        string          `json:"quotedAt"`
	Note            string          `json:"note"`
}

func toolQuoteSwap(ctx context.Context, _ *mcp.CallToolRequest, in quoteSwapInput) (*mcp.CallToolResult, quoteSwapOutput, error) {
	out, err := quoteSwap(ctx, in)
	return nil, out, err
}

func quoteSwap(ctx context.Context, in quoteSwapInput) (quoteSwapOutput, error) {
	bases := osmosisRESTList()
	if len(bases) == 0 {
		return quoteSwapOutput{}, newError(codeSwapNotConfigured, "quote_swap is off on this server (no --osmosis-rest, or not aether-testnet-1)")
	}
	a, amount, err := parseAssetAmount(in.Amount)
	if err != nil {
		return quoteSwapOutput{}, err
	}
	if a.Denom != wallet.BaseDenom {
		return quoteSwapOutput{}, newError(codeInvalidAmount, "quote_swap quotes AETH into OSMO; amount must be AETH, e.g. \"1 AETH\"")
	}
	if !amount.IsPositive() {
		return quoteSwapOutput{}, newError(codeInvalidAmount, "amount must be more than zero")
	}
	slip := in.SlippagePercent
	if slip == 0 {
		slip = quoteDefaultSlip
	}
	if slip < 0 || slip > quoteMaxSlip {
		return quoteSwapOutput{}, newError(codeInvalidArgument, fmt.Sprintf("slippagePercent must be between 0 and %g", quoteMaxSlip))
	}

	q, err := osmosisQuotes.get(ctx, bases, osmosisPoolID(), amount)
	if err != nil {
		return quoteSwapOutput{}, err
	}
	return q.output(a, amount, slip), nil
}

// rawQuote is what Osmosis said, kept for quoteTTL.
type rawQuote struct {
	pool                  uint64
	reserveIn, reserveOut *big.Int
	weightIn, weightOut   *big.Int
	swapFee               *big.Rat
	out                   *big.Int
	at                    time.Time
}

func (q rawQuote) output(a wallet.Asset, amount math.Int, slipPercent float64) quoteSwapOutput {
	in := new(big.Rat).SetInt(amount.BigInt())
	out := new(big.Rat).SetInt(q.out)
	// Balancer spot price, OSMO per AETH: (Ro/Wo) / (Ri/Wi).
	spot := new(big.Rat).Quo(
		new(big.Rat).SetFrac(q.reserveOut, q.weightOut),
		new(big.Rat).SetFrac(q.reserveIn, q.weightIn),
	)
	price := new(big.Rat).Quo(out, in)
	cost := new(big.Rat).Sub(big.NewRat(1, 1), new(big.Rat).Quo(price, spot))
	cost.Mul(cost, big.NewRat(100, 1))

	slip, _ := new(big.Rat).SetString(strconv.FormatFloat(slipPercent, 'f', -1, 64))
	keep := new(big.Rat).Sub(big.NewRat(1, 1), new(big.Rat).Quo(slip, big.NewRat(100, 1)))
	minOut := new(big.Rat).Mul(out, keep)
	minUosmo := new(big.Int).Quo(minOut.Num(), minOut.Denom()) // floor

	return quoteSwapOutput{
		Chain:           osmosisChainID,
		Pool:            q.pool,
		In:              newAssetAmountDTO(a, amount),
		Out:             newOsmoAmount(q.out),
		SpotPrice:       spot.FloatString(6),
		Price:           price.FloatString(6),
		CostPercent:     cost.FloatString(2),
		SwapFeePercent:  new(big.Rat).Mul(q.swapFee, big.NewRat(100, 1)).FloatString(2),
		Reserves:        poolReservesDTO{AETH: wallet.FormatAeth(math.NewIntFromBigInt(q.reserveIn)), OSMO: formatMicro(q.reserveOut)},
		SlippagePercent: slip.FloatString(2),
		MinOutput:       newOsmoAmount(minUosmo),
		QuotedAt:        q.at.UTC().Format(time.RFC3339),
		Note: "A quote, not a promise: the pool moves with every trade, and it is small, so a large swap gets a poor price (watch costPercent). " +
			"Swapping from Aether goes through contracts/aether-swap on Osmosis and back over IBC, about 22 s; this server can quote it but not send it.",
	}
}

func newOsmoAmount(uosmo *big.Int) osmoAmountDTO {
	return osmoAmountDTO{OSMO: formatMicro(uosmo), Uosmo: uosmo.String(), OnAether: osmoOnAether}
}

// formatMicro writes a 6-decimal base amount in whole units, trailing
// zeros trimmed.
func formatMicro(n *big.Int) string {
	s := new(big.Rat).SetFrac(n, big.NewInt(1_000_000)).FloatString(6)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// quoteCache keeps answers for quoteTTL and caps how often this server
// asks Osmosis, whoever is calling.
type quoteCache struct {
	mu       sync.Mutex
	answers  map[string]rawQuote
	upstream []time.Time
}

var osmosisQuotes = &quoteCache{answers: map[string]rawQuote{}}

func (c *quoteCache) get(ctx context.Context, bases []string, pool uint64, amount math.Int) (rawQuote, error) {
	key := fmt.Sprintf("%d/%s", pool, amount)
	now := time.Now()
	c.mu.Lock()
	if q, ok := c.answers[key]; ok && now.Sub(q.at) < quoteTTL {
		c.mu.Unlock()
		return q, nil
	}
	for k, q := range c.answers {
		if now.Sub(q.at) >= quoteTTL {
			delete(c.answers, k)
		}
	}
	recent := c.upstream[:0]
	for _, t := range c.upstream {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	c.upstream = recent
	if len(c.upstream) >= quoteUpstreamPerMin {
		wait := time.Minute - now.Sub(c.upstream[0])
		c.mu.Unlock()
		e := newError(codeQuoteUnavailable, "too many quotes this minute; try again shortly")
		e.RetryAfterSeconds = int64(wait/time.Second) + 1
		return rawQuote{}, e
	}
	c.upstream = append(c.upstream, now)
	c.mu.Unlock()

	q, err := fetchQuote(ctx, bases, pool, amount)
	if err != nil {
		return rawQuote{}, err
	}
	c.mu.Lock()
	c.answers[key] = q
	c.mu.Unlock()
	return q, nil
}

// fetchQuote asks each base in turn until one answers both the pool and
// the estimate.
func fetchQuote(ctx context.Context, bases []string, pool uint64, amount math.Int) (rawQuote, error) {
	var last error
	for _, base := range bases {
		q, err := fetchQuoteFrom(ctx, base, pool, amount)
		if err == nil {
			return q, nil
		}
		if ae := (*agentError)(nil); errors.As(err, &ae) && ae.Code == codeInvalidAmount {
			return rawQuote{}, err // Osmosis refused this amount; another endpoint won't differ
		}
		last = err
	}
	return rawQuote{}, newError(codeQuoteUnavailable, fmt.Sprintf("no Osmosis endpoint answered: %v", last))
}

func fetchQuoteFrom(ctx context.Context, base string, pool uint64, amount math.Int) (rawQuote, error) {
	var p struct {
		Pool struct {
			Type       string `json:"@type"`
			PoolParams struct {
				SwapFee string `json:"swap_fee"`
			} `json:"pool_params"`
			PoolAssets []struct {
				Token struct {
					Denom  string `json:"denom"`
					Amount string `json:"amount"`
				} `json:"token"`
				Weight string `json:"weight"`
			} `json:"pool_assets"`
		} `json:"pool"`
	}
	if err := osmosisGet(ctx, fmt.Sprintf("%s/osmosis/poolmanager/v1beta1/pools/%d", base, pool), &p); err != nil {
		return rawQuote{}, err
	}
	if p.Pool.Type != "/osmosis.gamm.v1beta1.Pool" {
		return rawQuote{}, fmt.Errorf("pool %d is %q, not a balancer pool", pool, p.Pool.Type)
	}
	q := rawQuote{pool: pool, at: time.Now()}
	for _, pa := range p.Pool.PoolAssets {
		amt, ok1 := new(big.Int).SetString(pa.Token.Amount, 10)
		w, ok2 := new(big.Int).SetString(pa.Weight, 10)
		if !ok1 || !ok2 || w.Sign() <= 0 {
			return rawQuote{}, fmt.Errorf("pool %d: unreadable asset %q", pool, pa.Token.Denom)
		}
		switch pa.Token.Denom {
		case aethOnOsmosis:
			q.reserveIn, q.weightIn = amt, w
		case "uosmo":
			q.reserveOut, q.weightOut = amt, w
		}
	}
	if q.reserveIn == nil || q.reserveOut == nil || q.reserveIn.Sign() <= 0 || q.reserveOut.Sign() <= 0 {
		return rawQuote{}, fmt.Errorf("pool %d does not hold both AETH and OSMO", pool)
	}
	fee, ok := new(big.Rat).SetString(p.Pool.PoolParams.SwapFee)
	if !ok {
		return rawQuote{}, fmt.Errorf("pool %d: unreadable swap fee %q", pool, p.Pool.PoolParams.SwapFee)
	}
	q.swapFee = fee

	var est struct {
		TokenOutAmount string `json:"token_out_amount"`
	}
	u := fmt.Sprintf("%s/osmosis/poolmanager/v1beta1/%d/estimate/single_pool_swap_exact_amount_in?pool_id=%d&token_in=%s&token_out_denom=uosmo",
		base, pool, pool, url.QueryEscape(amount.String()+aethOnOsmosis))
	if err := osmosisGet(ctx, u, &est); err != nil {
		return rawQuote{}, err
	}
	out, ok := new(big.Int).SetString(est.TokenOutAmount, 10)
	if !ok || out.Sign() <= 0 {
		return rawQuote{}, newError(codeInvalidAmount, fmt.Sprintf("Osmosis estimates no OSMO for %s AETH", wallet.FormatAeth(amount)))
	}
	q.out = out
	return q, nil
}

// osmosisGet decodes a JSON answer. A 400 is Osmosis refusing the
// request (an amount the pool can't take); its message is passed on.
func osmosisGet(ctx context.Context, u string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := osmosisHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusBadRequest {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &e)
		return newError(codeInvalidAmount, "Osmosis refused the estimate: "+cmp.Or(e.Message, strings.TrimSpace(string(body))))
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", u, resp.StatusCode)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("%s: %w", u, err)
	}
	return nil
}
