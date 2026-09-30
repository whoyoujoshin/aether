package main

import (
	"fmt"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/wallet"
)

// Amounts must carry a unit (see wallet.Assets.Parse), and every result
// names its asset and states the amount both in that asset and in its
// base unit, so an agent never has to convert.

const baseDenom = wallet.BaseDenom

// assets is what this server accepts: AETH, plus USDC once --usdc-channel
// names Aether's channel to Noble.
var assets, _ = wallet.NewAssets("")

// USDC limits, in uusdc. USDC can't share AETH's caps (there's no price
// to add them up with), so it has its own, and spending it stays off
// until the owner sets both.
var (
	usdcPerTxLimit        int64
	usdcDailyLimit        int64
	usdcApprovalThreshold math.Int // nil: no approvals for USDC
)

// parseAmount reads an AETH amount: what paywalled services, prepaid
// balances and pull allowances are priced in.
func parseAmount(s string) (math.Int, error) {
	amount, err := wallet.ParseAmount(s)
	if err != nil {
		return math.Int{}, newError(codeInvalidAmount, err.Error())
	}
	return amount, nil
}

// parseAssetAmount reads an amount of any asset this server knows:
// "5 USDC", "1.5 AETH", "5000000uusdc".
func parseAssetAmount(s string) (wallet.Asset, math.Int, error) {
	a, amount, err := assets.Parse(s)
	if err != nil {
		return wallet.Asset{}, math.Int{}, newError(codeInvalidAmount, err.Error())
	}
	return a, amount, nil
}

// assetOfDenom is the asset a stored record's denom names; "" is AETH,
// as records from before USDC were.
func assetOfDenom(denom string) wallet.Asset {
	if denom == "" || denom == baseDenom {
		return wallet.AETH
	}
	if a, ok := assets.ByDenom(denom); ok {
		return a
	}
	return wallet.Asset{Symbol: denom, Denom: denom, BaseUnit: denom}
}

// recordDenom is what a record stores for a: "" for AETH, so records
// (and the approvals they sign over) read exactly as they did before.
func recordDenom(a wallet.Asset) string {
	if a.Denom == baseDenom {
		return ""
	}
	return a.Denom
}

func formatAeth(uaeth math.Int) string { return wallet.FormatAeth(uaeth) }

// amountDTO states an amount with its asset, in both units, so an agent
// never has to convert (and can check the one it meant).
type amountDTO struct {
	Asset  string `json:"asset" jsonschema:"AETH, or USDC (Noble's, over Aether's channel to Noble)"`
	Amount string `json:"amount" jsonschema:"the amount in the asset's own unit, e.g. 1.5"`
	Base   string `json:"base" jsonschema:"the same amount in the asset's smallest unit (uaeth or uusdc; 1 = 1,000,000 of them)"`
	Denom  string `json:"denom" jsonschema:"the chain's name for the asset: uaeth, or ibc/... for USDC"`
	Uaeth  string `json:"uaeth,omitempty" jsonschema:"AETH amounts only: the amount in uaeth"`
	Aeth   string `json:"aeth,omitempty" jsonschema:"AETH amounts only: the amount in AETH"`
}

// newAmountDTO is an AETH amount.
func newAmountDTO(uaeth math.Int) amountDTO { return newAssetAmountDTO(wallet.AETH, uaeth) }

func newAssetAmountDTO(a wallet.Asset, base math.Int) amountDTO {
	d := amountDTO{Asset: a.Symbol, Amount: a.Decimal(base), Base: base.String(), Denom: a.Denom}
	if a.Denom == baseDenom {
		d.Uaeth, d.Aeth = base.String(), formatAeth(base)
	}
	return d
}

// knownAmount is the first asset this server knows that coins holds,
// AETH first; ok is false if it holds none of them.
func knownAmount(coins sdk.Coins) (wallet.Asset, math.Int, bool) {
	for _, a := range assets.List() {
		if amt := coins.AmountOf(a.Denom); amt.IsPositive() {
			return a, amt, true
		}
	}
	return wallet.Asset{}, math.Int{}, false
}

// coinsAmountDTO converts a coin string like "150000uaeth" (as the
// chain reports amounts); ok is false if it holds no known asset.
func coinsAmountDTO(coins string) (amountDTO, bool) {
	c, err := sdk.ParseCoinsNormalized(coins)
	if err != nil {
		return amountDTO{}, false
	}
	a, amt, ok := knownAmount(c)
	if !ok {
		return amountDTO{}, false
	}
	return newAssetAmountDTO(a, amt), true
}

