package main

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/whoyoujoshin/aether/wallet"
)

// swap_from_test_wallet: swap_aeth for a hosted test wallet. Test
// wallets have no idempotency keys or owner, so it has no limits but
// the wallet's balance; a swap still in flight is followed up by
// passing its txHash instead of an amount.

const sandboxSwapMaxWait = 120 // seconds: an HTTP call shouldn't hang longer

type swapFromTestWalletInput struct {
	WalletToken     string  `json:"walletToken" jsonschema:"the token create_test_wallet returned"`
	Amount          string  `json:"amount,omitempty" jsonschema:"the AETH to swap, with its unit, e.g. \"0.005 AETH\". Leave out with txHash to follow up a swap already sent"`
	SlippagePercent float64 `json:"slippagePercent,omitempty" jsonschema:"how far below the quote the swap may fill (default 1, at most 50)"`
	MinOutput       string  `json:"minOutput,omitempty" jsonschema:"instead of slippagePercent: the least OSMO to accept, in uosmo"`
	MaxCostPercent  float64 `json:"maxCostPercent,omitempty" jsonschema:"refuse if the quote's fees and price impact exceed this (default 3)"`
	TxHash          string  `json:"txHash,omitempty" jsonschema:"follow up a swap this wallet already sent, instead of sending one"`
	WaitSeconds     int     `json:"waitSeconds,omitempty" jsonschema:"how long to wait for the swap to finish (default 60, max 120); a swap takes about 25 seconds"`
}

func toolSwapFromTestWallet(ctx context.Context, _ *mcp.CallToolRequest, in swapFromTestWalletInput) (*mcp.CallToolResult, swapAethOutput, error) {
	out, err := swapFromTestWallet(ctx, in)
	return nil, out, err
}

func swapFromTestWallet(ctx context.Context, in swapFromTestWalletInput) (swapAethOutput, error) {
	var out swapAethOutput
	contract := swapContract()
	if contract == "" {
		return out, newError(codeSwapNotConfigured, "swaps are off on this server")
	}
	w, address, err := sbx.open(in.WalletToken)
	if err != nil {
		return out, err
	}
	wait := in.WaitSeconds
	if wait <= 0 {
		wait = 60
	}
	if wait > sandboxSwapMaxWait {
		wait = sandboxSwapMaxWait
	}
	deadline := time.Now().Add(time.Duration(wait) * time.Second)

	c, err := dialChain()
	if err != nil {
		return out, err
	}
	defer c.close()
	sc, ok := c.(swapChain)
	if !ok {
		return out, newError(codeInternal, "this chain connection can't follow IBC packets")
	}

	if in.TxHash != "" {
		if in.Amount != "" {
			return out, newError(codeInvalidArgument, "give either amount (a new swap) or txHash (follow one up), not both")
		}
		d, err := c.lookup(in.TxHash)
		if err != nil {
			return out, newError(codeInvalidArgument, fmt.Sprintf("transaction %s: %v", in.TxHash, err))
		}
		if d.From != address {
			return out, newError(codeInvalidArgument, "that transaction wasn't sent by this test wallet")
		}
		out.TxHash = in.TxHash
		return trackSwap(ctx, c, sc, address, in.TxHash, nil, deadline, out)
	}

	a, amount, err := parseAssetAmount(in.Amount)
	if err != nil {
		return out, err
	}
	if a.Denom != baseDenom || !amount.IsPositive() {
		return out, newError(codeInvalidAmount, "amount must be a positive amount of AETH, e.g. \"0.005 AETH\"")
	}
	minOut, quote, err := swapMinOutput(ctx, in.Amount, in.SlippagePercent, in.MinOutput, in.MaxCostPercent)
	out.Quote = quote
	if err != nil {
		return out, err
	}
	if err := checkSwapReturnRoom(c, swapExpected(quote, minOut)); err != nil {
		return out, err
	}

	bal, err := c.balance(address)
	if err != nil {
		return out, err
	}
	before := bal.AmountOf(osmoOnAether).BigInt()
	msg := &transfertypes.MsgTransfer{
		SourcePort: transfertypes.PortID, SourceChannel: testnetOsmosisChannel,
		Token:  sdk.NewCoin(baseDenom, amount),
		Sender: address, Receiver: contract,
		TimeoutTimestamp: uint64(time.Now().Add(swapPacketTimeout).UnixNano()),
		Memo:             swapMemo(contract, minOut.String(), address),
	}
	hash, next, err := sendMsgFrom(c, w, "w", address, msg, "", sbx.pendingNext(address))
	if err != nil {
		return out, err
	}
	sbx.notePending(address, next)
	amt, mo := newAmountDTO(amount), newOsmoAmount(minOut.BigInt())
	out.TxHash, out.Amount, out.MinOutput = hash, &amt, &mo
	return trackSwap(ctx, c, sc, address, hash, before, deadline, out)
}

