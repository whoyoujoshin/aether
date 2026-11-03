package shopper

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"time"
	"unicode/utf8"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	"github.com/whoyoujoshin/aether/paywall"
	"github.com/whoyoujoshin/aether/wallet"
	"github.com/whoyoujoshin/aether/x402"
)

const (
	// BuyGasLimit is the gas a shop payment asks for. The fee is the
	// node's minimum price times this.
	BuyGasLimit      = 400_000
	maxKeyLen        = 128
	maxResourceTries = 3
)

// Chain is the node reads a buy needs. The service's paywall broadcasts.
type Chain interface {
	Account(ctx context.Context, address string) (number, sequence uint64, err error)
	Tx(ctx context.Context, hash string) (found bool, code uint32, height int64, err error)
	// Fee is what a transaction asking gas pays, preferably in denom.
	// Callers use the node's minimum gas price, the same setting as the
	// other tools (wallet.FeeSetting). The exact scheme pays it in uaeth.
	Fee(ctx context.Context, gas uint64, denom string) (sdk.Coins, error)
}

// BuyInput is one attempt to pay a saved quote.
type BuyInput struct {
	QuoteID        string
	IdempotencyKey string
	Confirm        bool
	DryRun         bool
	Body           []byte
}

// BuyResult is a receipt when a payment landed, plus the decision.
type BuyResult struct {
	Decision Decision `json:"decision"`
	Receipt  *Receipt `json:"receipt,omitempty"`
	Body     string   `json:"body,omitempty"`
}

// Buyer pays one saved quote. Auto is whatever Policy.Auto says; the
// zero value is off, so a buy needs Confirm.
type Buyer struct {
	ChainID     string
	StateDir    string
	Policy      Policy
	Wallet      *wallet.Wallet
	KeyName     string
	Facilitator Facilitator
	Chain       Chain
	HTTP        *http.Client
	// ExplorerURL, when set, is the prefix of the human approval link.
	ExplorerURL string
	// ResourceWait is the pause between retries of a paid request that
	// came back as a server error. Tests set it to zero.
	ResourceWait time.Duration
}

