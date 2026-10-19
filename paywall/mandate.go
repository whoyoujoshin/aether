package paywall

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// A spending mandate is an owner's signed statement that an agent's
// account may pay on the owner's behalf: up to MaxPerPayment a payment,
// only to PayTo (if set), between NotBefore and Expires. The owner signs
// it with their own ML-DSA key; the agent sends it with each paid request
// (HeaderMandate), and a seller can check, before taking payment, that
// whoever is paying answers to an owner and is acting within what the
// owner allowed (docs/MANDATES.md).
//
// A mandate is a statement, not a lock: the chain doesn't enforce it.
// What binds the agent is the owner's own controls (agentmcp's limits, an
// on-chain grant); what the mandate gives a seller is an accountable
// owner and the scope the owner signed. MaxTotal is stated for the
// record; a seller sees only its own payments and can't add up the rest.

// HeaderMandate carries a mandate with a paid request: base64 of its JSON.
const HeaderMandate = "X-AETHER-MANDATE"

// HeaderMandateOwner tells the upstream which owner a checked mandate
// names. The paywall sets it; it never passes on a caller's.
const HeaderMandateOwner = "X-Aether-Mandate-Owner"

const (
	mandateDomain  = "aether-x402-mandate/v1\n"
	MandateVersion = 1
)

// Mandate is an owner's signed permission for an agent to pay.
type Mandate struct {
	Version int    `json:"v"`
	Network string `json:"network"` // chain ID
	ID      string `json:"id"`      // random hex, to tell mandates apart
	Owner   string `json:"owner"`   // the signer's address
	Agent   string `json:"agent"`   // the account that pays
	// MaxPerPayment is the most one payment may be, as receipts state an
	// amount: a bare number of uaeth, or the amount followed by its denom.
	MaxPerPayment string   `json:"maxPerPayment"`
	MaxTotal      string   `json:"maxTotal,omitempty"` // stated, not checkable by one seller
	PayTo         []string `json:"payTo,omitempty"`    // empty: any payee
	NotBefore     int64    `json:"notBefore"`          // unix seconds
	Expires       int64    `json:"expires"`            // unix seconds
	Purpose       string   `json:"purpose,omitempty"`  // free text, shown to sellers
	OwnerPubKey   string   `json:"ownerPubKey"`        // base64 ML-DSA-44 public key
	Signature     string   `json:"signature"`          // base64, over MandateSigningMessage
}

// MandateSigningMessage is the exact byte string a mandate's owner signs.
func MandateSigningMessage(m Mandate) []byte {
	return lines(mandateDomain, strconv.Itoa(m.Version), m.Network, m.ID, m.Owner, m.Agent, m.MaxPerPayment, m.MaxTotal,
		strings.Join(m.PayTo, ","), strconv.FormatInt(m.NotBefore, 10), strconv.FormatInt(m.Expires, 10), m.Purpose)
}

// NewMandate fills in an ID if m has none, signs m with the owner's key
// and checks the result.
func NewMandate(m Mandate, sign Signer) (*Mandate, error) {
	m.Version = MandateVersion
	if m.ID == "" {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		m.ID = hex.EncodeToString(b)
	}
	if err := m.wellFormed(); err != nil {
		return nil, err
	}
	sig, pub, err := sign(MandateSigningMessage(m))
	if err != nil {
		return nil, err
	}
	m.OwnerPubKey = base64.StdEncoding.EncodeToString(pub)
	m.Signature = base64.StdEncoding.EncodeToString(sig)
	if err := m.Verify(); err != nil {
		return nil, err
	}
	return &m, nil
}

// wellFormed rejects what would make the signed lines ambiguous, and
// what no mandate can mean.
func (m *Mandate) wellFormed() error {
	for _, f := range []string{m.Network, m.ID, m.Owner, m.Agent, m.MaxPerPayment, m.MaxTotal, m.Purpose} {
		if strings.ContainsAny(f, "\r\n") {
			return errors.New("mandate: a field contains a line break")
		}
	}
	for _, p := range m.PayTo {
		if p == "" || strings.ContainsAny(p, ",\r\n") {
			return errors.New("mandate: payTo entries must be addresses")
		}
	}
	if m.Network == "" || m.Owner == "" || m.Agent == "" || m.ID == "" {
		return errors.New("mandate: network, id, owner and agent are required")
	}
	if _, _, err := parseMandateAmount(m.MaxPerPayment); err != nil {
		return fmt.Errorf("mandate: maxPerPayment: %w", err)
	}
	if m.MaxTotal != "" {
		if _, _, err := parseMandateAmount(m.MaxTotal); err != nil {
			return fmt.Errorf("mandate: maxTotal: %w", err)
		}
	}
	if m.Expires <= m.NotBefore {
		return errors.New("mandate: expires must be after notBefore")
	}
	return nil
}

// Verify checks that the mandate is well formed and signed by its owner.
// It says nothing about any payment: see CheckMandate.
func (m *Mandate) Verify() error {
	if m.Version != MandateVersion {
		return fmt.Errorf("mandate: unsupported version %d", m.Version)
	}
	if err := m.wellFormed(); err != nil {
		return err
	}
	pub, err := decodePubKey(m.OwnerPubKey)
	if err != nil {
		return fmt.Errorf("mandate: %w", err)
	}
	if addressOf(pub) != m.Owner {
		return errors.New("mandate: not signed by the owner's key")
	}
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil || !pub.VerifySignature(MandateSigningMessage(*m), sig) {
		return errors.New("mandate: bad signature")
	}
	return nil
}

