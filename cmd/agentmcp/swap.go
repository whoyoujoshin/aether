package main

import (
	"context"
	"encoding/json"
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

// swap_aeth: swap this agent's AETH for OSMO in the Osmosis testnet pool
// and get the OSMO back on Aether (docs/OSMOSIS-POOL.md). It sends AETH
// over the Osmosis channel to contracts/aether-swap with a memo; on
// Osmosis, ibc-hooks runs the contract, which swaps in the pool and
// sends the OSMO back to this agent. If the swap can't clear minOutput,
// or anything else fails before the OSMO leaves, Osmosis acknowledges
// the transfer with an error and Aether refunds the AETH.
//
// It is spending, so it goes through send_aeth's limits, owner approval
// and idempotency. Like create_escrow it isn't available in grant mode.

const (
	sendKindSwap = "swap"

	// testnetSwapContract is contracts/aether-swap on osmo-test-5 (code 13376).
	testnetSwapContract = "osmo14qlp2rly8gy322slcs0tvuus5e5rc4390llet43mw23reg0vdnfs0nv5hn"

	swapPacketTimeout  = 10 * time.Minute
	swapDefaultMaxCost = 3.0
	swapPollInterval   = 3 * time.Second
	statusSwapInFlight = "in_flight"
	statusSwapped      = "swapped"
	statusSwapRefunded = "refunded"
	statusSwapSettled  = "settled"
)

// osmosisSwapContract is --osmosis-swap-contract: "" is the testnet one
// on aether-testnet-1, "off" turns swap_aeth off.
var osmosisSwapContract string

func swapContract() string {
	switch osmosisSwapContract {
	case "off":
		return ""
	case "":
		if chainID == "aether-testnet-1" {
			return testnetSwapContract
		}
		return ""
	}
	return osmosisSwapContract
}

// swapParams is what a recorded swap was for.
type swapParams struct {
	Contract   string `json:"contract"`
	Channel    string `json:"channel"`
	MinOutput  string `json:"minOutput"`           // uosmo
	OsmoBefore string `json:"osmoBefore"`          // this agent's OSMO on Aether when it was signed
	Sequence   uint64 `json:"sequence,omitempty"`  // the packet, once in a block
	Settled    string `json:"settled,omitempty"`   // swapped / refunded, once known
	Error      string `json:"error,omitempty"`     // why it was refunded
	AckTxHash  string `json:"ackTxHash,omitempty"` // the acknowledgement or timeout on Aether
	Explicit   bool   `json:"explicit,omitempty"`  // minOutput was given, not from a quote
}

// swapChain is what swap_aeth needs beyond chain.
type swapChain interface {
	sentPacketSequence(hash, channel string) (uint64, error)
	sentPacketOutcome(channel string, seq uint64) (*wallet.PacketOutcome, error)
}

func (g grpcChain) sentPacketSequence(h, ch string) (uint64, error) {
	ctx, cancel := escrowCtx()
	defer cancel()
	return g.c.SentPacketSequence(ctx, h, ch)
}

func (g grpcChain) sentPacketOutcome(ch string, seq uint64) (*wallet.PacketOutcome, error) {
	ctx, cancel := escrowCtx()
	defer cancel()
	return g.c.SentPacketOutcome(ctx, transfertypes.PortID, ch, seq)
}

type swapAethInput struct {
	Amount          string  `json:"amount" jsonschema:"the AETH to swap, with its unit: \"1 AETH\" or \"1000000uaeth\""`
	SlippagePercent float64 `json:"slippagePercent,omitempty" jsonschema:"how far below the quote the swap may fill (default 1, at most 50); minOutput is the quote less this"`
	MinOutput       string  `json:"minOutput,omitempty" jsonschema:"instead of slippagePercent: the least OSMO to accept, in uosmo (e.g. from quote_swap's minOutput)"`
	MaxCostPercent  float64 `json:"maxCostPercent,omitempty" jsonschema:"refuse if the quote's fees and price impact together exceed this (default 3). The pool is small: a large swap moves its price"`
	IdempotencyKey  string  `json:"idempotencyKey" jsonschema:"unique ID for this swap. Retrying with the same key never swaps twice: it follows up the same transfer. Use a new key only for a new swap"`
	WaitSeconds     int     `json:"waitSeconds,omitempty" jsonschema:"how long to wait for the swap to finish (default 90, max 300); a swap takes about 25 seconds"`
}

type swapAethOutput struct {
	Status         string           `json:"status" jsonschema:"swapped (done; received is what arrived), in_flight (sent, the swap hasn't finished: call again with the same idempotencyKey to keep following it), refunded (the swap didn't happen and the AETH came back: see error), pending (not in a block yet), failed (the transfer failed on Aether), pending_approval (waiting for the owner; nothing sent)"`
	TxHash         string           `json:"txHash,omitempty"`
	Amount         *amountDTO       `json:"amount,omitempty" jsonschema:"the AETH sent"`
	MinOutput      *osmoAmountDTO   `json:"minOutput,omitempty" jsonschema:"the least OSMO the swap was allowed to fill at"`
	Quote          *quoteSwapOutput `json:"quote,omitempty" jsonschema:"the quote it was sent on (first call only)"`
	PacketSequence uint64           `json:"packetSequence,omitempty"`
	Received       *osmoAmountDTO   `json:"received,omitempty" jsonschema:"OSMO that arrived on Aether since the swap was sent"`
	Error          string           `json:"error,omitempty" jsonschema:"why it was refunded, as Osmosis acknowledged it"`
	AckTxHash      string           `json:"ackTxHash,omitempty" jsonschema:"the transaction on Aether that acknowledged or timed out the transfer"`
	ApprovalID     string           `json:"approvalId,omitempty"`
	ErrorCode      string           `json:"errorCode,omitempty"`
	Message        string           `json:"message,omitempty"`
	Replayed       bool             `json:"replayed,omitempty"`
}

// swapMemo is the ibc-hooks memo that runs the contract's swap_and_return.
func swapMemo(contract, minOutput, recipient string) string {
	bz, _ := json.Marshal(map[string]any{"wasm": map[string]any{
		"contract": contract,
		"msg": map[string]any{"swap_and_return": map[string]any{
			"min_output": minOutput, "recipient": recipient,
		}},
	}})
	return string(bz)
}

func toolSwapAeth(ctx context.Context, _ *mcp.CallToolRequest, in swapAethInput) (*mcp.CallToolResult, swapAethOutput, error) {
	out, err := swapAeth(ctx, in)
	return nil, out, err
}

func swapAeth(ctx context.Context, in swapAethInput) (swapAethOutput, error) {
	var out swapAethOutput
	contract := swapContract()
	if contract == "" {
		return out, newError(codeSwapNotConfigured, "swap_aeth is off on this server (no --osmosis-swap-contract, or not aether-testnet-1)")
	}
	if granter != "" {
		return out, newError(codeInvalidArgument, "swap_aeth isn't available in grant mode: the chain caps grant spending only for plain sends, so a transfer from the granter's account would be uncapped. Run without --granter, with this agent's own funds")
	}
	if in.IdempotencyKey == "" {
		return out, newError(codeInvalidArgument, "idempotencyKey is required")
	}
	a, amount, err := parseAssetAmount(in.Amount)
	if err != nil {
		return out, err
	}
	if a.Denom != baseDenom {
		return out, newError(codeInvalidAmount, "swap_aeth swaps AETH into OSMO; amount must be AETH, e.g. \"1 AETH\"")
	}
	if !amount.IsPositive() {
		return out, newError(codeInvalidAmount, "amount must be more than zero")
	}
	var explicitMin math.Int
	if in.MinOutput != "" {
		s := strings.TrimSuffix(strings.TrimSpace(in.MinOutput), "uosmo")
		v, ok := math.NewIntFromString(s)
		if !ok || !v.IsPositive() {
			return out, newError(codeInvalidAmount, fmt.Sprintf("minOutput must be a positive number of uosmo, e.g. \"382882\"; got %q", in.MinOutput))
		}
		explicitMin = v
	}
	if in.MaxCostPercent < 0 || in.MaxCostPercent > 100 {
		return out, newError(codeInvalidArgument, "maxCostPercent must be between 0 and 100")
	}
	deadline := time.Now().Add(clampWait(in.WaitSeconds))

	c, err := dialChain()
	if err != nil {
		return out, err
	}
	defer c.close()
	sc, ok := c.(swapChain)
	if !ok {
		return out, newError(codeInternal, "this chain connection can't follow IBC packets")
	}

	// A retry follows up the recorded swap; only a new one is quoted.
	stateMu.Lock()
	st, err := loadState()
	if err != nil {
		stateMu.Unlock()
		return out, err
	}
	rec, replay := st.Sends[in.IdempotencyKey]
	stateMu.Unlock()
	if replay {
		if rec.Kind != sendKindSwap || rec.Amount != amount.String() || rec.Swap == nil || (in.MinOutput != "" && rec.Swap.MinOutput != explicitMin.String()) {
			e := newError(codeIdempotencyConflict, fmt.Sprintf("idempotencyKey %q was already used for something else; use a new key for a new swap", in.IdempotencyKey))
			e.TxHash = rec.TxHash
			return out, e
		}
		out.Replayed = true
		stateMu.Lock()
		st, err := loadState()
		if err == nil {
			rec = st.Sends[in.IdempotencyKey]
			err = resend(st, c, rec)
		}
		stateMu.Unlock()
		if err != nil {
			return out, err
		}
	} else {
		// Over a limit, don't spend a quote on it. signSwap checks again
		// under the lock, with the approval.
		if err := checkLimits(st, wallet.AETH, amount); err != nil {
			return out, err
		}
		minOut, quote, err := swapMinOutput(ctx, in.Amount, in.SlippagePercent, in.MinOutput, in.MaxCostPercent)
		out.Quote = quote
		if err != nil {
			return out, err
		}
		if err := checkSwapReturnRoom(c, swapExpected(quote, minOut)); err != nil {
			return out, err
		}
		var sendErr error
		rec, sendErr = signSwap(c, in, amount, contract, minOut, in.MinOutput != "", &out)
		if sendErr != nil || rec == nil {
			if sendErr != nil && rec != nil {
				if ae, ok := sendErr.(*agentError); ok {
					ae.TxHash = rec.TxHash
				}
			}
			return out, sendErr
		}
		out.Quote = quote
		if quote != nil {
			notify("swap_sent", map[string]any{"amount": newAmountDTO(amount), "minOutputUosmo": minOut.String(), "costPercent": quote.CostPercent, "txHash": rec.TxHash})
		}
	}

	amt := newAmountDTO(amount)
	minOut, _ := math.NewIntFromString(rec.Swap.MinOutput)
	mo := newOsmoAmount(minOut.BigInt())
	out.TxHash, out.Amount, out.MinOutput = rec.TxHash, &amt, &mo
	return followSwap(ctx, c, sc, in.IdempotencyKey, rec, deadline, out)
}

// signSwap checks the limits and approval, then signs, records and sends
// the transfer. A nil record with a nil error means it waits for the owner.
func signSwap(c chain, in swapAethInput, amount math.Int, contract string, minOut math.Int, explicit bool, out *swapAethOutput) (*sendRecord, error) {
	stateMu.Lock()
	defer stateMu.Unlock()
	st, err := loadState()
	if err != nil {
		return nil, err
	}
	if err := checkLimits(st, wallet.AETH, amount); err != nil {
		return nil, err
	}
	w, err := newWallet()
	if err != nil {
		return nil, err
	}
	from, err := getOrCreateAgentAccount(w)
	if err != nil {
		return nil, err
	}
	gate := sendAethInput{To: contract, Amount: amount.String() + wallet.AETH.BaseUnit,
		Memo: fmt.Sprintf("swap for at least %s OSMO on Osmosis testnet", formatMicro(minOut.BigInt())), IdempotencyKey: in.IdempotencyKey}
	if proceed, p, err := approvalGate(st, from.Address, gate, wallet.AETH, amount); !proceed {
		if err != nil {
			return nil, err
		}
		out.Status, out.ApprovalID, out.Message = statusPendingApproval, p.ApprovalID, "swapping "+wallet.AETH.Format(amount)+" needs the owner's approval: "+p.Message
		return nil, nil
	}
	bal, err := c.balance(from.Address)
	if err != nil {
		return nil, err
	}
	memo := swapMemo(contract, minOut.String(), from.Address)
	msg := &transfertypes.MsgTransfer{
		SourcePort: transfertypes.PortID, SourceChannel: testnetOsmosisChannel,
		Token:  sdk.NewCoin(baseDenom, amount),
		Sender: from.Address, Receiver: contract,
		TimeoutTimestamp: uint64(time.Now().Add(swapPacketTimeout).UnixNano()),
		Memo:             memo,
	}
	rec, err := signAndRecord(st, c, w, from.Address, in.IdempotencyKey, msg, &sendRecord{
		Kind: sendKindSwap, To: contract, Amount: amount.String(), Memo: memo,
		Swap: &swapParams{Contract: contract, Channel: testnetOsmosisChannel, MinOutput: minOut.String(), OsmoBefore: bal.AmountOf(osmoOnAether).String(), Explicit: explicit},
	}, true)
	if err != nil {
		return nil, err
	}
	consumeApproval(st, gate.IdempotencyKey)
	if err := st.save(); err != nil {
		return nil, err
	}
	return rec, broadcastRecorded(st, c, rec)
}

// followSwap waits for the transfer's block, then its packet, then the
// OSMO, until deadline, recording what it learns under key.
func followSwap(ctx context.Context, c chain, sc swapChain, key string, rec *sendRecord, deadline time.Time, out swapAethOutput) (swapAethOutput, error) {
	sw := *rec.Swap
	if sw.Settled == "" {
		tx, err := awaitTransaction(ctx, c, rec.TxHash, deadline)
		if err != nil {
			return out, err
		}
		switch tx.Status {
		case statusPending:
			out.Status, out.Message = statusPending, "not in a block yet: call swap_aeth again with the same arguments and idempotencyKey to keep following it (it won't swap twice)"
			return out, nil
		case statusFailed:
			out.Status, out.ErrorCode, out.Message = statusFailed, tx.ErrorCode, tx.RawLog
			return out, nil
		}
		if sw.Sequence == 0 {
			seq, err := sc.sentPacketSequence(rec.TxHash, sw.Channel)
			if err != nil {
				return out, err
			}
			sw.Sequence = seq
			saveSwap(key, sw, false)
		}
	}
	out.PacketSequence = sw.Sequence

	for {
		if sw.Settled == "" {
			o, err := sc.sentPacketOutcome(sw.Channel, sw.Sequence)
			if err != nil {
				return out, err
			}
			switch o.Status {
			case wallet.PacketFailed, wallet.PacketTimedOut:
				sw.Settled, sw.AckTxHash = statusSwapRefunded, o.TxHash
				sw.Error = o.Error
				if o.Status == wallet.PacketTimedOut {
					sw.Error = "the transfer timed out before Osmosis received it"
				}
				saveSwap(key, sw, true) // the AETH came back: free its budget
			case wallet.PacketAcked, wallet.PacketSettled:
				sw.Settled, sw.AckTxHash = statusSwapped, o.TxHash
				saveSwap(key, sw, false)
			}
		}
		switch sw.Settled {
		case statusSwapRefunded:
			out.Status, out.Error, out.AckTxHash = statusSwapRefunded, sw.Error, sw.AckTxHash
			out.Message = "the swap didn't happen and the AETH was refunded to this agent"
			return out, nil
		case statusSwapped:
			out.AckTxHash = sw.AckTxHash
			if got, err := osmoReceived(c, rec.From, sw.OsmoBefore); err == nil && got.Sign() > 0 {
				r := newOsmoAmount(got)
				out.Status, out.Received = statusSwapped, &r
				return out, nil
			}
		}
		if !time.Now().Add(swapPollInterval).Before(deadline) || ctx.Err() != nil {
			if sw.Settled == statusSwapped {
				out.Status = statusSwapped
				out.Message = "Osmosis swapped it and sent the OSMO back; it hasn't arrived on Aether yet (it usually does within seconds): check get_balance, or call again with the same idempotencyKey"
			} else {
				out.Status = statusSwapInFlight
				out.Message = "sent; Osmosis hasn't acknowledged it yet. Call swap_aeth again with the same arguments and idempotencyKey to keep following it (it won't swap twice)"
			}
			return out, nil
		}
		select {
		case <-ctx.Done():
		case <-time.After(swapPollInterval):
		}
	}
}

// osmoReceived is how much OSMO address holds on Aether above before.
func osmoReceived(c chain, address, before string) (*big.Int, error) {
	bal, err := c.balance(address)
	if err != nil {
		return nil, err
	}
	b, ok := math.NewIntFromString(before)
	if !ok {
		b = math.ZeroInt()
	}
	return bal.AmountOf(osmoOnAether).Sub(b).BigInt(), nil
}

// saveSwap stores what's been learned about a swap; refunded also frees
// its amount from the daily budget, since the AETH came back.
func saveSwap(key string, sw swapParams, refunded bool) {
	stateMu.Lock()
	defer stateMu.Unlock()
	st, err := loadState()
	if err != nil {
		return
	}
	rec, ok := st.Sends[key]
	if !ok || rec.Swap == nil {
		return
	}
	*rec.Swap = sw
	if refunded {
		st.releaseSpend(rec.TxHash)
	}
	_ = st.save()
}