// Buy signs at most one transaction for the idempotency key. The
// service's paywall settles it: the shopper shows it the
// PAYMENT-SIGNATURE and does not broadcast first. A retry sends those
// same bytes or sends nothing.
func (b Buyer) Buy(ctx context.Context, in BuyInput) (BuyResult, error) {
	if in.IdempotencyKey == "" || utf8.RuneCountInString(in.IdempotencyKey) > maxKeyLen {
		return BuyResult{}, fail(CodeRefused, NextStop, "An idempotency key is required, up to 128 characters.")
	}
	if b.ChainID == "" || b.StateDir == "" || b.Wallet == nil || b.Chain == nil {
		return BuyResult{}, fail(CodeRefused, NextStop, "The shopper is missing its chain, state directory, or key.")
	}
	unlock, err := acquireLock(b.StateDir)
	if err != nil {
		return BuyResult{}, err
	}
	defer unlock()

	saved, err := LoadQuote(b.StateDir, in.QuoteID)
	if err != nil {
		return BuyResult{}, fail(CodeRefused, NextQuoteAgain, "That quote is not on this computer. Run quote again.")
	}
	if saved.ChainID != "" && saved.ChainID != b.ChainID {
		return BuyResult{}, fail(CodeQuoteStale, NextQuoteAgain, "That quote is for a different chain. Nothing was spent.")
	}
	offer := saved.Offer
	if offer.Requirements == nil || offer.QuoteID == "" {
		return BuyResult{}, fail(CodeRefused, NextQuoteAgain, "That quote has no live price. Run quote again.")
	}

	prev, err := loadAttempt(b.StateDir, in.IdempotencyKey)
	if err != nil {
		return BuyResult{}, err
	}
	if prev != nil && prev.QuoteID != offer.QuoteID {
		return BuyResult{}, fail(CodeIdempotency, NextNewKey, "That idempotency key was already used for a different quote. Nothing new was signed.")
	}
	if prev != nil && (prev.State == attemptRejected || prev.State == attemptFailed) {
		msg := prev.Reason
		if msg == "" {
			msg = "This attempt already finished without a new payment."
		}
		code := CodeRefused
		if prev.State == attemptFailed {
			code = CodeRefused
		}
		return BuyResult{}, fail(code, NextNewKey, msg)
	}

	acc, err := b.Wallet.GetAccount(b.KeyName)
	if err != nil {
		return BuyResult{}, fail(CodeRefused, NextStop, "The signing key could not be opened.")
	}

	// A finished payment is resumed, not signed again.
	if prev != nil && prev.State == attemptSettled && prev.TxHash != "" {
		return b.resumeSettled(ctx, in, offer, prev)
	}

	skipProbe := prev != nil && prev.TxB64 != "" && prev.State == attemptUnknown
	req := offer.Requirements
	if !skipProbe {
		live, err := ProbePayment(ctx, b.http(), offer.Method, offer.URL, in.Body)
		if err != nil || live.Required == nil {
			return BuyResult{}, fail(CodeFacilitatorDown, NextRetryLater, "The service did not answer with a price. Nothing was spent.")
		}
		got := pickExact(live.Required.Accepts, x402.Network(b.ChainID))
		if got == nil || !sameRequirement(*got, *offer.Requirements) {
			return BuyResult{}, fail(CodeQuoteStale, NextQuoteAgain, "The price changed. Look again before buying. Nothing was spent.")
		}
		if splitsAsked(got.Extra) {
			return BuyResult{}, fail(CodeRefused, NextPickAnother, "This service asks to split the payment. Nothing was spent.")
		}
		req = got
	}

	spent, err := spentSince(b.StateDir, time.Now().Add(-24*time.Hour))
	if err != nil {
		return BuyResult{}, err
	}
	b.Policy.Spent = spent
	if prev == nil || prev.TxB64 == "" {
		decision := b.Policy.Judge(offer)
		switch decision.Code {
		case CodeOverCap, CodeDailyCap, CodeRefused, CodeNotBuyable:
			return BuyResult{Decision: decision}, nil
		case CodeNeedsApproval, CodeUSDCNeedsApproval:
			ok, rejected := b.approvalAllows(offer)
			if rejected {
				return BuyResult{}, fail(CodeRefused, NextStop, "This buy was rejected. Nothing was spent.")
			}
			if !ok {
				req := b.requestApproval(in.IdempotencyKey, offer, decision.Code)
				decision.Message = decision.Message + " Approval " + req.ID + "."
				if req.Link != "" {
					decision.Message += " " + req.Link
				}
				return BuyResult{Decision: decision}, nil
			}
		case CodeNeedsConfirm:
			if !in.Confirm {
				return BuyResult{Decision: decision}, nil
			}
		case CodeInPolicy:
			if !b.Policy.Auto && !in.Confirm {
				decision.Code = CodeNeedsConfirm
				decision.NextAction = NextConfirm
				decision.Message = offer.PriceDisplay + " is within your limit. Nothing is spent until you confirm."
				return BuyResult{Decision: decision}, nil
			}
		default:
			return BuyResult{Decision: decision}, nil
		}
		if in.DryRun {
			note, err := b.feeNote(ctx)
			if err != nil {
				return BuyResult{}, err
			}
			return BuyResult{Decision: decide(CodeDryRun, NextSubmit, "Dry run: nothing was signed. "+note+" "+decision.Message, offer.QuoteID)}, nil
		}
	} else if in.DryRun {
		note, err := b.feeNote(ctx)
		if err != nil {
			return BuyResult{}, err
		}
		return BuyResult{Decision: decide(CodeDryRun, NextSubmit, "Dry run: this attempt already has one signature and it was not sent again. "+note, offer.QuoteID)}, nil
	}

	txBytes, hash, err := b.signature(ctx, in.IdempotencyKey, acc.Address, offer, prev)
	if err != nil {
		return BuyResult{}, err
	}
	payload := paymentPayload(*req, txBytes)

	if prev == nil || (prev.State != attemptUnknown && prev.State != attemptSettled) {
		vr, err := b.Facilitator.Verify(ctx, payload, *req)
		if err != nil {
			return BuyResult{}, fail(CodeFacilitatorDown, NextRetryLater, "The payment service is not answering. Nothing was spent. Try again in a minute.")
		}
		if !vr.IsValid {
			found, _, _, lookErr := b.Chain.Tx(ctx, hash)
			if lookErr == nil && !found {
				_ = b.mark(in, offer, acc.Address, hash, txBytes, attemptRejected, vr.InvalidReason, 0, 0)
				return BuyResult{}, fail(CodeRefused, NextNewKey, "The payment was refused before it was sent. Nothing was spent. Use a new idempotency key to try again.")
			}
		}
	}

	// Show the signed payment to the service. Its paywall settles that
	// header and serves the body only for a payment it broadcast itself.
	// POST /settle on the public facilitator first would land the
	// transaction, use up the sequence, and the paywall would answer 402.
	status, body, tries, reason, hdr, ferr := b.fetchPaid(ctx, offer, payload, prev)
	return b.finish(ctx, in, offer, acc.Address, hash, txBytes, status, body, tries, reason, hdr, ferr)
}