// MandateExpectation is what a seller knows about a request. Payer is
// empty before payment (the agent isn't known yet).
type MandateExpectation struct {
	Network string
	PayTo   string
	Payer   string
	Amount  math.Int
	Denom   string
	Now     int64
	// Owners, if set, are the only owners this seller accepts.
	Owners []string
}

// CheckMandate verifies m and that it covers this payment.
func CheckMandate(m *Mandate, want MandateExpectation) error {
	if err := m.Verify(); err != nil {
		return err
	}
	if m.Network != want.Network {
		return fmt.Errorf("mandate: for network %s, not %s", m.Network, want.Network)
	}
	if want.Now < m.NotBefore {
		return errors.New("mandate: not valid yet")
	}
	if want.Now >= m.Expires {
		return errors.New("mandate: expired")
	}
	if want.Payer != "" && want.Payer != m.Agent {
		return fmt.Errorf("mandate: for agent %s, but %s paid", m.Agent, want.Payer)
	}
	if len(m.PayTo) > 0 {
		ok := false
		for _, p := range m.PayTo {
			ok = ok || p == want.PayTo
		}
		if !ok {
			return errors.New("mandate: doesn't allow paying this payee")
		}
	}
	if len(want.Owners) > 0 {
		ok := false
		for _, o := range want.Owners {
			ok = ok || o == m.Owner
		}
		if !ok {
			return errors.New("mandate: this service doesn't accept mandates from this owner")
		}
	}
	max, denom, _ := parseMandateAmount(m.MaxPerPayment)
	if denom != want.Denom {
		return fmt.Errorf("mandate: limits payments in %s, and this one is in %s", denom, want.Denom)
	}
	if want.Amount.GT(max) {
		return fmt.Errorf("mandate: allows at most %s a payment; this costs %s", m.MaxPerPayment, ReceiptAmount(want.Amount, want.Denom))
	}
	return nil
}

// parseMandateAmount reads a receipt-style amount: digits, then a denom
// (none for uaeth).
func parseMandateAmount(s string) (math.Int, string, error) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return math.Int{}, "", fmt.Errorf("%q: expected a whole number of base units, then its denom (none for uaeth)", s)
	}
	n, ok := math.NewIntFromString(s[:i])
	if !ok {
		return math.Int{}, "", fmt.Errorf("%q: not a number", s)
	}
	denom := s[i:]
	if denom == "" {
		denom = Asset
	} else if err := sdk.ValidateDenom(denom); err != nil {
		return math.Int{}, "", fmt.Errorf("%q: %v", s, err)
	}
	return n, denom, nil
}

// EncodeMandate is a mandate as HeaderMandate carries it.
func EncodeMandate(m *Mandate) (string, error) {
	bz, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(bz), nil
}

// DecodeMandate reads HeaderMandate.
func DecodeMandate(header string) (*Mandate, error) {
	bz, err := base64.StdEncoding.DecodeString(strings.TrimSpace(header))
	if err != nil {
		return nil, errors.New("mandate: not base64")
	}
	var m Mandate
	if err := json.Unmarshal(bz, &m); err != nil {
		return nil, fmt.Errorf("mandate: %w", err)
	}
	return &m, nil
}

// MandateConfig makes a paywall check mandates.
type MandateConfig struct {
	// Required refuses a paid request without a mandate. Otherwise a
	// mandate is checked only when one is sent.
	Required bool
	// Owners, if set, are the only owners accepted.
	Owners []string
}

// MandateGate checks a request's mandate before the paywall asks for or
// takes payment, so an agent outside its owner's mandate is turned away
// without paying (403, with the reason). The payer is checked after
// payment, where the paywall knows it: see MandatePayerCheck.
func (p *Paywall) MandateGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del(HeaderMandateOwner)
		cfg := p.cfg.Mandates
		if cfg == nil {
			next.ServeHTTP(w, r)
			return
		}
		header := r.Header.Get(HeaderMandate)
		if header == "" {
			if cfg.Required {
				writeMandateError(w, "MANDATE_REQUIRED", "this service sells only to agents with an owner's spending mandate ("+HeaderMandate+")")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		m, err := DecodeMandate(header)
		if err == nil {
			err = CheckMandate(m, p.mandateExpectation(""))
		}
		if err != nil {
			writeMandateError(w, "MANDATE_REFUSED", err.Error())
			return
		}
		next.ServeHTTP(w, r)
	})
}

// MandatePayerCheck runs after payment: it refuses (403) a request whose
// mandate names another agent than the account that paid, and tells the
// upstream the owner (HeaderMandateOwner).
func (p *Paywall) MandatePayerCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get(HeaderMandate)
		if p.cfg.Mandates == nil || header == "" {
			next.ServeHTTP(w, r)
			return
		}
		payer, _ := Settlement(w.Header())
		m, err := DecodeMandate(header)
		if err == nil {
			err = CheckMandate(m, p.mandateExpectation(payer))
		}
		if err != nil {
			writeMandateError(w, "MANDATE_REFUSED", err.Error())
			return
		}
		r.Header.Set(HeaderMandateOwner, m.Owner)
		next.ServeHTTP(w, r)
	})
}

func (p *Paywall) mandateExpectation(payer string) MandateExpectation {
	return MandateExpectation{Network: p.cfg.Network, PayTo: p.cfg.PayTo, Payer: payer, Amount: p.cfg.Price,
		Denom: p.cfg.Asset.Denom, Now: p.cfg.Now().Unix(), Owners: p.cfg.Mandates.Owners}
}

func writeMandateError(w http.ResponseWriter, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "message": msg})
}
