package paywall

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
)

// Receipts: with Config.Receipts set, every paid response carries a
// signed receipt (X-PAYMENT-RECEIPT, base64 JSON) saying who paid what
// for which request, and what came back: the status and a hash of the
// body. The buyer can show it to its owner, or to anyone, to prove what
// it bought -- and what it got for the money.
//
// Receipts are signed by the payee account's own key, or by another key
// the payee delegated receipts to (ReceiptDelegation), so the payee's
// key can stay offline. Either way anyone can check one against the
// payee's address alone: no manifest or server needs to be trusted.

// HeaderReceipt carries a paid response's receipt.
const HeaderReceipt = "X-PAYMENT-RECEIPT"

const (
	receiptDomain    = "aether-x402-receipt/v1\n"
	delegationDomain = "aether-x402-receipt-delegation/v1\n"
	// maxReceiptBody is the most of a response a receipt hashes; a
	// bigger (or streamed) response gets a receipt without ResponseHash.
	maxReceiptBody = 4 << 20
)

// Receipt is a seller's signed statement about one paid response.
type Receipt struct {
	X402Version int    `json:"x402Version"`
	Network     string `json:"network"`
	PayTo       string `json:"payTo"`
	Payer       string `json:"payer"`
	Scheme      string `json:"scheme"`
	// Payment is the transaction hash (aether-memo) or the request ID
	// (aether-prepaid) that paid.
	Payment     string `json:"payment"`
	Amount      string `json:"amount"` // uaeth
	Method      string `json:"method"`
	Host        string `json:"host"`
	Path        string `json:"path"`
	RequestHash string `json:"requestHash"` // hex SHA-256 of the request body
	Status      int    `json:"status"`
	// ResponseHash is the hex SHA-256 of the response body, or empty if
	// the response was too big or streamed to hash.
	ResponseHash string             `json:"responseHash,omitempty"`
	At           int64              `json:"at"`     // unix seconds
	Signer       string             `json:"signer"` // base64 ML-DSA-44 public key
	Delegation   *ReceiptDelegation `json:"delegation,omitempty"`
	Signature    string             `json:"signature"` // base64, over ReceiptSigningMessage
}

// ReceiptDelegation lets Signer (an address) sign receipts for PayTo
// until Expires. It is signed with the payee's key.
type ReceiptDelegation struct {
	PayTo       string `json:"payTo"`
	Signer      string `json:"signer"`  // address of the receipt key
	Expires     int64  `json:"expires"` // unix seconds
	PayToPubKey string `json:"payToPubKey"`
	Signature   string `json:"signature"` // base64, over DelegationSigningMessage
}

// ReceiptSigningMessage is the exact byte string a receipt's signer signs.
func ReceiptSigningMessage(r Receipt) []byte {
	return lines(receiptDomain, r.Network, r.PayTo, r.Payer, r.Scheme, r.Payment, r.Amount, r.Method, r.Host, r.Path,
		r.RequestHash, strconv.Itoa(r.Status), r.ResponseHash, strconv.FormatInt(r.At, 10))
}

// DelegationSigningMessage is the exact byte string a payee signs to
// delegate receipts.
func DelegationSigningMessage(payTo, signer string, expires int64) []byte {
	return lines(delegationDomain, payTo, signer, strconv.FormatInt(expires, 10))
}

