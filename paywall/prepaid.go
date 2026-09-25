package paywall

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/wallet"
)

// The aether-prepaid scheme, for agents making many small requests:
//
//  1. Deposit once: send any amount (at least MinDeposit) to payTo with
//     memo "prepaid:<address>". The address is the account credited --
//     anyone can fund it, e.g. a person funding their bot.
//  2. Pay per request instantly: sign each request with that account's
//     ML-DSA key (PrepaidPayment). The server checks the signature and
//     deducts the price from the balance. No transaction, no block wait.
//  3. The first request after a deposit names the deposit's hash, which
//     credits it (once).
//
// Each signed request names a RequestID, charged at most once: a
// replayed header -- or a client retrying after a lost response --
// is refused, never charged again. The signature covers the network,
// payee, host, method, path, body and the most the client will pay, so
// it can't be reused for anything else.
//
// The unspent balance is held by the seller: deposit only what you'd
// trust this service with. A seller with a Payout configured pays it
// back on request (withdraw.go).

const (
	// signedRequestWindow bounds how old or future-dated a signed request
	// may be.
	signedRequestWindow = 5 * time.Minute
	// requestIDRetention is how long a charged RequestID is remembered.
	requestIDRetention = 24 * time.Hour
	// maxSignedBody is the largest request body a signature covers.
	maxSignedBody = 10 << 20

	// signingDomain starts every signed request. Transaction sign bytes
	// begin with 0x0a (protobuf SignDoc) or '{' (amino JSON), so a
	// request signature can never be mistaken for a transaction's.
	signingDomain = "aether-prepaid-request/v1\n"
)

// PrepaidConfig enables the aether-prepaid scheme.
type PrepaidConfig struct {
	Ledger     Ledger
	MinDeposit math.Int // uaeth; at least the price; also the smallest partial withdrawal
	// Payout, if set, lets agents withdraw unspent balances (see
	// WithdrawHandler).
	Payout Payout
}

// PrepaidPayment is an aether-prepaid X-PAYMENT payload.
type PrepaidPayment struct {
	Account   string `json:"account"`
	PubKey    string `json:"pubKey"` // base64 ML-DSA-44 public key
	Timestamp int64  `json:"timestamp"`
	RequestID string `json:"requestId"`
	MaxPrice  string `json:"maxPrice"` // uaeth
	DepositTx string `json:"depositTx,omitempty"`
	Signature string `json:"signature"` // base64, over SigningMessage
}

// RequestFields are what a prepaid signature covers.
type RequestFields struct {
	Network, PayTo, Host, Method, Path string
	Body                               []byte
	MaxPrice                           math.Int
	Timestamp                          int64
	RequestID, DepositTx               string
}

