package wallet

import (
	"context"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	sdkmath "cosmossdk.io/math"
	nodeservice "github.com/cosmos/cosmos-sdk/client/grpc/node"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// FeeSetting is how a tool pays for gas. Every node asks a minimum price
// per gas (minimum-gas-prices; on aether-1 0.0001uaeth, or 0.0005 in the
// USDC voucher, docs/MAINNET.md) and refuses a transaction whose fee
// doesn't cover it; anything over the minimum is a tip, which goes first
// when blocks are full (app/tipping.go). A lone native mining submission
// needs no fee at any price (app/mining_fee.go).
type FeeSetting struct {
	// GasPrice is "auto", the node's own minimum (asked once a minute),
	// or a price list such as 0.0001uaeth or 0.0001uaeth,0.0005ibc/....
	GasPrice string
	// Tip multiplies the price: 1 pays the minimum, 2 pays twice it.
	Tip float64
}

// AutoFees pays the node's minimum: what every tool does unless told
// otherwise.
var AutoFees = FeeSetting{GasPrice: "auto", Tip: 1}

// FeeFlags adds --gas-price and --tip to fs. Read the setting after fs is
// parsed.
func FeeFlags(fs *flag.FlagSet) *FeeSetting {
	s := &FeeSetting{}
	fs.StringVar(&s.GasPrice, "gas-price", "auto",
		`price per gas to pay: "auto" pays the node's minimum (minimum-gas-prices), or a price such as 0.0001uaeth`)
	fs.Float64Var(&s.Tip, "tip", 1,
		"multiple of the price to pay, at least 1: 2 pays twice the minimum, so the transaction goes ahead of those paying the minimum when blocks are full")
	return s
}

// Validate checks the setting without asking a node.
func (s FeeSetting) Validate() error {
	if s.Tip < 1 {
		return fmt.Errorf("--tip %v: at least 1 (1 pays the minimum)", s.Tip)
	}
	if p := strings.TrimSpace(s.GasPrice); p != "" && p != "auto" {
		if _, err := sdk.ParseDecCoins(p); err != nil {
			return fmt.Errorf("--gas-price %q: %w", s.GasPrice, err)
		}
	}
	return nil
}

// FeeFor is the fee a transaction asking gas pays at prices, times tip:
// in denom if prices name it, else in uaeth, else in the first denom
// priced; rounded up. Nothing priced, or a zero price, is no fee.
func FeeFor(prices sdk.DecCoins, denom string, gas uint64, tip float64) (sdk.Coins, error) {
	if tip < 1 {
		return nil, fmt.Errorf("tip %v: at least 1", tip)
	}
	price := sdk.DecCoin{}
	for _, want := range []string{denom, BaseDenom} {
		if want == "" {
			continue
		}
		if a := prices.AmountOf(want); a.IsPositive() {
			price = sdk.NewDecCoinFromDec(want, a)
			break
		}
	}
	if price.Denom == "" {
		for _, p := range prices {
			if p.Amount.IsPositive() {
				price = p
				break
			}
		}
	}
	if price.Denom == "" {
		return sdk.NewCoins(), nil
	}
	tipDec, err := sdkmath.LegacyNewDecFromStr(strconv.FormatFloat(tip, 'f', -1, 64))
	if err != nil {
		return nil, fmt.Errorf("tip %v: %w", tip, err)
	}
	amount := price.Amount.MulInt(sdkmath.NewIntFromUint64(gas)).Mul(tipDec).Ceil().TruncateInt()
	return sdk.NewCoins(sdk.NewCoin(price.Denom, amount)), nil
}

// Fee is the fee for a transaction asking gas under s: FeeFor at s's
// price, or at the node's minimum for "auto" (or an empty price).
func (c *Client) Fee(ctx context.Context, s FeeSetting, gas uint64, denom string) (sdk.Coins, error) {
	if s.Tip == 0 {
		s.Tip = 1
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	var prices sdk.DecCoins
	if p := strings.TrimSpace(s.GasPrice); p != "" && p != "auto" {
		prices, _ = sdk.ParseDecCoins(p)
	} else {
		var err error
		if prices, err = c.MinimumGasPrices(ctx); err != nil {
			return nil, err
		}
	}
	return FeeFor(prices, denom, gas, s.Tip)
}

// nodePriceTTL is how long a node's minimum price is kept: it changes
// only when the node restarts with another.
const nodePriceTTL = time.Minute

type nodePrices struct {
	mu     sync.Mutex
	prices sdk.DecCoins
	at     time.Time
}

var nodePriceCache sync.Map // *Client -> *nodePrices

// MinimumGasPrices asks the node for its minimum gas prices
// (cosmos.base.node.v1beta1 Config), kept for a minute. A node without
// that service (aetherd before it served it) answers Unimplemented and
// is taken to ask nothing, as the testnet's validators do.
func (c *Client) MinimumGasPrices(ctx context.Context) (sdk.DecCoins, error) {
	v, _ := nodePriceCache.LoadOrStore(c, &nodePrices{})
	np := v.(*nodePrices)
	np.mu.Lock()
	defer np.mu.Unlock()
	if !np.at.IsZero() && time.Since(np.at) < nodePriceTTL {
		return np.prices, nil
	}
	resp, err := nodeservice.NewServiceClient(c.conn).Config(ctx, &nodeservice.ConfigRequest{})
	var prices sdk.DecCoins
	switch {
	case status.Code(err) == codes.Unimplemented:
	case err != nil:
		return nil, fmt.Errorf("asking the node its minimum gas price: %w", err)
	default:
		if p := strings.TrimSpace(resp.MinimumGasPrice); p != "" {
			if prices, err = sdk.ParseDecCoins(p); err != nil {
				return nil, fmt.Errorf("the node's minimum gas price %q: %w", p, err)
			}
		}
	}
	np.prices, np.at = prices, time.Now()
	return prices, nil
}
