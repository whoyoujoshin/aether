package wallet

import (
	"bytes"
	"context"
	"sync"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
)

// AccountKeys reads the ML-DSA-44 key stored on an account: nil if the
// account doesn't exist or has signed nothing yet. *Client is one.
type AccountKeys interface {
	AccountPubKey(ctx context.Context, address string) ([]byte, error)
}

// SignsFor is the chain's rule for which key signs for an account
// (docs/PQ-KEY-ROTATION.md), for every check made off chain: the key
// stored on the account, or, while none is stored, the key the address
// hashes from. After a key rotation that is the new key, not the retired
// one, whose hash is still the address.
//
// keys nil checks only the hash, as before rotation existed: right for
// an account that never rotated, wrong for one that did, so verifiers
// connected to a node pass one.
func SignsFor(ctx context.Context, keys AccountKeys, account string, pub []byte) (bool, error) {
	if len(pub) != mldsa.PubKeySize {
		return false, nil
	}
	if keys != nil {
		stored, err := keys.AccountPubKey(ctx, account)
		if err != nil {
			return false, err
		}
		if len(stored) > 0 {
			return bytes.Equal(stored, pub), nil
		}
	}
	return sdk.AccAddress((&mldsa.PubKey{Key: pub}).Address()).String() == account, nil
}

// DefaultAccountKeyTTL is how long CachedAccountKeys trusts an answer: a
// verifier may accept a retired key for up to this long after a rotation
// applies.
const DefaultAccountKeyTTL = 30 * time.Second

// CachedAccountKeys remembers each account's key for TTL, so a verifier
// on a hot path (a paywall's signed requests) doesn't ask the node every
// time. Errors aren't cached.
type CachedAccountKeys struct {
	Keys AccountKeys
	TTL  time.Duration
	Now  func() time.Time // tests

	mu      sync.Mutex
	entries map[string]cachedKey
}

type cachedKey struct {
	key []byte
	at  time.Time
}

// maxCachedAccounts bounds the cache; past it, it starts over.
const maxCachedAccounts = 10_000

func NewCachedAccountKeys(keys AccountKeys, ttl time.Duration) *CachedAccountKeys {
	if ttl <= 0 {
		ttl = DefaultAccountKeyTTL
	}
	return &CachedAccountKeys{Keys: keys, TTL: ttl}
}

func (c *CachedAccountKeys) AccountPubKey(ctx context.Context, address string) ([]byte, error) {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	c.mu.Lock()
	if e, ok := c.entries[address]; ok && now().Sub(e.at) < c.TTL {
		c.mu.Unlock()
		return e.key, nil
	}
	c.mu.Unlock()

	key, err := c.Keys.AccountPubKey(ctx, address)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.entries == nil || len(c.entries) >= maxCachedAccounts {
		c.entries = map[string]cachedKey{}
	}
	c.entries[address] = cachedKey{key: key, at: now()}
	c.mu.Unlock()
	return key, nil
}

// DialAccountKeys reads keys from the node at Endpoint, dialling for each
// lookup: for a process that keeps no client open. Wrap it in
// CachedAccountKeys.
type DialAccountKeys struct{ Endpoint string }

func (d DialAccountKeys) AccountPubKey(ctx context.Context, address string) ([]byte, error) {
	c, err := NewClient(d.Endpoint)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	return c.AccountPubKey(ctx, address)
}
