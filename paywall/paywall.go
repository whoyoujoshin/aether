package paywall

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/wallet"
)

// Config sets what a Paywall charges and how it checks payment.
type Config struct {
	PayTo       string   // address payments must go to
	Price       math.Int // per request, in uaeth
	Network     string   // chain ID
	Description string
	MimeType    string

	// InvoiceTTL is how long a client has to pay an invoice and present
	// the payment (default 24h). A payment is served whenever it lands
	// within that time -- being slow to confirm never forfeits it.
	InvoiceTTL time.Duration

	// Secret signs invoices. Nil picks a random one, so invoices die
	// with the process -- and so can't be replayed after a restart
	// wipes an in-memory Store. Instances sharing a Secret must share a
	// Store.
	Secret []byte
	Store  RedeemedStore

	// Lookup fetches a transaction by hash, returning an error wrapping
	// wallet.ErrTransactionNotFound if it isn't in a block.
	Lookup func(hash string) (*wallet.TransactionDetail, error)
	Now    func() time.Time

	// Prepaid, if set, also offers the aether-prepaid scheme.
	Prepaid *PrepaidConfig
	// Pull, if set, also offers the aether-pull scheme; run the
	// Paywall's RunCollector alongside it.
	Pull *PullConfig
	// Receipts, if set, signs a receipt for every paid response.
	Receipts *ReceiptConfig
}

// RedeemedStore remembers which invoices have been used.
type RedeemedStore interface {
	// Redeem marks invoice used, reporting false if it already was.
	// It may forget it after until.
	Redeem(invoice string, until time.Time) bool
	// Release un-marks it, so the same payment can be used again.
	Release(invoice string)
}

// Paywall is HTTP middleware charging per request.
type Paywall struct {
	cfg    Config
	secret []byte
	store  RedeemedStore

	withdrawMu sync.Mutex
	pull       *pullState
}

