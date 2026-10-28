package main

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeAccountKeys is the keys stored on accounts, for wallet.SignsFor. An
// account it doesn't list has none, so its own key signs for it.
type fakeAccountKeys struct {
	mu   sync.Mutex
	keys map[string][]byte
}

func (f *fakeAccountKeys) AccountPubKey(_ context.Context, address string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.keys[address], nil
}

var testAccountKeys = &fakeAccountKeys{keys: map[string][]byte{}}

// storeKey puts key on account for the rest of the test, as a rotation
// would.
func storeKey(t *testing.T, account string, key []byte) {
	t.Helper()
	testAccountKeys.mu.Lock()
	testAccountKeys.keys[account] = key
	testAccountKeys.mu.Unlock()
	t.Cleanup(func() {
		testAccountKeys.mu.Lock()
		delete(testAccountKeys.keys, account)
		testAccountKeys.mu.Unlock()
	})
}

func TestMain(m *testing.M) {
	accountKeys = testAccountKeys
	os.Exit(m.Run())
}

// After the owner rotates its key, a decision signed by its retired key
// is ignored, and one signed by its new key counts.
func TestApprovals_FollowTheOwnersKeyRotation(t *testing.T) {
	f := setupAgent(t)
	o := setupApprovals(t, 100_000)
	next, _, err := o.w.CreateAccount("owner-next")
	require.NoError(t, err)
	storeKey(t, o.address, next.PubKey)

	out, err := send(t, "rot", "0.2 AETH", "")
	require.NoError(t, err)
	o.decide(t, "approve", out.ApprovalID)
	again, err := send(t, "rot", "0.2 AETH", "")
	require.NoError(t, err)
	require.Equal(t, statusPendingApproval, again.Status, "the retired key approved nothing")
	require.Empty(t, f.broadcasts)

	rotated := owner{dir: o.dir, key: "owner-next", address: o.address, w: o.w}
	rotated.decide(t, "approve", out.ApprovalID)
	sent, err := send(t, "rot", "0.2 AETH", "")
	require.NoError(t, err)
	require.Equal(t, statusPending, sent.Status)
	require.Len(t, f.broadcasts, 1)
}
