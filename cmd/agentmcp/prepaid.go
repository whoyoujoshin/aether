package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"cosmossdk.io/math"

	"github.com/whoyoujoshin/aether/paywall"
	"github.com/whoyoujoshin/aether/wallet"
)

// fetch_paid's prepaid path (aether-prepaid, package paywall): deposit
// once with a seller, then pay per request by signing it -- no block
// wait. Opt-in per call with the prepay amount.

// prepaidDeposit is a deposit to one seller not yet known to be
// credited. Recorded before it's sent, so a retried or concurrent call
// presents the same deposit instead of making another.
type prepaidDeposit struct {
	SendKey   string    `json:"sendKey"`
	TxHash    string    `json:"txHash,omitempty"`
	Amount    string    `json:"amountUaeth"`
	CreatedAt time.Time `json:"createdAt"`
}

// requestIDFor is the ID a seller charges at most once: the same
// fetch_paid key always yields the same ID.
func requestIDFor(key string) string {
	sum := sha256.Sum256([]byte("fetch_paid/" + key))
	return hex.EncodeToString(sum[:16])
}

func quoteScheme(r *httpResult, scheme string) (paywall.PaymentRequirements, bool) {
	var pr paywall.PaymentRequired
	if jsonUnmarshal(r.body, &pr) != nil {
		return paywall.PaymentRequirements{}, false
	}
	for _, req := range pr.Accepts {
		if req.Scheme == scheme && req.Network == chainID && req.Asset == paywall.Asset {
			return req, true
		}
	}
	return paywall.PaymentRequirements{}, false
}