func New(cfg Config) (*Paywall, error) {
	if _, err := sdk.AccAddressFromBech32(cfg.PayTo); err != nil {
		return nil, fmt.Errorf("invalid payTo address %q: %w", cfg.PayTo, err)
	}
	if cfg.Price.IsNil() || !cfg.Price.IsPositive() || !cfg.Price.IsUint64() {
		return nil, errors.New("price must be a positive uaeth amount")
	}
	if cfg.Network == "" || cfg.Lookup == nil {
		return nil, errors.New("network and lookup are required")
	}
	if cfg.InvoiceTTL <= 0 {
		cfg.InvoiceTTL = 24 * time.Hour
	}
	if cfg.Prepaid != nil {
		if cfg.Prepaid.Ledger == nil {
			return nil, errors.New("prepaid needs a ledger")
		}
		if cfg.Prepaid.MinDeposit.IsNil() || cfg.Prepaid.MinDeposit.LT(cfg.Price) {
			cfg.Prepaid.MinDeposit = cfg.Price
		}
	}
	if cfg.Pull != nil {
		pc := cfg.Pull
		if pc.Ledger == nil || pc.Collector == nil || pc.Grants == nil {
			return nil, errors.New("pull needs a ledger, a collector and a grant lookup")
		}
		if _, err := sdk.AccAddressFromBech32(pc.Grantee); err != nil {
			return nil, fmt.Errorf("invalid pull grantee %q: %w", pc.Grantee, err)
		}
		if pc.Credit.IsNil() {
			pc.Credit = cfg.Price.MulRaw(defaultCreditRequests)
		}
		if pc.Credit.LT(cfg.Price) {
			return nil, errors.New("pull credit must be at least the price")
		}
		if pc.CollectEvery <= 0 {
			pc.CollectEvery = time.Minute
		}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Receipts != nil {
		if cfg.Receipts.Sign == nil {
			return nil, errors.New("receipts need a signer")
		}
		if err := checkReceiptConfig(cfg.Receipts, cfg.PayTo, cfg.Now().Unix()); err != nil {
			return nil, fmt.Errorf("receipts wouldn't verify: %w", err)
		}
	}
	p := &Paywall{cfg: cfg, secret: cfg.Secret, store: cfg.Store, pull: newPullState()}
	if p.secret == nil {
		p.secret = make([]byte, 32)
		if _, err := rand.Read(p.secret); err != nil {
			return nil, err
		}
	}
	if p.store == nil {
		p.store = NewMemoryStore()
	}
	return p, nil
}

// Middleware serves next only to requests that paid.
func (p *Paywall) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get(HeaderPayment)
		if header == "" {
			p.paymentRequired(w, r, ErrPaymentRequired, "this resource costs "+wallet.FormatAeth(p.cfg.Price)+" AETH per request", "")
			return
		}
		var pay PaymentPayload
		if err := DecodeHeader(header, &pay); err != nil {
			p.paymentRequired(w, r, ErrInvalidPayment, "X-PAYMENT must be base64-encoded JSON", "")
			return
		}
		if pay.Network == p.cfg.Network && pay.Scheme == SchemePrepaid && p.cfg.Prepaid != nil {
			p.servePrepaid(w, r, pay.Payload, next)
			return
		}
		if pay.Network == p.cfg.Network && pay.Scheme == SchemePull && p.cfg.Pull != nil {
			p.servePull(w, r, pay.Payload, next)
			return
		}
		if pay.Scheme != Scheme || pay.Network != p.cfg.Network {
			p.paymentRequired(w, r, ErrUnsupportedScheme, fmt.Sprintf("this server accepts %s on network %q", strings.Join(p.schemes(), " or "), p.cfg.Network), "")
			return
		}
		var memo MemoPayment
		if err := json.Unmarshal(pay.Payload, &memo); err != nil {
			p.paymentRequired(w, r, ErrInvalidPayment, "payload must be {invoice, txHash}", "")
			return
		}
		invoiceID, txHash := memo.Invoice, strings.ToUpper(strings.TrimSpace(memo.TxHash))
		inv, err := p.parseInvoice(invoiceID)
		if err != nil {
			p.paymentRequired(w, r, ErrInvalidInvoice, err.Error(), "")
			return
		}
		if inv.resource != resourceKey(r.Method, r.URL.Path) {
			p.paymentRequired(w, r, ErrResourceMismatch, "this invoice was issued for a different request", "")
			return
		}
		if p.cfg.Now().After(inv.expiry) {
			p.paymentRequired(w, r, ErrInvoiceExpired, "this invoice can no longer be redeemed", "")
			return
		}

		detail, err := p.cfg.Lookup(txHash)
		if errors.Is(err, wallet.ErrTransactionNotFound) {
			// Keep the same invoice: the payment may just not be in a
			// block yet.
			w.Header().Set("Retry-After", "10")
			p.paymentRequired(w, r, ErrNotConfirmed, "transaction "+txHash+" is not in a block yet; retry shortly with the same X-PAYMENT", invoiceID)
			return
		}
		if err != nil {
			log.Printf("paywall: looking up %s: %v", txHash, err)
			w.Header().Set("Retry-After", "10")
			http.Error(w, "could not reach the chain to verify payment; retry with the same X-PAYMENT", http.StatusServiceUnavailable)
			return
		}
		if detail.Code != 0 {
			p.paymentRequired(w, r, ErrPaymentFailed, "transaction "+txHash+" failed on chain", "")
			return
		}
		if detail.Memo != invoiceID {
			p.paymentRequired(w, r, ErrMemoMismatch, "the transaction's memo must be exactly the invoice", "")
			return
		}
		paid, payer := p.received(detail)
		if paid.LT(math.NewIntFromUint64(inv.price)) {
			p.paymentRequired(w, r, ErrInsufficient, fmt.Sprintf("paid %s uaeth to %s; the invoice is for %d uaeth", paid, p.cfg.PayTo, inv.price), "")
			return
		}
		if !p.store.Redeem(invoiceID, inv.expiry) {
			p.paymentRequired(w, r, ErrAlreadyRedeemed, "this invoice has already been used for a response", "")
			return
		}

		settlement, _ := EncodeHeader(SettlementResponse{Success: true, Transaction: txHash, Network: p.cfg.Network, Payer: payer})
		w.Header().Set(HeaderPaymentResponse, settlement)
		bought := paidRequest{scheme: Scheme, payer: payer, payment: txHash}
		if p.cfg.Receipts != nil {
			bought.body = readForReceipt(r)
		}
		if status := p.serveReceipted(w, r, next, bought); status >= 500 {
			// The server failed, not the client: let the same payment
			// be used again.
			p.store.Release(invoiceID)
		}
	})
}

// Payer returns the address that paid for this request, as recorded
// in the X-PAYMENT-RESPONSE header the middleware set.
func Payer(w http.ResponseWriter) string {
	var s SettlementResponse
	if DecodeHeader(w.Header().Get(HeaderPaymentResponse), &s) != nil {
		return ""
	}
	return s.Payer
}

func (p *Paywall) received(d *wallet.TransactionDetail) (math.Int, string) {
	total, payer := math.ZeroInt(), ""
	for _, t := range d.Transfers {
		if t.To != p.cfg.PayTo {
			continue
		}
		coins, err := sdk.ParseCoinsNormalized(t.Amount)
		if err != nil {
			continue
		}
		total = total.Add(coins.AmountOf(Asset))
		if payer == "" {
			payer = t.From
		}
	}
	return total, payer
}