func (b Buyer) resumeSettled(ctx context.Context, in BuyInput, offer Offer, prev *attempt) (BuyResult, error) {
	rec, err := loadReceipt(b.StateDir, prev.TxHash)
	if err != nil {
		rec = b.receipt(in, offer, prev.Payer, prev.TxHash, x402.SettleResponse{Success: true, Transaction: prev.TxHash}, true, 0, 0, prev.HTTPStatus)
	}
	if delivered(prev.HTTPStatus) || delivered(rec.HTTPStatus) {
		d := decide(CodeAlreadyPaid, NextReadReceipt, "You already paid for this. Here is the receipt.", offer.QuoteID)
		d.Spent = true
		return BuyResult{Decision: d, Receipt: &rec}, nil
	}
	txBytes, err := base64.StdEncoding.DecodeString(prev.TxB64)
	if err != nil || len(txBytes) == 0 || offer.Requirements == nil {
		d := notDelivered(offer, prev.TxHash)
		d.Spent = true
		return BuyResult{Decision: d, Receipt: &rec}, nil
	}
	status, body, tries, reason, hdr, ferr := b.fetchPaid(ctx, offer, paymentPayload(*offer.Requirements, txBytes), prev)
	return b.finish(ctx, in, offer, prev.Payer, prev.TxHash, txBytes, status, body, tries, reason, hdr, ferr)
}