func fetchPrepaid(ctx context.Context, in fetchPaidInput, method string, req paywall.PaymentRequirements, maxAmount, prepay math.Int) (fetchPaidOutput, error) {
	price, err := wallet.ParseUaeth(req.MaxAmountRequired)
	if err != nil {
		return fetchPaidOutput{}, newError(codePaymentUnsupported, "the server's price is invalid: "+err.Error())
	}
	if price.GT(maxAmount) {
		return fetchPaidOutput{}, newError(codePriceExceedsMax, fmt.Sprintf("the server asks %s AETH (%s uaeth) per request; your maxAmount is %s AETH. Nothing was paid", formatAeth(price), price, formatAeth(maxAmount)))
	}
	if minDep, err := wallet.ParseUaeth(req.Extra.MinDeposit); err == nil && prepay.LT(minDep) {
		return fetchPaidOutput{}, newError(codePaymentUnsupported, fmt.Sprintf("the server's minimum deposit is %s AETH; prepay is %s AETH", formatAeth(minDep), formatAeth(prepay)))
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
	attempt := func(depositTx string) (*httpResult, error) {
		header, err := paywall.EncodePrepaidPayment(agent.Address, paywall.RequestFields{
			Network: chainID, PayTo: req.PayTo, Host: u.Host, Method: method, Path: path, Body: []byte(in.Body),
			MaxPrice: price, Timestamp: time.Now().Unix(), RequestID: requestIDFor(in.IdempotencyKey), DepositTx: depositTx,
		}, func(msg []byte) ([]byte, []byte, error) { return w.SignBytes(accountName, msg) })
		if err != nil {
			return nil, err
		}
		return doHTTP(ctx, in, method, header)
	}
	paid := func(res *httpResult, depositTx string) fetchPaidOutput {
		out := res.output("paid")
		out.Payment = &fetchPaymentDTO{Scheme: paywall.SchemePrepaid, Amount: newAmountDTO(price), PayTo: req.PayTo, DepositTxHash: depositTx}
		var s paywall.SettlementResponse
		if paywall.DecodeHeader(res.header.Get(paywall.HeaderPaymentResponse), &s) == nil {
			if bal, err := wallet.ParseUaeth(s.Balance); err == nil {
				b := newAmountDTO(bal)
				out.Payment.Balance = &b
				notePrepaidBalance(req.PayTo, serviceBase(u), bal)
			} else if s.Balance == "0" {
				b := newAmountDTO(math.ZeroInt())
				out.Payment.Balance = &b
				notePrepaidBalance(req.PayTo, serviceBase(u), math.ZeroInt())
			}
		}
		return out
	}

	stateMu.Lock()
	st, err := loadState()
	stateMu.Unlock()
	if err != nil {
		return fetchPaidOutput{}, err
	}
	dep := st.Prepaid[req.PayTo]

	if dep == nil {
		// Try the balance first: usually it's enough, and this is instant.
		res, err := attempt("")
		if err != nil {
			return fetchPaidOutput{}, err
		}
		if res.status != http.StatusPaymentRequired {
			return paid(res, ""), nil
		}
		_, reason, _ := quote(res)
		if reason != paywall.ErrInsufficientBalance {
			return fetchPaidOutput{}, refusedAfterPayment(reason, res, "")
		}
		// Top up.
		stateMu.Lock()
		st, err = loadState()
		if err == nil {
			if dep = st.Prepaid[req.PayTo]; dep == nil {
				dep = &prepaidDeposit{SendKey: fmt.Sprintf("prepay/%s/%d", req.PayTo, time.Now().UnixNano()), Amount: prepay.String(), CreatedAt: time.Now()}
				st.Prepaid[req.PayTo] = dep
				err = st.save()
			}
		}
		stateMu.Unlock()
		if err != nil {
			return fetchPaidOutput{}, err
		}
	}

	depAmount, _ := math.NewIntFromString(dep.Amount)
	_, sent, err := toolSendAeth(ctx, nil, sendAethInput{
		To: req.PayTo, Amount: dep.Amount + baseDenom, Memo: paywall.DepositMemoPrefix + agent.Address, IdempotencyKey: dep.SendKey,
	})
	if err != nil {
		return fetchPaidOutput{}, err
	}
	forget := func() {
		stateMu.Lock()
		if st, err := loadState(); err == nil && st.Prepaid[req.PayTo] != nil && st.Prepaid[req.PayTo].SendKey == dep.SendKey {
			delete(st.Prepaid, req.PayTo)
			_ = st.save()
		}
		stateMu.Unlock()
	}
	if sent.Status == statusPendingApproval {
		return fetchPaidOutput{Status: "approval_pending", ApprovalID: sent.ApprovalID, Message: sent.Message,
			Payment: &fetchPaymentDTO{Scheme: paywall.SchemePrepaid, Amount: newAmountDTO(depAmount), PayTo: req.PayTo}}, nil
	}
	if sent.Status == statusFailed {
		forget()
		e := newError(sent.ErrorCode, "the deposit was rejected: "+sent.Message)
		e.TxHash = sent.TxHash
		return fetchPaidOutput{}, e
	}
	c, err := dialChain()
	if err != nil {
		return fetchPaidOutput{}, err
	}
	defer c.close()
	wait := in.TimeoutSeconds
	if wait <= 0 {
		wait = defaultFetchWait
	}
	confirmed, err := awaitTransaction(ctx, c, sent.TxHash, time.Now().Add(clampWait(wait)))
	if err != nil {
		return fetchPaidOutput{}, err
	}
	switch confirmed.Status {
	case statusPending:
		return fetchPaidOutput{Status: "payment_pending",
			Payment: &fetchPaymentDTO{Scheme: paywall.SchemePrepaid, Amount: newAmountDTO(depAmount), PayTo: req.PayTo, DepositTxHash: sent.TxHash},
			Message: fmt.Sprintf("deposited %s AETH, not in a block yet. Call fetch_paid again with the same idempotencyKey to continue -- it won't deposit again", formatAeth(depAmount))}, nil
	case statusFailed:
		forget()
		e := newError(confirmed.ErrorCode, "the deposit failed on chain: "+confirmed.RawLog)
		e.TxHash = sent.TxHash
		return fetchPaidOutput{}, e
	}

	for i := 0; ; i++ {
		res, err := attempt(sent.TxHash)
		if err != nil {
			return fetchPaidOutput{}, err
		}
		if res.status != http.StatusPaymentRequired {
			forget() // credited
			return paid(res, sent.TxHash), nil
		}
		_, reason, _ := quote(res)
		if reason == paywall.ErrNotConfirmed && i < proofRetries {
			select {
			case <-ctx.Done():
				return fetchPaidOutput{}, ctx.Err()
			case <-time.After(proofRetryInterval):
			}
			continue
		}
		if reason == paywall.ErrInsufficientBalance || reason == paywall.ErrAlreadyRedeemed {
			forget() // the deposit was credited; it just didn't cover this
		}
		return fetchPaidOutput{}, refusedAfterPayment(reason, res, sent.TxHash)
	}
}