func (p *Paywall) schemes() []string {
	out := []string{Scheme}
	if p.cfg.Prepaid != nil {
		out = append(out, SchemePrepaid)
	}
	if p.cfg.Pull != nil {
		out = append(out, SchemePull)
	}
	return out
}

// paymentRequired writes a 402. It offers invoice if set (a payment
// that may still confirm), else a fresh one.
func (p *Paywall) paymentRequired(w http.ResponseWriter, r *http.Request, code, message, invoiceID string) {
	p.paymentRequiredFor(w, r, code, message, invoiceID, "")
}

// paymentRequiredFor also reports account's prepaid balance, if named.
func (p *Paywall) paymentRequiredFor(w http.ResponseWriter, r *http.Request, code, message, invoiceID, account string) {
	expiry := p.cfg.Now().Add(p.cfg.InvoiceTTL).Truncate(time.Second)
	if invoiceID != "" {
		if inv, err := p.parseInvoice(invoiceID); err == nil {
			expiry = inv.expiry
		}
	} else {
		var err error
		invoiceID, err = p.newInvoice(invoice{expiry: expiry, price: p.cfg.Price.Uint64(), resource: resourceKey(r.Method, r.URL.Path)})
		if err != nil {
			http.Error(w, "failed to issue invoice", http.StatusInternalServerError)
			return
		}
	}
	body := PaymentRequired{
		X402Version: X402Version,
		Error:       code,
		Message:     message,
		Accepts: []PaymentRequirements{{
			Scheme:            Scheme,
			Network:           p.cfg.Network,
			MaxAmountRequired: p.cfg.Price.String(),
			Asset:             Asset,
			PayTo:             p.cfg.PayTo,
			Resource:          resourceURL(r),
			Description:       p.cfg.Description,
			MimeType:          p.cfg.MimeType,
			MaxTimeoutSeconds: int64(p.cfg.InvoiceTTL.Seconds()),
			Extra: Extra{
				Invoice:      invoiceID,
				AmountAeth:   wallet.FormatAeth(p.cfg.Price),
				ExpiresAt:    expiry.UTC().Format(time.RFC3339),
				Instructions: instructions,
			},
		}},
	}
	if p.cfg.Prepaid != nil {
		prepaid := body.Accepts[0]
		prepaid.Scheme = SchemePrepaid
		prepaid.Extra = Extra{
			AmountAeth:   wallet.FormatAeth(p.cfg.Price),
			DepositMemo:  DepositMemoPrefix + "<address>",
			MinDeposit:   p.cfg.Prepaid.MinDeposit.String(),
			Instructions: prepaidInstructions,
		}
		if p.cfg.Prepaid.Payout != nil {
			prepaid.Extra.WithdrawPath = WithdrawPath
		}
		if account != "" {
			if bal, err := p.cfg.Prepaid.Ledger.Balance(account); err == nil {
				prepaid.Extra.Balance = bal.String()
			}
		}
		body.Accepts = append(body.Accepts, prepaid)
	}
	if p.cfg.Pull != nil {
		pull := body.Accepts[0]
		pull.Scheme = SchemePull
		pull.Extra = Extra{
			AmountAeth:   wallet.FormatAeth(p.cfg.Price),
			Grantee:      p.cfg.Pull.Grantee,
			Credit:       p.cfg.Pull.Credit.String(),
			Instructions: pullInstructions,
		}
		if account != "" {
			if a, err := p.cfg.Pull.Ledger.PullAccount(account); err == nil {
				pull.Extra.Owed = a.Owed().String()
			}
		}
		body.Accepts = append(body.Accepts, pull)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusPaymentRequired)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(body)
}

func resourceURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host + r.URL.Path
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status, s.wroteHeader = code, true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.wroteHeader = true
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// MemoryStore is an in-process RedeemedStore.
type MemoryStore struct {
	mu        sync.Mutex
	until     map[string]time.Time
	lastPrune time.Time
	now       func() time.Time
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{until: map[string]time.Time{}, now: time.Now}
}

func (m *MemoryStore) Redeem(invoice string, until time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	if now.Sub(m.lastPrune) > time.Minute {
		for k, t := range m.until {
			if now.After(t) {
				delete(m.until, k)
			}
		}
		m.lastPrune = now
	}
	if _, used := m.until[invoice]; used {
		return false
	}
	m.until[invoice] = until
	return true
}

func (m *MemoryStore) Release(invoice string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.until, invoice)
}
