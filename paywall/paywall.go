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

	// InvoiceTTL is how long a client has to get its payment into a
	// block (default 15m). Blocks are ~60s apart.
	InvoiceTTL time.Duration
	// RedeemWindow is how long after that the paid request may be made
	// (default 1h).
	RedeemWindow time.Duration

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
		cfg.InvoiceTTL = 15 * time.Minute
	}
	if cfg.RedeemWindow <= 0 {
		cfg.RedeemWindow = time.Hour
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	p := &Paywall{cfg: cfg, secret: cfg.Secret, store: cfg.Store}
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
		if pay.Scheme != Scheme || pay.Network != p.cfg.Network {
			p.paymentRequired(w, r, ErrUnsupportedScheme, fmt.Sprintf("this server accepts scheme %q on network %q", Scheme, p.cfg.Network), "")
			return
		}
		invoiceID, txHash := pay.Payload.Invoice, strings.ToUpper(strings.TrimSpace(pay.Payload.TxHash))
		inv, err := p.parseInvoice(invoiceID)
		if err != nil {
			p.paymentRequired(w, r, ErrInvalidInvoice, err.Error(), "")
			return
		}
		if inv.resource != resourceKey(r.Method, r.URL.Path) {
			p.paymentRequired(w, r, ErrResourceMismatch, "this invoice was issued for a different request", "")
			return
		}
		now := p.cfg.Now()
		redeemBy := inv.expiry.Add(p.cfg.RedeemWindow)
		if now.After(redeemBy) {
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
		if paidAt, err := time.Parse(time.RFC3339, detail.Timestamp); err != nil || paidAt.After(inv.expiry) {
			p.paymentRequired(w, r, ErrPaidTooLate, "the payment was not in a block before the invoice expired", "")
			return
		}
		if !p.store.Redeem(invoiceID, redeemBy) {
			p.paymentRequired(w, r, ErrAlreadyRedeemed, "this invoice has already been used for a response", "")
			return
		}

		settlement, _ := EncodeHeader(SettlementResponse{Success: true, Transaction: txHash, Network: p.cfg.Network, Payer: payer})
		w.Header().Set(HeaderPaymentResponse, settlement)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if rec.status >= 500 {
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

// paymentRequired writes a 402. It offers invoice if set (a payment
// that may still confirm), else a fresh one.
func (p *Paywall) paymentRequired(w http.ResponseWriter, r *http.Request, code, message, invoiceID string) {
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