// swapMinOutput is the minOutput a swap is sent with: the explicit one,
// or the quote's for slippage, refusing a quote that costs more than
// maxCost percent. Without a quote it needs an explicit one.
func swapMinOutput(ctx context.Context, amount string, slippage float64, explicit string, maxCost float64) (math.Int, *quoteSwapOutput, error) {
	var min math.Int
	if explicit != "" {
		v, ok := math.NewIntFromString(strings.TrimSuffix(strings.TrimSpace(explicit), "uosmo"))
		if !ok || !v.IsPositive() {
			return min, nil, newError(codeInvalidAmount, fmt.Sprintf("minOutput must be a positive number of uosmo, e.g. \"382882\"; got %q", explicit))
		}
		min = v
	}
	if maxCost == 0 {
		maxCost = swapDefaultMaxCost
	}
	if maxCost < 0 || maxCost > 100 {
		return min, nil, newError(codeInvalidArgument, "maxCostPercent must be between 0 and 100")
	}
	q, err := quoteSwap(ctx, quoteSwapInput{Amount: amount, SlippagePercent: slippage})
	if err != nil {
		if min.IsNil() {
			return min, nil, err
		}
		return min, nil, nil
	}
	if cost, ok := new(big.Rat).SetString(q.CostPercent); ok && cost.Cmp(new(big.Rat).SetFloat64(maxCost)) > 0 {
		return min, &q, newError(codeSwapCostTooHigh, fmt.Sprintf("this swap would cost %s%% in fees and price impact, more than maxCostPercent %g: swap less, or raise maxCostPercent", q.CostPercent, maxCost))
	}
	if min.IsNil() {
		min, _ = math.NewIntFromString(q.MinOutput.Uosmo)
	}
	return min, &q, nil
}

// trackSwap follows a sent swap until deadline. before is the sender's
// OSMO when it was sent, or nil when following one up (then received is
// what the account holds now).
func trackSwap(ctx context.Context, c chain, sc swapChain, address, hash string, before *big.Int, deadline time.Time, out swapAethOutput) (swapAethOutput, error) {
	tx, err := awaitTransaction(ctx, c, hash, deadline)
	if err != nil {
		return out, err
	}
	switch tx.Status {
	case statusPending:
		out.Status, out.Message = statusPending, "not in a block yet: call again with this txHash (and no amount) to follow it up"
		return out, nil
	case statusFailed:
		out.Status, out.ErrorCode, out.Message = statusFailed, tx.ErrorCode, tx.RawLog
		return out, nil
	}
	seq, err := sc.sentPacketSequence(hash, testnetOsmosisChannel)
	if err != nil {
		return out, err
	}
	out.PacketSequence = seq
	for {
		o, err := sc.sentPacketOutcome(testnetOsmosisChannel, seq)
		if err != nil {
			return out, err
		}
		switch o.Status {
		case wallet.PacketFailed, wallet.PacketTimedOut:
			out.Status, out.Error, out.AckTxHash = statusSwapRefunded, o.Error, o.TxHash
			if o.Status == wallet.PacketTimedOut {
				out.Error = "the transfer timed out before Osmosis received it"
			}
			out.Message = "the swap didn't happen and the AETH was refunded to this wallet"
			return out, nil
		case wallet.PacketAcked, wallet.PacketSettled:
			out.AckTxHash = o.TxHash
			bal, err := c.balance(address)
			if err != nil {
				return out, err
			}
			got := bal.AmountOf(osmoOnAether).BigInt()
			if before != nil {
				got = new(big.Int).Sub(got, before)
			}
			if got.Sign() > 0 {
				r := newOsmoAmount(got)
				out.Status, out.Received = statusSwapped, &r
				if before == nil {
					out.Message = "received is this wallet's whole OSMO balance on Aether"
				}
				return out, nil
			}
		}
		if !time.Now().Add(swapPollInterval).Before(deadline) || ctx.Err() != nil {
			if o.Status == wallet.PacketAcked || o.Status == wallet.PacketSettled {
				out.Status, out.Message = statusSwapped, "Osmosis swapped it and sent the OSMO back; it hasn't arrived yet: check get_balance shortly"
			} else {
				out.Status, out.Message = statusSwapInFlight, "sent; Osmosis hasn't acknowledged it yet: call again with this txHash (and no amount) to keep following it"
			}
			return out, nil
		}
		select {
		case <-ctx.Done():
		case <-time.After(swapPollInterval):
		}
	}
}
