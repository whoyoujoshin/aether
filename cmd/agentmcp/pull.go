package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/whoyoujoshin/aether/paywall"
	"github.com/whoyoujoshin/aether/wallet"
)

// fetch_paid's pull path (aether-pull, package paywall): grant the
// seller a capped, expiring allowance on chain, payable only to it, then
// pay per request by signing it -- no block wait, and nothing deposited:
// the seller collects what the agent owes from its own account later.
// Opt-in per call with pullAllowance. Needs the agent's own funds, so
// not in grant mode.

const (
	// pullGrantDays is how long an allowance this agent grants lasts.
	pullGrantDays = 7
	// pullGrantRenewWithin: an allowance expiring sooner is renewed.
	pullGrantRenewWithin = 24 * time.Hour
	sendKindPullGrant    = "pull-grant"
)

func pullGrantKey(grantee string) string { return "pull-grant/" + grantee }

// pullGrantMemo describes an allowance in the owner's approval request.
func pullGrantMemo(payTo string) string { return "aether-pull allowance, payable only to " + payTo }

func fetchPull(ctx context.Context, in fetchPaidInput, method string, req paywall.PaymentRequirements, maxAmount, allowance math.Int) (fetchPaidOutput, error) {
	price, err := wallet.ParseUaeth(req.MaxAmountRequired)
	if err != nil {
		return fetchPaidOutput{}, newError(codePaymentUnsupported, "the server's price is invalid: "+err.Error())
	}
	if price.GT(maxAmount) {
		return fetchPaidOutput{}, newError(codePriceExceedsMax, fmt.Sprintf("the server asks %s AETH (%s uaeth) per request; your maxAmount is %s AETH. Nothing was paid", formatAeth(price), price, formatAeth(maxAmount)))
	}
	if _, err := sdk.AccAddressFromBech32(req.Extra.Grantee); err != nil {
		return fetchPaidOutput{}, newError(codePaymentUnsupported, fmt.Sprintf("the server's pull grantee %q is not a valid address", req.Extra.Grantee))
	}
	if _, err := sdk.AccAddressFromBech32(req.PayTo); err != nil {
		return fetchPaidOutput{}, newError(codePaymentUnsupported, fmt.Sprintf("the server's payTo %q is not a valid address", req.PayTo))
	}
	if allowance.LT(price) {
		return fetchPaidOutput{}, newError(codeInvalidArgument, fmt.Sprintf("pullAllowance %s AETH doesn't cover one request (%s AETH)", formatAeth(allowance), formatAeth(price)))
	}
	w, err := newWallet()
	if err != nil {
		return fetchPaidOutput{}, err
	}
	agent, err := getOrCreateAgentAccount(w)
	if err != nil {
		return fetchPaidOutput{}, err
	}
	u, _ := url.Parse(in.URL)
	path := u.Path
	if path == "" {
		path = "/"
	}
	requestID := requestIDFor(in.IdempotencyKey)
	var grantTx string // an allowance granted by this call
	spendTag := "pull:" + req.PayTo + "/" + requestID

	attempt := func() (*httpResult, error) {
		// Each request is money leaving this account once it's
		// collected: it counts against the daily limit now.
		stateMu.Lock()
		st, err := loadState()
		if err == nil && !st.hasSpend(spendTag) {
			if price.Int64() > perTxLimit {
				err = newError(codePerTxLimit, fmt.Sprintf("%s AETH exceeds the per-transaction limit of %s AETH", formatAeth(price), formatAeth(math.NewInt(perTxLimit))))
			} else if st.spentInWindow(time.Now())+price.Int64() > dailyLimit {
				err = dailyLimitError(st, time.Now(), price.Int64())
			}
		}
		stateMu.Unlock()
		if err != nil {
			return nil, err
		}
		header, err := paywall.EncodePullPayment(agent.Address, paywall.RequestFields{
			Network: chainID, PayTo: req.PayTo, Host: u.Host, Method: method, Path: path, Body: []byte(in.Body),
			MaxPrice: price, Timestamp: time.Now().Unix(), RequestID: requestID,
		}, func(msg []byte) ([]byte, []byte, error) { return w.SignBytes(accountName, msg) })
		if err != nil {
			return nil, err
		}
		return doHTTP(ctx, in, method, header)
	}
	paid := func(res *httpResult) (fetchPaidOutput, error) {
		if res.status < 500 {
			stateMu.Lock()
			st, err := loadState()
			if err == nil && !st.hasSpend(spendTag) {
				st.Events = append(st.Events, spendEvent{Time: time.Now(), Amount: price.Int64(), TxHash: spendTag})
				err = st.save()
			}
			stateMu.Unlock()
			if err != nil {
				return fetchPaidOutput{}, err
			}
		}
		out := res.output("paid")
		out.Payment = &fetchPaymentDTO{Scheme: paywall.SchemePull, Amount: newAmountDTO(price), PayTo: req.PayTo, GrantTxHash: grantTx}
		var s paywall.SettlementResponse
		if paywall.DecodeHeader(res.header.Get(paywall.HeaderPaymentResponse), &s) == nil {
			if owed, ok := math.NewIntFromString(s.Owed); ok {
				o := newAmountDTO(owed)
				out.Payment.Owed = &o
			}
			if left, ok := math.NewIntFromString(s.Allowance); ok {
				a := newAmountDTO(left)
				out.Payment.Allowance = &a
			}
		}
		out.Receipt = recordPurchase(purchase{u: u, method: method, payTo: req.PayTo, scheme: paywall.SchemePull, payer: agent.Address,
			payment: requestID, amount: price, reqBody: []byte(in.Body), res: res})
		return out, nil
	}

	for round := 0; ; round++ {
		res, err := attempt()
		if err != nil {
			return fetchPaidOutput{}, err
		}
		if res.status != http.StatusPaymentRequired {
			return paid(res)
		}
		_, reason, _ := quote(res)
		switch reason {
		case paywall.ErrNoGrant, paywall.ErrGrantTooLow, paywall.ErrPullUnpaid:
		default:
			return fetchPaidOutput{}, refusedAfterPayment(reason, res, "")
		}
		if round > 0 {
			return fetchPaidOutput{}, newError(codePaymentRejected, "the server still refuses after granting an allowance: "+reason)
		}
		// The server says what's owed but not yet collected; the new
		// allowance must cover that and this request.
		if offer, ok := quoteScheme(res, paywall.SchemePull); ok {
			if owed, ok := math.NewIntFromString(offer.Extra.Owed); ok && allowance.LT(owed.Add(price)) {
				return fetchPaidOutput{}, newError(codeInvalidArgument, fmt.Sprintf("you owe this service %s AETH not yet collected; pullAllowance must be at least %s AETH", formatAeth(owed), formatAeth(owed.Add(price))))
			}
		}
		out, hash, done, err := ensurePullGrant(ctx, w, agent.Address, req, allowance, in.TimeoutSeconds)
		if err != nil || done {
			return out, err
		}
		grantTx = hash
	}
}