// SigningMessage is the exact byte string an aether-prepaid request's
// account signs.
func SigningMessage(f RequestFields) []byte {
	body := sha256.Sum256(f.Body)
	var b strings.Builder
	b.WriteString(signingDomain)
	for _, s := range []string{
		f.Network, f.PayTo, f.Host, f.Method, f.Path, hex.EncodeToString(body[:]),
		f.MaxPrice.String(), strconv.FormatInt(f.Timestamp, 10), f.RequestID, f.DepositTx,
	} {
		b.WriteString(s)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// Signer signs msg with an account's ML-DSA key, returning the
// signature and the public key.
type Signer func(msg []byte) (sig, pubKey []byte, err error)

// EncodePrepaidPayment signs f with sign and builds an aether-prepaid
// X-PAYMENT header value.
func EncodePrepaidPayment(account string, f RequestFields, sign Signer) (string, error) {
	sig, pub, err := sign(SigningMessage(f))
	if err != nil {
		return "", err
	}
	return encodePayment(SchemePrepaid, f.Network, PrepaidPayment{
		Account: account, PubKey: base64.StdEncoding.EncodeToString(pub),
		Timestamp: f.Timestamp, RequestID: f.RequestID, MaxPrice: f.MaxPrice.String(),
		DepositTx: f.DepositTx, Signature: base64.StdEncoding.EncodeToString(sig),
	})
}

// Ledger holds prepaid balances. Implementations must make each method
// atomic and durable: these are customers' funds.
type Ledger interface {
	// Credit adds a deposit to account, once per transaction hash.
	Credit(depositTx, account string, amount math.Int) (balance math.Int, credited bool, err error)
	// Charge deducts amount for a request ID not charged before. ok is
	// false (and nothing changes) if the balance is short; fresh is
	// false if the request ID was already charged.
	Charge(account, requestID string, amount math.Int, forgetAfter time.Time) (balance math.Int, ok, fresh bool, err error)
	// Refund reverses a Charge, forgetting the request ID.
	Refund(account, requestID string, amount math.Int) error
	Balance(account string) (math.Int, error)

	// ReserveWithdrawal deducts a withdrawal from account's balance and
	// records it under id, once: if id is already recorded it returns
	// that record with fresh false and changes nothing. amount nil
	// means the whole balance. It fails with ErrLedgerInsufficient if
	// the balance is short (or empty), and ErrLedgerBelowMinimum if
	// amount is under min without being the whole balance.
	ReserveWithdrawal(account, id string, amount *math.Int, min math.Int, at time.Time) (w Withdrawal, balance math.Int, fresh bool, err error)
	// SaveWithdrawal updates a reserved withdrawal's record.
	SaveWithdrawal(w Withdrawal) error
	// CancelWithdrawal returns a reserved withdrawal's amount to the
	// balance and forgets it. Only for a payout that can never land.
	CancelWithdrawal(account, id string) error
}

// signedRequest is an aether-prepaid request whose signature checked
// out: the account is who it claims.
type signedRequest struct {
	pay      PrepaidPayment
	maxPrice math.Int
	body     []byte
}

// refusal is why a signed request was turned down.
type refusal struct {
	code, message string
	status        int // HTTP status if not 402
}

// verifySigned checks an aether-prepaid payload against r, reading
// (and replacing) r.Body.
func (p *Paywall) verifySigned(r *http.Request, raw json.RawMessage) (*signedRequest, *refusal) {
	var pay PrepaidPayment
	if err := json.Unmarshal(raw, &pay); err != nil {
		return nil, &refusal{code: ErrInvalidPayment, message: "payload must be a signed prepaid request"}
	}
	accAddr, err := sdk.AccAddressFromBech32(pay.Account)
	if err != nil {
		return nil, &refusal{code: ErrInvalidSignature, message: "invalid account address"}
	}
	pubBytes, err := base64.StdEncoding.DecodeString(pay.PubKey)
	if err != nil || len(pubBytes) != mldsa.PubKeySize {
		return nil, &refusal{code: ErrInvalidSignature, message: "pubKey must be a base64 ML-DSA-44 public key"}
	}
	pub := &mldsa.PubKey{Key: pubBytes}
	if !bytes.Equal(pub.Address(), accAddr) {
		return nil, &refusal{code: ErrInvalidSignature, message: "pubKey does not belong to account"}
	}
	sig, err := base64.StdEncoding.DecodeString(pay.Signature)
	if err != nil {
		return nil, &refusal{code: ErrInvalidSignature, message: "signature must be base64"}
	}
	now := p.cfg.Now()
	signedAt := time.Unix(pay.Timestamp, 0)
	if signedAt.Before(now.Add(-signedRequestWindow)) || signedAt.After(now.Add(signedRequestWindow)) {
		return nil, &refusal{code: ErrStaleRequest, message: fmt.Sprintf("sign each request fresh: timestamp must be within %s of the server's clock", signedRequestWindow)}
	}
	if pay.RequestID == "" || len(pay.RequestID) > 128 {
		return nil, &refusal{code: ErrInvalidPayment, message: "requestId is required (1-128 characters)"}
	}
	maxPrice := math.ZeroInt() // "0": a withdrawal, which pays nothing
	if pay.MaxPrice != "0" {
		if maxPrice, err = wallet.ParseUaeth(pay.MaxPrice); err != nil {
			return nil, &refusal{code: ErrInvalidPayment, message: "maxPrice: " + err.Error()}
		}
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxSignedBody+1))
	if err != nil {
		return nil, &refusal{message: "failed to read request body", status: http.StatusBadRequest}
	}
	if len(body) > maxSignedBody {
		return nil, &refusal{code: ErrRequestTooLarge, message: fmt.Sprintf("signed requests' bodies are limited to %d bytes", maxSignedBody)}
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	msg := SigningMessage(RequestFields{
		Network: p.cfg.Network, PayTo: p.cfg.PayTo, Host: r.Host, Method: r.Method, Path: r.URL.Path, Body: body,
		MaxPrice: maxPrice, Timestamp: pay.Timestamp, RequestID: pay.RequestID, DepositTx: pay.DepositTx,
	})
	if !pub.VerifySignature(msg, sig) {
		return nil, &refusal{code: ErrInvalidSignature, message: "signature does not match this request"}
	}
	return &signedRequest{pay: pay, maxPrice: maxPrice, body: body}, nil
}

func (p *Paywall) servePrepaid(w http.ResponseWriter, r *http.Request, raw json.RawMessage, next http.Handler) {
	refuse := func(code, msg, account string) { p.paymentRequiredFor(w, r, code, msg, "", account) }

	req, bad := p.verifySigned(r, raw)
	if bad != nil {
		if bad.status != 0 {
			http.Error(w, bad.message, bad.status)
		} else {
			refuse(bad.code, bad.message, "")
		}
		return
	}
	pay, maxPrice, now := req.pay, req.maxPrice, p.cfg.Now()
	// Signed and fresh: from here on the account is who it claims.
	if maxPrice.LT(p.cfg.Price) {
		refuse(ErrPriceAboveMax, fmt.Sprintf("the price is %s uaeth; the request allows at most %s", p.cfg.Price, maxPrice), pay.Account)
		return
	}

	ledger := p.cfg.Prepaid.Ledger
	if pay.DepositTx != "" {
		if !p.creditDeposit(w, r, strings.ToUpper(strings.TrimSpace(pay.DepositTx)), pay.Account) {
			return
		}
	}

	balance, ok, fresh, err := ledger.Charge(pay.Account, pay.RequestID, p.cfg.Price, now.Add(requestIDRetention))
	if err != nil {
		log.Printf("paywall: charging %s: %v", pay.Account, err)
		http.Error(w, "failed to record the charge", http.StatusInternalServerError)
		return
	}
	if !fresh {
		refuse(ErrAlreadyRedeemed, "this requestId was already charged and served", pay.Account)
		return
	}
	if !ok {
		refuse(ErrInsufficientBalance, fmt.Sprintf("balance %s uaeth is less than the price %s uaeth: deposit to %s with memo %s%s", balance, p.cfg.Price, p.cfg.PayTo, DepositMemoPrefix, pay.Account), pay.Account)
		return
	}

	settlement, _ := EncodeHeader(SettlementResponse{Success: true, Network: p.cfg.Network, Payer: pay.Account, Balance: balance.String()})
	w.Header().Set(HeaderPaymentResponse, settlement)
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	next.ServeHTTP(rec, r)
	if rec.status >= 500 {
		if err := ledger.Refund(pay.Account, pay.RequestID, p.cfg.Price); err != nil {
			log.Printf("paywall: refunding %s for a failed request: %v", pay.Account, err)
		}
	}
}

// creditDeposit credits depositTx to the account its memo names. It
// writes the response and returns false if the deposit can't be used.
func (p *Paywall) creditDeposit(w http.ResponseWriter, r *http.Request, txHash, account string) bool {
	refuse := func(code, msg string) { p.paymentRequiredFor(w, r, code, msg, "", account) }
	detail, err := p.cfg.Lookup(txHash)
	if errors.Is(err, wallet.ErrTransactionNotFound) {
		w.Header().Set("Retry-After", "10")
		refuse(ErrNotConfirmed, "deposit "+txHash+" is not in a block yet; retry shortly")
		return false
	}
	if err != nil {
		log.Printf("paywall: looking up deposit %s: %v", txHash, err)
		w.Header().Set("Retry-After", "10")
		http.Error(w, "could not reach the chain to verify the deposit; retry", http.StatusServiceUnavailable)
		return false
	}
	if detail.Code != 0 {
		refuse(ErrPaymentFailed, "deposit "+txHash+" failed on chain")
		return false
	}
	beneficiary, ok := strings.CutPrefix(detail.Memo, DepositMemoPrefix)
	if !ok {
		refuse(ErrInvalidDeposit, "a deposit's memo must be "+DepositMemoPrefix+"<address>")
		return false
	}
	if _, err := sdk.AccAddressFromBech32(beneficiary); err != nil {
		refuse(ErrInvalidDeposit, "the deposit's memo names an invalid address")
		return false
	}
	paid, _ := p.received(detail)
	if paid.LT(p.cfg.Prepaid.MinDeposit) {
		refuse(ErrInsufficient, fmt.Sprintf("deposits must be at least %s uaeth to %s", p.cfg.Prepaid.MinDeposit, p.cfg.PayTo))
		return false
	}
	// Credit whoever the memo names: presenting someone else's deposit
	// only credits them.
	if _, _, err := p.cfg.Prepaid.Ledger.Credit(txHash, beneficiary, paid); err != nil {
		log.Printf("paywall: crediting deposit %s: %v", txHash, err)
		http.Error(w, "failed to record the deposit", http.StatusInternalServerError)
		return false
	}
	return true
}

const prepaidInstructions = "For many requests: deposit at least minDeposit uaeth to payTo with memo depositMemo " +
	"(\"prepaid:\" + the address to credit). Then sign each request with that account's ML-DSA key and send X-PAYMENT: base64 of " +
	`{"x402Version":1,"scheme":"aether-prepaid","network":"<network>","payload":{account,pubKey,timestamp,requestId,maxPrice,depositTx?,signature}}` +
	" -- see package paywall's SigningMessage. The first request after a deposit names it in depositTx. " +
	"Each requestId is charged once. Unspent balance stays with the seller; if withdrawPath is set, POST a request signed the same way there (body {\"amount\":\"all\"}) to get it back."