// limits are this server's caps for one asset, in its base unit.
type limits struct {
	perTx, daily int64
	approval     math.Int // nil: no approvals
}

func limitsFor(a wallet.Asset) (limits, error) {
	switch {
	case a.Denom == baseDenom:
		return limits{perTxLimit, dailyLimit, approvalThreshold}, nil
	case a.Symbol == "USDC" && usdcPerTxLimit > 0 && usdcDailyLimit > 0:
		return limits{usdcPerTxLimit, usdcDailyLimit, usdcApprovalThreshold}, nil
	}
	return limits{}, newError(codeAssetNotEnabled, fmt.Sprintf("this agent can't spend %s: its owner hasn't set --usdc-per-tx-limit and --usdc-daily-limit", a.Symbol))
}

// checkLimits refuses amount of a if it's over this server's per-payment
// cap or would take the rolling 24h total over the daily cap. what names
// the thing for the message ("a 5 USDC allowance").
func checkLimits(st *agentState, a wallet.Asset, amount math.Int) error {
	l, err := limitsFor(a)
	if err != nil {
		return err
	}
	if amount.Int64() > l.perTx {
		return newError(codePerTxLimit, fmt.Sprintf("%s exceeds the per-transaction limit of %s", a.Format(amount), a.Format(math.NewInt(l.perTx))))
	}
	now := time.Now()
	if st.spentInWindow(now, a.Denom)+amount.Int64() > l.daily {
		return dailyLimitError(st, now, a, amount.Int64())
	}
	return nil
}

func dailyLimitError(st *agentState, now time.Time, a wallet.Asset, amount int64) error {
	l, _ := limitsFor(a)
	spent := st.spentInWindow(now, a.Denom)
	e := newError(codeDailyLimit, fmt.Sprintf("%s would exceed the rolling 24h limit of %s (%s already committed in the last 24h)",
		a.Format(math.NewInt(amount)), a.Format(math.NewInt(l.daily)), a.Format(math.NewInt(spent))))
	if wait, ok := st.retryAfter(now, amount, l.daily, a.Denom); ok {
		e.RetryAfterSeconds = int64(wait.Seconds()) + 1
	}
	return e
}

// newSpend is a budget reservation for amount of a.
func newSpend(now time.Time, a wallet.Asset, amount int64, tag string) spendEvent {
	return spendEvent{Time: now, Amount: amount, Denom: recordDenom(a), TxHash: tag}
}

// configureUSDC sets up USDC from the --usdc-* flags. The limits are
// refused unless the channel is set and they're written in USDC.
func configureUSDC(channel, perTx, daily, threshold string) error {
	a, err := wallet.NewAssets(channel)
	if err != nil {
		return fmt.Errorf("--usdc-channel: %w", err)
	}
	assets = a
	usdc, ok := a.BySymbol("USDC")
	parse := func(flagName, s string) (math.Int, error) {
		if !ok {
			return math.Int{}, fmt.Errorf("%s needs --usdc-channel", flagName)
		}
		got, amount, err := a.Parse(s)
		if err != nil {
			return math.Int{}, fmt.Errorf("%s: %w", flagName, err)
		}
		if got.Denom != usdc.Denom {
			return math.Int{}, fmt.Errorf("%s: %q isn't a USDC amount", flagName, s)
		}
		return amount, nil
	}
	if perTx != "" {
		v, err := parse("--usdc-per-tx-limit", perTx)
		if err != nil {
			return err
		}
		usdcPerTxLimit = v.Int64()
	}
	if daily != "" {
		v, err := parse("--usdc-daily-limit", daily)
		if err != nil {
			return err
		}
		usdcDailyLimit = v.Int64()
	}
	if (usdcPerTxLimit > 0) != (usdcDailyLimit > 0) {
		return fmt.Errorf("set both --usdc-per-tx-limit and --usdc-daily-limit, or neither")
	}
	if threshold != "" {
		if approver == "" {
			return fmt.Errorf("--usdc-approval-threshold needs --approver: the owner address whose key signs approvals")
		}
		v, err := parse("--usdc-approval-threshold", threshold)
		if err != nil {
			return err
		}
		usdcApprovalThreshold = v
	}
	return nil
}