// finish records the chain result after the resource has been asked.
// A 2xx with a successful transaction is the only "paid" result. A
// transaction that landed while the resource is still 402 is reported
// as paid but not delivered, and a later call retries the same proof.
func (b Buyer) finish(ctx context.Context, in BuyInput, offer Offer, payer, hash string, tx []byte, status int, body string, tries int, reason string, hdr http.Header, ferr error) (BuyResult, error) {
	found, code, height, lookErr := b.Chain.Tx(ctx, hash)
	if lookErr != nil {
		_ = b.mark(in, offer, payer, hash, tx, attemptUnknown, "", tries, status)
		return BuyResult{}, fail(CodeSettlementUnknown, NextPoll, "The payment may have been sent. Check status with the same idempotency key. Nothing new was signed.")
	}
	settled := settlementFrom(hdr, hash, payer, b.ChainID, offer.Amount)
	if delivered(status) {
		if !found {
			_ = b.mark(in, offer, payer, hash, tx, attemptUnknown, "", tries, status)
			return BuyResult{}, fail(CodeSettlementUnknown, NextPoll, "The service answered, and the payment is not in a block yet. Run the same command to check. Nothing new was signed.")
		}
		if code != 0 {
			_ = b.mark(in, offer, payer, hash, tx, attemptFailed, "the payment failed on chain", tries, status)
			rec := b.receipt(in, offer, payer, hash, settled, true, code, height, status)
			_ = saveReceipt(b.StateDir, rec)
			return BuyResult{Receipt: &rec}, fail(CodeRefused, NextNewKey, "The payment was included and failed. Nothing new was signed.")
		}
		settled.Success = true
		rec := b.receipt(in, offer, payer, hash, settled, true, 0, height, status)
		_ = saveReceipt(b.StateDir, rec)
		_ = b.mark(in, offer, payer, hash, tx, attemptSettled, "", tries, status)
		out := BuyResult{Decision: decide(CodeAlreadyPaid, NextReadReceipt, "Paid "+offer.PriceDisplay+".", offer.QuoteID), Receipt: &rec, Body: body}
		out.Decision.Spent = true
		return out, nil
	}
	if found && code == 0 {
		rec := b.receipt(in, offer, payer, hash, settled, true, 0, height, status)
		_ = saveReceipt(b.StateDir, rec)
		_ = b.mark(in, offer, payer, hash, tx, attemptSettled, reason, tries, status)
		d := notDelivered(offer, hash)
		if status >= 500 || ferr != nil {
			d.Message = "You paid, and the service failed. Payment " + hash + ". Run the same command to ask again with the same proof. Nothing new will be signed."
		}
		d.Spent = true
		return BuyResult{Decision: d, Receipt: &rec}, nil
	}
	if found && code != 0 {
		_ = b.mark(in, offer, payer, hash, tx, attemptFailed, "the payment failed on chain", tries, status)
		rec := b.receipt(in, offer, payer, hash, settled, true, code, height, status)
		_ = saveReceipt(b.StateDir, rec)
		return BuyResult{Receipt: &rec}, fail(CodeRefused, NextNewKey, "The payment was included and failed. Nothing new was signed.")
	}
	if status == http.StatusPaymentRequired && preBroadcast(reason) {
		_ = b.mark(in, offer, payer, hash, tx, attemptRejected, reason, tries, status)
		return BuyResult{}, fail(CodeRefused, NextNewKey, "The payment was refused before it was sent. Nothing was spent. Use a new idempotency key to try again.")
	}
	_ = b.mark(in, offer, payer, hash, tx, attemptUnknown, reason, tries, status)
	return BuyResult{}, fail(CodeSettlementUnknown, NextPoll, "The payment was submitted and is not in a block yet. Run the same command to ask again. Nothing new was signed.")
}

