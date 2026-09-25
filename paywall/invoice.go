package paywall

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
	"time"
)

// Invoices are self-authenticating (HMAC), so issuing one to an
// unauthenticated client stores nothing: only paid invoices take
// memory, and each of those cost someone a payment.

const (
	invoicePrefix  = "x402-"
	invoiceVersion = 1
	nonceLen       = 12
	resourceLen    = 16
	macLen         = 16
	payloadLen     = 1 + 8 + 8 + nonceLen + resourceLen
)

type invoice struct {
	expiry   time.Time
	price    uint64 // uaeth
	resource [resourceLen]byte
}

func resourceKey(method, path string) [resourceLen]byte {
	sum := sha256.Sum256([]byte(method + " " + path))
	var k [resourceLen]byte
	copy(k[:], sum[:])
	return k
}

func (p *Paywall) newInvoice(inv invoice) (string, error) {
	b := make([]byte, payloadLen, payloadLen+macLen)
	b[0] = invoiceVersion
	binary.BigEndian.PutUint64(b[1:], uint64(inv.expiry.Unix()))
	binary.BigEndian.PutUint64(b[9:], inv.price)
	if _, err := rand.Read(b[17 : 17+nonceLen]); err != nil {
		return "", err
	}
	copy(b[17+nonceLen:], inv.resource[:])
	b = append(b, p.mac(b)...)
	return invoicePrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

var errBadInvoice = errors.New("invoice was not issued by this server")

func (p *Paywall) parseInvoice(s string) (invoice, error) {
	raw, ok := strings.CutPrefix(s, invoicePrefix)
	if !ok {
		return invoice{}, errBadInvoice
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(b) != payloadLen+macLen || b[0] != invoiceVersion {
		return invoice{}, errBadInvoice
	}
	if !hmac.Equal(b[payloadLen:], p.mac(b[:payloadLen])) {
		return invoice{}, errBadInvoice
	}
	inv := invoice{
		expiry: time.Unix(int64(binary.BigEndian.Uint64(b[1:])), 0),
		price:  binary.BigEndian.Uint64(b[9:]),
	}
	copy(inv.resource[:], b[17+nonceLen:])
	return inv, nil
}

func (p *Paywall) mac(payload []byte) []byte {
	m := hmac.New(sha256.New, p.secret)
	m.Write(payload)
	return m.Sum(nil)[:macLen]
}