// ensurePullGrant makes sure an allowance of `allowance` to the seller's
// grantee is on chain, granting one if needed. done is true when the
// caller should return out as is (approval or confirmation pending).
func ensurePullGrant(ctx context.Context, w *wallet.Wallet, agent string, req paywall.PaymentRequirements, allowance math.Int, timeoutSeconds int) (out fetchPaidOutput, hash string, done bool, err error) {
	out, done, err = ensurePullGrantTx(ctx, w, agent, req, allowance, timeoutSeconds, &hash)
	return out, hash, done, err
}

func ensurePullGrantTx(ctx context.Context, w *wallet.Wallet, agent string, req paywall.PaymentRequirements, allowance math.Int, timeoutSeconds int, hash *string) (out fetchPaidOutput, done bool, err error) {
	grantee, key := req.Extra.Grantee, pullGrantKey(req.Extra.Grantee)
	pending := func(hash, msg string) (fetchPaidOutput, bool, error) {
		return fetchPaidOutput{Status: "payment_pending", Message: msg,
			Payment: &fetchPaymentDTO{Scheme: paywall.SchemePull, Amount: newAmountDTO(allowance), PayTo: req.PayTo, GrantTxHash: hash}}, true, nil
	}
	c, err := dialChain()
	if err != nil {
		return out, false, err
	}
	defer c.close()

	stateMu.Lock()
	st, err := loadState()
	if err != nil {
		stateMu.Unlock()
		return out, false, err
	}
	rec := st.Sends[key]
	if rec != nil {
		// An allowance already signed: follow it up, never sign another
		// while it might still land.
		stat, _, err := chainStatus(c, rec.TxHash)
		if err != nil {
			stateMu.Unlock()
			return out, false, err
		}
		if stat == statusPending {
			bz, _ := base64.StdEncoding.DecodeString(rec.TxBase64)
			if res, err := c.broadcast(wallet.SignedTx{Bytes: bz}); err == nil && (res.Code == 0 || res.Code == codeTxInMempool) {
				rec.Accepted = true
				_ = st.save()
			}
			stateMu.Unlock()
			*hash = rec.TxHash
			return awaitPullGrant(ctx, c, rec.TxHash, timeoutSeconds, pending)
		}
		// Settled (confirmed, then used up or replaced; or failed):
		// a new allowance may be granted.
		delete(st.Sends, key)
	}
	if allowance.Int64() > perTxLimit {
		stateMu.Unlock()
		return out, false, newError(codePerTxLimit, fmt.Sprintf("a %s AETH allowance exceeds the per-transaction limit of %s AETH; ask for a smaller pullAllowance", formatAeth(allowance), formatAeth(math.NewInt(perTxLimit))))
	}
	// Granting an allowance commits up to its amount: the owner approves
	// it like a payment of that much.
	gate := sendAethInput{To: grantee, Amount: allowance.String() + baseDenom, Memo: pullGrantMemo(req.PayTo), IdempotencyKey: key + "/" + allowance.String()}
	if proceed, p, err := approvalGate(st, agent, gate, allowance); !proceed {
		stateMu.Unlock()
		if err != nil {
			return out, false, err
		}
		return fetchPaidOutput{Status: "approval_pending", ApprovalID: p.ApprovalID, Message: "granting this service a " + formatAeth(allowance) + " AETH allowance needs the owner's approval: " + p.Message,
			Payment: &fetchPaymentDTO{Scheme: paywall.SchemePull, Amount: newAmountDTO(allowance), PayTo: req.PayTo}}, true, nil
	}
	accNum, chainSeq, err := c.accountInfo(agent)
	if status.Code(err) == codes.NotFound {
		stateMu.Unlock()
		return out, false, newError(codeAccountNotFound, fmt.Sprintf("the agent account %s doesn't exist on chain yet: send it some AETH first", agent))
	}
	if err != nil {
		stateMu.Unlock()
		return out, false, err
	}
	msg, err := wallet.SendGrantMsg(agent, grantee, sdk.NewCoins(sdk.NewCoin(baseDenom, allowance)), []string{req.PayTo}, time.Now().Add(pullGrantDays*24*time.Hour))
	if err != nil {
		stateMu.Unlock()
		return out, false, newError(codePaymentUnsupported, err.Error())
	}
	seq := st.nextSequence(agent, chainSeq)
	signed, err := w.BuildAndSignMsgTx(accountName, msg, wallet.TxParams{
		ChainID: chainID, AccountNumber: accNum, Sequence: seq, GasLimit: 400_000,
		Fees: sdk.NewCoins(sdk.NewCoin(baseDenom, math.ZeroInt())),
	})
	if err != nil {
		stateMu.Unlock()
		return out, false, fmt.Errorf("failed to sign the allowance: %w", err)
	}
	rec = &sendRecord{Kind: sendKindPullGrant, From: agent, To: grantee, Amount: allowance.String(), Memo: pullGrantMemo(req.PayTo),
		TxHash: wallet.TxHash(signed), TxBase64: base64.StdEncoding.EncodeToString(signed.Bytes), Sequence: seq, CreatedAt: time.Now()}
	st.Sends[key] = rec
	consumeApproval(st, gate.IdempotencyKey)
	// Recorded before it's broadcast: a retry re-sends these bytes.
	if err := st.save(); err != nil {
		stateMu.Unlock()
		return out, false, fmt.Errorf("failed to record the allowance before sending (nothing was sent): %w", err)
	}
	res, err := c.broadcast(signed)
	if err == nil && res.Code != 0 && res.Code != codeTxInMempool {
		delete(st.Sends, key)
		_ = st.save()
		stateMu.Unlock()
		return out, false, newError(chainErrorCode(res.Codespace, res.Code, res.RawLog, codeTxRejected), "the allowance was rejected: "+res.RawLog)
	}
	if err == nil {
		rec.Accepted = true
		_ = st.save()
	}
	stateMu.Unlock()
	if err != nil {
		e := newError(codeBroadcastUncertain, fmt.Sprintf("broadcasting the allowance failed (%v); call fetch_paid again with the same idempotencyKey -- it re-sends the same one", err))
		e.TxHash = rec.TxHash
		return out, false, e
	}
	notify("pull_allowance_granted", map[string]any{"grantee": grantee, "payTo": req.PayTo, "allowance": newAmountDTO(allowance), "txHash": rec.TxHash})
	*hash = rec.TxHash
	return awaitPullGrant(ctx, c, rec.TxHash, timeoutSeconds, pending)
}

func awaitPullGrant(ctx context.Context, c chain, hash string, timeoutSeconds int, pending func(hash, msg string) (fetchPaidOutput, bool, error)) (fetchPaidOutput, bool, error) {
	if timeoutSeconds <= 0 {
		timeoutSeconds = defaultFetchWait
	}
	confirmed, err := awaitTransaction(ctx, c, hash, time.Now().Add(clampWait(timeoutSeconds)))
	if err != nil {
		return fetchPaidOutput{}, false, err
	}
	switch confirmed.Status {
	case statusPending:
		return pending(hash, "granted this service an allowance, not in a block yet. Call fetch_paid again with the same idempotencyKey to continue -- it won't grant again")
	case statusFailed:
		e := newError(confirmed.ErrorCode, "the allowance failed on chain: "+confirmed.RawLog)
		e.TxHash = hash
		return fetchPaidOutput{}, false, e
	}
	return fetchPaidOutput{}, false, nil
}
