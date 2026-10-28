package wallet_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/wallet"
)

type keyMap struct {
	keys  map[string][]byte
	err   error
	calls int
}

func (k *keyMap) AccountPubKey(_ context.Context, a string) ([]byte, error) {
	k.calls++
	return k.keys[a], k.err
}

func TestSignsFor(t *testing.T) {
	w := newTestWallet(t)
	own, _, err := w.CreateAccount("own")
	require.NoError(t, err)
	next, _, err := w.CreateAccount("next")
	require.NoError(t, err)
	ctx := context.Background()
	yes := func(keys wallet.AccountKeys, pub []byte) bool {
		ok, err := wallet.SignsFor(ctx, keys, own.Address, pub)
		require.NoError(t, err)
		return ok
	}

	// Never rotated (no key stored, or its own): its own key only.
	for _, keys := range []wallet.AccountKeys{nil, &keyMap{}, &keyMap{keys: map[string][]byte{own.Address: own.PubKey}}} {
		require.True(t, yes(keys, own.PubKey))
		require.False(t, yes(keys, next.PubKey))
	}
	// Rotated: the stored key, and not the retired one.
	rotated := &keyMap{keys: map[string][]byte{own.Address: next.PubKey}}
	require.True(t, yes(rotated, next.PubKey))
	require.False(t, yes(rotated, own.PubKey))
	require.True(t, yes(nil, own.PubKey), "without the chain, only the hash: the reason to pass keys")

	require.False(t, yes(rotated, next.PubKey[:10]), "not an ML-DSA-44 key")
	_, err = wallet.SignsFor(ctx, &keyMap{err: errors.New("down")}, own.Address, own.PubKey)
	require.Error(t, err, "can't tell: an error, never a yes")
}

func TestCachedAccountKeys(t *testing.T) {
	src := &keyMap{keys: map[string][]byte{"a": []byte("k1")}}
	now := time.Unix(1000, 0)
	c := wallet.NewCachedAccountKeys(src, 30*time.Second)
	c.Now = func() time.Time { return now }
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		k, err := c.AccountPubKey(ctx, "a")
		require.NoError(t, err)
		require.Equal(t, []byte("k1"), k)
	}
	require.Equal(t, 1, src.calls)

	src.keys["a"] = []byte("k2") // rotated
	now = now.Add(29 * time.Second)
	k, _ := c.AccountPubKey(ctx, "a")
	require.Equal(t, []byte("k1"), k, "within the TTL")
	now = now.Add(2 * time.Second)
	k, _ = c.AccountPubKey(ctx, "a")
	require.Equal(t, []byte("k2"), k, "past it")

	src.err = errors.New("down")
	now = now.Add(time.Minute)
	_, err := c.AccountPubKey(ctx, "a")
	require.Error(t, err)
	src.err = nil
	k, err = c.AccountPubKey(ctx, "a")
	require.NoError(t, err)
	require.Equal(t, []byte("k2"), k, "errors aren't cached")
}