func (b Buyer) signature(ctx context.Context, key, payer string, offer Offer, prev *attempt) ([]byte, string, error) {
	if prev != nil && prev.TxB64 != "" {
		bz, err := base64.StdEncoding.DecodeString(prev.TxB64)
		if err != nil || len(bz) == 0 {
			return nil, "", fail(CodeRefused, NextStop, "The stored payment could not be read. Nothing new was signed.")
		}
		return bz, prev.TxHash, nil
	}
	if other, err := otherInFlight(b.StateDir, payer, key); err != nil {
		return nil, "", err
	} else if other != "" {
		return nil, "", fail(CodeSettlementUnknown, NextPoll, "Another payment from this account is still unfinished ("+other+"). Finish that one before signing another. Nothing new was signed.")
	}
	number, seq, err := b.Chain.Account(ctx, payer)
	if err != nil {
		return nil, "", fail(CodeInsufficientFunds, NextGetCoins, "This account is not on the chain yet. Get test coins and try again. Nothing was spent.")
	}
	amount, ok := sdkmath.NewIntFromString(offer.Amount)
	if !ok {
		return nil, "", fail(CodeRefused, NextQuoteAgain, "The saved price is not usable. Run quote again.")
	}
	from, err := sdk.AccAddressFromBech32(payer)
	if err != nil {
		return nil, "", fail(CodeRefused, NextStop, "The signing account is not a valid address.")
	}
	to, err := sdk.AccAddressFromBech32(offer.PayTo)
	if err != nil {
		return nil, "", fail(CodeRefused, NextPickAnother, "The service's account is not a valid address. Nothing was spent.")
	}
	msg := banktypes.NewMsgSend(from, to, sdk.NewCoins(sdk.NewCoin(offer.Asset, amount)))
	// The exact scheme's fee is uaeth, whatever coin the price is in,
	// and it has to cover the node's minimum gas price.
	fees, err := b.Chain.Fee(ctx, BuyGasLimit, wallet.BaseDenom)
	if err != nil {
		return nil, "", fail(CodeFacilitatorDown, NextRetryLater, "The node's gas price could not be read. Nothing was signed.")
	}
	signed, err := b.Wallet.BuildAndSignMsgTx(b.KeyName, msg, wallet.TxParams{
		ChainID: b.ChainID, AccountNumber: number, Sequence: seq, GasLimit: BuyGasLimit,
		Fees: fees,
	})
	if err != nil {
		return nil, "", fail(CodeRefused, NextStop, "The payment could not be signed. Nothing was spent.")
	}
	hash := x402.TxHash(signed.Bytes)
	if err := b.mark(BuyInput{IdempotencyKey: key}, offer, payer, hash, signed.Bytes, attemptSigned, "", 0, 0); err != nil {
		return nil, "", err
	}
	return signed.Bytes, hash, nil
}

func (b Buyer) mark(in BuyInput, offer Offer, payer, hash string, tx []byte, state, reason string, tries, httpStatus int) error {
	prev, _ := loadAttempt(b.StateDir, in.IdempotencyKey)
	triesOut := tries
	statusOut := httpStatus
	if prev != nil && tries < prev.ResourceTries {
		triesOut = prev.ResourceTries
	}
	if prev != nil && statusOut == 0 {
		statusOut = prev.HTTPStatus
	}
	return saveAttempt(b.StateDir, &attempt{
		Key: in.IdempotencyKey, QuoteID: offer.QuoteID, Payer: payer, TxHash: hash,
		TxB64: base64.StdEncoding.EncodeToString(tx), State: state, URL: offer.URL, Method: offer.Method,
		Amount: offer.Amount, Asset: offer.Asset, PayTo: offer.PayTo, Reason: reason,
		ResourceTries: triesOut, HTTPStatus: statusOut,
	})
}

func (b Buyer) fetchPaid(ctx context.Context, offer Offer, payload x402.PaymentPayload, prev *attempt) (status int, body string, tries int, reason string, hdr http.Header, err error) {
	header, err := paywall.EncodeHeader(payload)
	if err != nil {
		return 0, "", 0, "", nil, err
	}
	base := 0
	if prev != nil {
		base = prev.ResourceTries
	}
	wait := b.ResourceWait
	switch {
	case wait < 0:
		wait = 0
	case wait == 0:
		wait = 10 * time.Second
	}
	var lastErr error
	for made := 0; made < maxResourceTries; made++ {
		if made > 0 {
			select {
			case <-ctx.Done():
				return status, body, base + made, reason, hdr, ctx.Err()
			case <-time.After(wait):
			}
		}
		var h http.Header
		status, body, h, lastErr = b.oneRequest(ctx, offer, header)
		tries = base + made + 1
		if lastErr != nil {
			continue
		}
		hdr = h
		reason = paymentError(h)
		// A 402 that is not "wait for the block" is the service's answer.
		// 5xx and payment_not_confirmed are retried with the same header.
		if status < 500 && reason != paywall.ErrNotConfirmed {
			return status, body, tries, reason, hdr, nil
		}
	}
	if lastErr == nil && status >= 500 {
		lastErr = fail(CodePaidNotDelivered, NextRefetch, "You paid, and the service failed.")
	}
	return status, body, tries, reason, hdr, lastErr
}