func lines(domain string, fields ...string) []byte {
	var b strings.Builder
	b.WriteString(domain)
	for _, f := range fields {
		b.WriteString(f)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// NewReceiptDelegation signs, with the payee's key, a delegation of
// receipts to the account signer until expires.
func NewReceiptDelegation(payTo, signer string, expires int64, sign Signer) (*ReceiptDelegation, error) {
	sig, pub, err := sign(DelegationSigningMessage(payTo, signer, expires))
	if err != nil {
		return nil, err
	}
	d := &ReceiptDelegation{PayTo: payTo, Signer: signer, Expires: expires,
		PayToPubKey: base64.StdEncoding.EncodeToString(pub), Signature: base64.StdEncoding.EncodeToString(sig)}
	if err := d.verify(); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *ReceiptDelegation) verify() error {
	pub, err := decodePubKey(d.PayToPubKey)
	if err != nil {
		return fmt.Errorf("delegation: %w", err)
	}
	if addressOf(pub) != d.PayTo {
		return errors.New("delegation: not signed by the payee's key")
	}
	sig, err := base64.StdEncoding.DecodeString(d.Signature)
	if err != nil || !pub.VerifySignature(DelegationSigningMessage(d.PayTo, d.Signer, d.Expires), sig) {
		return errors.New("delegation: bad signature")
	}
	return nil
}

// wellFormed rejects what would make the signed lines ambiguous: a field
// with a line break could shift the lines after it into other fields.
func (r *Receipt) wellFormed() error {
	for _, f := range []string{r.Network, r.PayTo, r.Payer, r.Scheme, r.Payment, r.Amount, r.Method, r.Host, r.Path} {
		if strings.ContainsAny(f, "\r\n") {
			return errors.New("receipt: a field contains a line break")
		}
	}
	if !isHexHash(r.RequestHash) || (r.ResponseHash != "" && !isHexHash(r.ResponseHash)) {
		return errors.New("receipt: hashes must be hex SHA-256")
	}
	return nil
}

func isHexHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil && strings.ToLower(s) == s
}

// Verify checks that the receipt was signed by its payee (directly or by
// delegation). It says nothing about whether the receipt matches a
// request: see CheckReceipt.
func (r *Receipt) Verify() error {
	if r.X402Version != X402Version {
		return fmt.Errorf("receipt: unsupported x402Version %d", r.X402Version)
	}
	if err := r.wellFormed(); err != nil {
		return err
	}
	pub, err := decodePubKey(r.Signer)
	if err != nil {
		return fmt.Errorf("receipt: %w", err)
	}
	signer := addressOf(pub)
	if signer != r.PayTo {
		d := r.Delegation
		if d == nil {
			return errors.New("receipt: signed by a key that isn't the payee's, with no delegation")
		}
		if d.PayTo != r.PayTo || d.Signer != signer {
			return errors.New("receipt: the delegation is for another payee or key")
		}
		if err := d.verify(); err != nil {
			return fmt.Errorf("receipt: %w", err)
		}
		if r.At > d.Expires {
			return errors.New("receipt: signed after its delegation expired")
		}
	}
	sig, err := base64.StdEncoding.DecodeString(r.Signature)
	if err != nil || !pub.VerifySignature(ReceiptSigningMessage(*r), sig) {
		return errors.New("receipt: bad signature")
	}
	return nil
}

// ReceiptExpectation is what a buyer knows about its own purchase.
type ReceiptExpectation struct {
	Network, PayTo, Payer, Scheme, Payment, Amount, Method, Host, Path string
	RequestBody, ResponseBody                                          []byte
	Status                                                             int
	// ResponseUnknown skips the response hash (the buyer didn't keep
	// the whole body).
	ResponseUnknown bool
}

// CheckReceipt verifies r and that it describes exactly this purchase.
func CheckReceipt(r *Receipt, want ReceiptExpectation) error {
	if err := r.Verify(); err != nil {
		return err
	}
	req, resp := sha256.Sum256(want.RequestBody), sha256.Sum256(want.ResponseBody)
	for _, c := range []struct{ name, got, want string }{
		{"network", r.Network, want.Network}, {"payTo", r.PayTo, want.PayTo}, {"payer", r.Payer, want.Payer},
		{"scheme", r.Scheme, want.Scheme}, {"payment", strings.ToUpper(r.Payment), strings.ToUpper(want.Payment)},
		{"amount", r.Amount, want.Amount}, {"method", r.Method, want.Method}, {"host", r.Host, want.Host},
		{"path", r.Path, want.Path}, {"requestHash", r.RequestHash, hex.EncodeToString(req[:])},
		{"status", strconv.Itoa(r.Status), strconv.Itoa(want.Status)},
	} {
		if c.got != c.want {
			return fmt.Errorf("receipt: %s is %q, not %q", c.name, c.got, c.want)
		}
	}
	if r.ResponseHash != "" && !want.ResponseUnknown && r.ResponseHash != hex.EncodeToString(resp[:]) {
		return errors.New("receipt: responseHash doesn't match the response received")
	}
	return nil
}

// DecodeReceipt reads an X-PAYMENT-RECEIPT header value.
func DecodeReceipt(header string) (*Receipt, error) {
	var r Receipt
	if err := DecodeHeader(header, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func decodePubKey(s string) (*mldsa.PubKey, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) != mldsa.PubKeySize {
		return nil, errors.New("not a base64 ML-DSA-44 public key")
	}
	return &mldsa.PubKey{Key: b}, nil
}

func addressOf(pub *mldsa.PubKey) string { return sdk.AccAddress(pub.Address()).String() }

// ReceiptConfig signs receipts. Sign must sign with the payee's key, or
// with the key Delegation names.
type ReceiptConfig struct {
	Sign       Signer
	Delegation *ReceiptDelegation
}

// checkReceiptConfig makes sure receipts will verify, by signing a probe.
func checkReceiptConfig(c *ReceiptConfig, payTo string, now int64) error {
	empty := sha256.Sum256(nil)
	probe := Receipt{X402Version: X402Version, PayTo: payTo, RequestHash: hex.EncodeToString(empty[:]), At: now, Delegation: c.Delegation}
	sig, pub, err := c.Sign(ReceiptSigningMessage(probe))
	if err != nil {
		return err
	}
	probe.Signer, probe.Signature = base64.StdEncoding.EncodeToString(pub), base64.StdEncoding.EncodeToString(sig)
	return probe.Verify()
}

// paidRequest is what a receipt says about the request and payment.
type paidRequest struct {
	scheme, payer, payment string
	body                   []byte // nil: too big to hash
}

// readForReceipt buffers r's body (up to maxSignedBody) for its hash,
// leaving r.Body readable. It returns nil if the body is bigger.
func readForReceipt(r *http.Request) []byte {
	if r.Body == nil || r.Body == http.NoBody {
		return []byte{}
	}
	orig := r.Body
	buf, err := io.ReadAll(io.LimitReader(orig, maxSignedBody+1))
	r.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(buf), orig), orig}
	if err != nil || len(buf) > maxSignedBody {
		return nil
	}
	return buf
}

