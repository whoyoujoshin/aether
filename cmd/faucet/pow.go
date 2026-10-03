package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"math/bits"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A browser asks for a /challenge, finds a nonce whose
// sha256(challenge + ":" + nonce) starts with powBits zero bits, and
// sends both with its request. That costs a page a second or two, where
// a captcha would cost a person more and fit a proof-of-work chain
// less, and it marks the drip as a person's ("web").
//
// A challenge is stateless until it's used: a random tag, the address it
// was issued for and its expiry, MACed with a key that lives only in this
// process. Used challenges are remembered until they expire, so one
// solution funds one request.

const challengeTTL = 5 * time.Minute

var (
	errPowMalformed = errors.New("malformed challenge")
	errPowForged    = errors.New("challenge wasn't issued by this faucet")
	errPowExpired   = errors.New("challenge expired; fetch a new one")
	errPowAddress   = errors.New("challenge was issued for another address")
	errPowUsed      = errors.New("challenge already used")
	errPowWork      = errors.New("nonce doesn't meet the challenge's difficulty")
)

type powIssuer struct {
	key  []byte
	bits int
	now  func() time.Time

	mu   sync.Mutex
	used map[string]time.Time // challenge -> its expiry
}

func newPowIssuer(bits int) *powIssuer {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	return &powIssuer{key: key, bits: bits, now: time.Now, used: map[string]time.Time{}}
}

func (p *powIssuer) mac(payload []byte) []byte {
	m := hmac.New(sha256.New, p.key)
	m.Write(payload)
	return m.Sum(nil)[:16]
}

// issue is a challenge for address: base64url(tag | expiry | address) "." base64url(mac).
func (p *powIssuer) issue(address string) (challenge string, expires time.Time) {
	expires = p.now().Add(challengeTTL)
	payload := make([]byte, 12+8, 12+8+len(address))
	if _, err := rand.Read(payload[:12]); err != nil {
		panic(err)
	}
	binary.BigEndian.PutUint64(payload[12:], uint64(expires.Unix()))
	payload = append(payload, address...)
	enc := base64.RawURLEncoding
	return enc.EncodeToString(payload) + "." + enc.EncodeToString(p.mac(payload)), expires
}

// powOK reports whether sha256(challenge ":" nonce) has at least bits
// leading zero bits.
func powOK(challenge, nonce string, bits int) bool {
	sum := sha256.Sum256([]byte(challenge + ":" + nonce))
	return leadingZeroBits(sum[:]) >= bits
}

func leadingZeroBits(b []byte) int {
	n := 0
	for _, x := range b {
		if x != 0 {
			return n + bits.LeadingZeros8(x)
		}
		n += 8
	}
	return n
}

// redeem checks a solved challenge for address and marks it used.
func (p *powIssuer) redeem(challenge, nonce, address string) error {
	body, sig, ok := strings.Cut(challenge, ".")
	enc := base64.RawURLEncoding
	payload, err1 := enc.DecodeString(body)
	mac, err2 := enc.DecodeString(sig)
	if !ok || err1 != nil || err2 != nil || len(payload) < 20 || len(nonce) == 0 || len(nonce) > 32 {
		return errPowMalformed
	}
	if !hmac.Equal(mac, p.mac(payload)) {
		return errPowForged
	}
	now := p.now()
	expires := time.Unix(int64(binary.BigEndian.Uint64(payload[12:20])), 0)
	if now.After(expires) {
		return errPowExpired
	}
	if string(payload[20:]) != address {
		return errPowAddress
	}
	if !powOK(challenge, nonce, p.bits) {
		return errPowWork
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for c, exp := range p.used {
		if now.After(exp) {
			delete(p.used, c)
		}
	}
	if _, seen := p.used[challenge]; seen {
		return errPowUsed
	}
	p.used[challenge] = expires
	return nil
}

// solvePow is the browser's half, for tests and Go clients.
func solvePow(challenge string, bits int) string {
	for i := 0; ; i++ {
		n := strconv.Itoa(i)
		if powOK(challenge, n, bits) {
			return n
		}
	}
}