func (b Buyer) oneRequest(ctx context.Context, offer Offer, header string) (int, string, http.Header, error) {
	var rdr io.Reader
	req, err := http.NewRequestWithContext(ctx, offer.Method, offer.URL, rdr)
	if err != nil {
		return 0, "", nil, err
	}
	req.Header.Set(paywall.HeaderPaymentSignature, header)
	resp, err := b.http().Do(req)
	if err != nil {
		return 0, "", nil, err
	}
	defer resp.Body.Close()
	buf, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return resp.StatusCode, "", resp.Header, err
	}
	return resp.StatusCode, string(buf), resp.Header, nil
}

func (b Buyer) feeNote(ctx context.Context) (string, error) {
	fees, err := b.Chain.Fee(ctx, BuyGasLimit, wallet.BaseDenom)
	if err != nil {
		return "", fail(CodeFacilitatorDown, NextRetryLater, "The node's gas price could not be read. Nothing was signed.")
	}
	if len(fees) == 0 {
		return "Fee: none. The node asks no minimum gas price.", nil
	}
	return "Fee: " + fees.String() + ".", nil
}

func (b Buyer) receipt(in BuyInput, offer Offer, payer, hash string, settled x402.SettleResponse, found bool, code uint32, height int64, status int) Receipt {
	if settled.Transaction == "" {
		settled.Transaction = hash
	}
	if settled.Network == "" {
		settled.Network = x402.Network(b.ChainID)
	}
	return Receipt{
		ChainID: b.ChainID, Network: x402.Network(b.ChainID), Payer: payer, PayTo: offer.PayTo,
		Asset: offer.Asset, Amount: offer.Amount, Rail: RailAPI, Method: offer.Method, URL: offer.URL,
		BodyHash: offer.BodyHash, QuoteID: offer.QuoteID, IdempotencyKey: in.IdempotencyKey, TxHash: hash,
		Facilitator: settled, ChainFound: found, ChainCode: code, ChainHeight: height, HTTPStatus: status,
		At: time.Now().UTC(),
	}
}

func (b Buyer) http() *http.Client {
	if b.HTTP != nil {
		return b.HTTP
	}
	return &http.Client{Timeout: 150 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func delivered(status int) bool {
	return status >= 200 && status < 300
}

func notDelivered(offer Offer, hash string) Decision {
	return decide(CodePaidNotDelivered, NextRefetch,
		"Paid but the service didn't deliver. Payment "+hash+". Run the same command to ask again with the same proof. Nothing new will be signed.",
		offer.QuoteID)
}

func paymentError(h http.Header) string {
	if h == nil {
		return ""
	}
	var pr x402.PaymentRequired
	if paywall.DecodeHeader(h.Get(paywall.HeaderPaymentRequired), &pr) != nil {
		return ""
	}
	return pr.Error
}

func settlementFrom(h http.Header, hash, payer, chainID, amount string) x402.SettleResponse {
	var s x402.SettleResponse
	if h != nil {
		_ = paywall.DecodeHeader(h.Get(paywall.HeaderPaymentResponseV2), &s)
	}
	if s.Transaction == "" {
		s.Transaction = hash
	}
	if s.Payer == "" {
		s.Payer = payer
	}
	if s.Network == "" {
		s.Network = x402.Network(chainID)
	}
	if s.Amount == "" {
		s.Amount = amount
	}
	return s
}

func paymentPayload(req x402.PaymentRequirements, tx []byte) x402.PaymentPayload {
	raw, _ := json.Marshal(x402.ExactPayload{Transaction: base64.StdEncoding.EncodeToString(tx)})
	return x402.PaymentPayload{X402Version: x402.Version, Accepted: req, Payload: raw}
}

func sameRequirement(a, b x402.PaymentRequirements) bool {
	return a.Scheme == b.Scheme && a.Network == b.Network && a.Amount == b.Amount && a.Asset == b.Asset && a.PayTo == b.PayTo && !splitsAsked(a.Extra) && !splitsAsked(b.Extra)
}