// receiptWriter holds a paid response until it's complete, so its
// receipt (a header) can cover the body. A response bigger than
// maxReceiptBody, or an event stream the handler flushes, streams
// instead, with a receipt that has no ResponseHash.
type receiptWriter struct {
	w         http.ResponseWriter
	receipt   func(status int, body []byte, hashed bool) string
	status    int
	buf       bytes.Buffer
	streaming bool
}

func (rw *receiptWriter) Header() http.Header { return rw.w.Header() }

func (rw *receiptWriter) WriteHeader(code int) {
	if rw.status == 0 {
		rw.status = code
	}
}

func (rw *receiptWriter) Write(b []byte) (int, error) {
	if rw.status == 0 {
		rw.status = http.StatusOK
	}
	if rw.streaming {
		return rw.w.Write(b)
	}
	if rw.buf.Len()+len(b) > maxReceiptBody {
		rw.stream()
		return rw.w.Write(b)
	}
	return rw.buf.Write(b)
}

func (rw *receiptWriter) Flush() {
	if rw.status == 0 {
		rw.status = http.StatusOK
	}
	// httputil.ReverseProxy flushes after every write when the upstream
	// sends no Content-Length; only a real stream needs its bytes now.
	if !rw.streaming && !strings.HasPrefix(rw.w.Header().Get("Content-Type"), "text/event-stream") {
		return
	}
	rw.stream()
	if f, ok := rw.w.(http.Flusher); ok {
		f.Flush()
	}
}

func (rw *receiptWriter) stream() {
	if rw.streaming {
		return
	}
	rw.streaming = true
	if h := rw.receipt(rw.status, nil, false); h != "" {
		rw.w.Header().Set(HeaderReceipt, h)
	}
	rw.w.WriteHeader(rw.status)
	_, _ = rw.w.Write(rw.buf.Bytes())
	rw.buf.Reset()
}

// finish sends a held response with its receipt.
func (rw *receiptWriter) finish() {
	if rw.streaming {
		return
	}
	if rw.status == 0 {
		rw.status = http.StatusOK
	}
	if h := rw.receipt(rw.status, rw.buf.Bytes(), true); h != "" {
		rw.w.Header().Set(HeaderReceipt, h)
	}
	rw.w.WriteHeader(rw.status)
	_, _ = rw.w.Write(rw.buf.Bytes())
}

// signReceipt builds the receipt header for a paid response ("" if it
// can't be signed: the response still goes out).
func (p *Paywall) signReceipt(r *http.Request, paid paidRequest, status int, body []byte, hashed bool) string {
	c := p.cfg.Receipts
	rec := Receipt{
		X402Version: X402Version, Network: p.cfg.Network, PayTo: p.cfg.PayTo, Payer: paid.payer, Scheme: paid.scheme,
		Payment: paid.payment, Amount: p.cfg.Price.String(), Method: r.Method, Host: r.Host, Path: r.URL.Path,
		Status: status, At: p.cfg.Now().Unix(), Delegation: c.Delegation,
	}
	if paid.body != nil {
		sum := sha256.Sum256(paid.body)
		rec.RequestHash = hex.EncodeToString(sum[:])
	}
	if hashed {
		sum := sha256.Sum256(body)
		rec.ResponseHash = hex.EncodeToString(sum[:])
	}
	if rec.RequestHash == "" || rec.wellFormed() != nil {
		return "" // e.g. a path with a line break: no receipt rather than an ambiguous one
	}
	sig, pub, err := c.Sign(ReceiptSigningMessage(rec))
	if err != nil {
		return ""
	}
	rec.Signer, rec.Signature = base64.StdEncoding.EncodeToString(pub), base64.StdEncoding.EncodeToString(sig)
	h, _ := EncodeHeader(rec)
	return h
}

// serveReceipted serves next, adding a receipt when receipts are on,
// and returns the response status.
func (p *Paywall) serveReceipted(w http.ResponseWriter, r *http.Request, next http.Handler, paid paidRequest) int {
	if p.cfg.Receipts == nil {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		return rec.status
	}
	rw := &receiptWriter{w: w, receipt: func(status int, body []byte, hashed bool) string {
		return p.signReceipt(r, paid, status, body, hashed)
	}}
	next.ServeHTTP(rw, r)
	rw.finish()
	return rw.status
}
