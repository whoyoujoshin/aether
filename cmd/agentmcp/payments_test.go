package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/wallet"
)

// fakeChain models just enough of a node: a mempool that rejects
// duplicates, blocks, and incoming history.
type fakeChain struct {
	mu         sync.Mutex
	seq        uint64
	broadcasts [][]byte
	mempool    map[string]bool
	blocks     map[string]*wallet.TransactionDetail
	incoming   []wallet.Transaction

	checkTxCode uint32 // non-zero: reject the next new broadcast
	loseReply   bool   // next broadcast reaches the mempool but errors
}

func newFakeChain() *fakeChain {
	return &fakeChain{mempool: map[string]bool{}, blocks: map[string]*wallet.TransactionDetail{}}
}

func (f *fakeChain) accountInfo(string) (uint64, uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return 7, f.seq, nil
}

func (f *fakeChain) broadcast(s wallet.SignedTx) (wallet.BroadcastResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.broadcasts = append(f.broadcasts, s.Bytes)
	hash := wallet.TxHash(s)
	switch {
	case f.blocks[hash] != nil:
		return wallet.BroadcastResult{TxHash: hash, Code: codeWrongSequence}, nil
	case f.mempool[hash]:
		return wallet.BroadcastResult{TxHash: hash, Code: codeTxInMempool}, nil
	case f.checkTxCode != 0:
		code := f.checkTxCode
		f.checkTxCode = 0
		return wallet.BroadcastResult{TxHash: hash, Code: code, RawLog: "rejected"}, nil
	}
	f.mempool[hash] = true
	if f.loseReply {
		f.loseReply = false
		return wallet.BroadcastResult{}, errors.New("context deadline exceeded")
	}
	return wallet.BroadcastResult{TxHash: hash}, nil
}

func (f *fakeChain) lookup(hash string) (*wallet.TransactionDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d := f.blocks[hash]; d != nil {
		return d, nil
	}
	return nil, fmt.Errorf("%w: %s", wallet.ErrTransactionNotFound, hash)
}

func (f *fakeChain) history(string, uint64) ([]wallet.Transaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.incoming, nil
}

func (f *fakeChain) close() error { return nil }

func (f *fakeChain) include(hash string, code uint32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.mempool, hash)
	f.blocks[hash] = &wallet.TransactionDetail{Hash: hash, Height: 42, Code: code}
	f.seq++
}

func setupAgent(t *testing.T) *fakeChain {
	t.Helper()
	dir := t.TempDir()
	keyringDir, keyringBackend, accountName = filepath.Join(dir, "keyring"), "test", "agent"
	stateFile = filepath.Join(dir, "state.json")
	chainID, perTxLimit, dailyLimit = "aether-testnet-1", 1_000_000, 5_000_000
	f := newFakeChain()
	orig := dialChain
	dialChain = func() (chain, error) { return f, nil }
	t.Cleanup(func() { dialChain = orig })
	return f
}

// A func, not a package var: vars are initialized before init() sets
// the aether bech32 prefix.
func recipient() string { return sdk.AccAddress("payment_recipient___").String() }

func send(t *testing.T, key, amount, memo string) (sendAethOutput, error) {
	t.Helper()
	_, out, err := toolSendAeth(context.Background(), nil, sendAethInput{To: recipient(), Amount: amount, Memo: memo, IdempotencyKey: key})
	return out, err
}

func spent(t *testing.T) int64 {
	t.Helper()
	_, out, err := toolGetSpendingStatus(context.Background(), nil, getSpendingStatusInput{})
	require.NoError(t, err)
	return out.SpentLast24hUaeth
}

// decode reads back what was actually signed.
func decode(t *testing.T, bz []byte) (memo string, sequence uint64) {
	t.Helper()
	tx, err := app.MakeEncodingConfig().TxConfig.TxDecoder()(bz)
	require.NoError(t, err)
	sigs, err := tx.(authsigning.SigVerifiableTx).GetSignaturesV2()
	require.NoError(t, err)
	require.Len(t, sigs, 1)
	return tx.(sdk.TxWithMemo).GetMemo(), sigs[0].Sequence
}

func TestSendAeth_RetryWithSameKeyNeverPaysTwice(t *testing.T) {
	f := setupAgent(t)

	first, err := send(t, "order-1", "250000", "invoice-42")
	require.NoError(t, err)
	require.Equal(t, statusPending, first.Status)
	require.False(t, first.Replayed)

	again, err := send(t, "order-1", "250000", "invoice-42")
	require.NoError(t, err)
	require.True(t, again.Replayed)
	require.Equal(t, first.TxHash, again.TxHash)
	require.Equal(t, statusPending, again.Status)

	require.Len(t, f.broadcasts, 2)
	require.Equal(t, f.broadcasts[0], f.broadcasts[1], "a retry must re-send the identical signed bytes, never a new transaction")
	require.Equal(t, int64(250_000), spent(t), "budget counted once")

	memo, _ := decode(t, f.broadcasts[0])
	require.Equal(t, "invoice-42", memo, "the memo must be in the signed transaction")

	// Once it's in a block, a retry just reports it -- nothing re-sent.
	f.include(first.TxHash, 0)
	settled, err := send(t, "order-1", "250000", "invoice-42")
	require.NoError(t, err)
	require.Equal(t, statusConfirmed, settled.Status)
	require.Len(t, f.broadcasts, 2)
}

func TestSendAeth_LostBroadcastReplyIsSafeToRetry(t *testing.T) {
	f := setupAgent(t)
	f.loseReply = true

	_, err := send(t, "order-2", "100000", "")
	require.ErrorContains(t, err, "same idempotencyKey")

	retry, err := send(t, "order-2", "100000", "")
	require.NoError(t, err)
	require.True(t, retry.Replayed)
	require.Equal(t, statusPending, retry.Status)
	require.Equal(t, f.broadcasts[0], f.broadcasts[1])
	require.Equal(t, int64(100_000), spent(t))
}

func TestSendAeth_SameKeyForADifferentPaymentIsRefused(t *testing.T) {
	setupAgent(t)
	_, err := send(t, "order-3", "100000", "a")
	require.NoError(t, err)

	for _, tc := range []struct{ amount, memo string }{{"100001", "a"}, {"100000", "b"}} {
		_, err = send(t, "order-3", tc.amount, tc.memo)
		require.ErrorContains(t, err, "already used for a different payment")
	}
	_, _, err = toolSendAeth(context.Background(), nil, sendAethInput{To: sdk.AccAddress("someone_else________").String(), Amount: "100000", Memo: "a", IdempotencyKey: "order-3"})
	require.ErrorContains(t, err, "already used for a different payment")
}

func TestSendAeth_RejectionReleasesBudget(t *testing.T) {
	f := setupAgent(t)
	f.checkTxCode = 5

	out, err := send(t, "order-4", "300000", "")
	require.NoError(t, err)
	require.Equal(t, statusFailed, out.Status)
	require.Equal(t, int64(0), spent(t), "a rejected transaction spent nothing")
}

func TestSendAeth_RetryAfterRejectionReservesBudgetAgain(t *testing.T) {
	f := setupAgent(t)
	f.checkTxCode = 5
	out, err := send(t, "order-5", "400000", "")
	require.NoError(t, err)
	require.Equal(t, statusFailed, out.Status)
	require.Equal(t, int64(0), spent(t))

	// Same signed bytes, accepted this time (e.g. the account got funded).
	retry, err := send(t, "order-5", "400000", "")
	require.NoError(t, err)
	require.Equal(t, statusPending, retry.Status)
	require.Equal(t, int64(400_000), spent(t), "an accepted retry must count against the budget again")
}

func TestSendAeth_LimitsStillApply(t *testing.T) {
	setupAgent(t)
	_, err := send(t, "big", "1000001", "")
	require.ErrorContains(t, err, "per-transaction limit")

	for i := 0; i < 5; i++ {
		_, err = send(t, fmt.Sprintf("k%d", i), "1000000", "")
		require.NoError(t, err)
	}
	_, err = send(t, "k5", "1", "")
	require.ErrorContains(t, err, "rolling 24h limit")

	_, err = send(t, "", "1", "")
	require.ErrorContains(t, err, "idempotencyKey is required")
}

func TestSendAeth_SecondPaymentBeforeFirstConfirmsUsesNextSequence(t *testing.T) {
	f := setupAgent(t)
	_, err := send(t, "a", "1", "")
	require.NoError(t, err)
	_, err = send(t, "b", "1", "")
	require.NoError(t, err)

	_, seqA := decode(t, f.broadcasts[0])
	_, seqB := decode(t, f.broadcasts[1])
	require.Equal(t, uint64(0), seqA)
	require.Equal(t, uint64(1), seqB, "the chain still reports sequence 0 while the first is in the mempool")
}

func TestTransactionStatus_ConfirmedAndFailed(t *testing.T) {
	f := setupAgent(t)
	ok, err := send(t, "ok", "200000", "")
	require.NoError(t, err)
	bad, err := send(t, "bad", "300000", "")
	require.NoError(t, err)
	require.Equal(t, int64(500_000), spent(t))

	_, st, err := toolGetTransactionStatus(context.Background(), nil, getTransactionStatusInput{Hash: ok.TxHash})
	require.NoError(t, err)
	require.Equal(t, statusPending, st.Status)

	f.include(ok.TxHash, 0)
	f.include(bad.TxHash, 11) // e.g. out of gas: in a block, but failed
	_, st, err = toolWaitForTransaction(context.Background(), nil, waitForTransactionInput{Hash: ok.TxHash, TimeoutSeconds: 1})
	require.NoError(t, err)
	require.Equal(t, statusConfirmed, st.Status)
	_, st, err = toolWaitForTransaction(context.Background(), nil, waitForTransactionInput{Hash: bad.TxHash, TimeoutSeconds: 1})
	require.NoError(t, err)
	require.Equal(t, statusFailed, st.Status)
	require.Equal(t, int64(200_000), spent(t), "a transaction that failed on-chain gives its budget back")
}

func TestWaitForPayment_MatchesOnlyConfirmedExactMemoAndEnoughAmount(t *testing.T) {
	f := setupAgent(t)
	f.incoming = []wallet.Transaction{
		{Hash: "WRONGMEMO", Direction: "received", Memo: "invoice-9", Amount: "500000uaeth"},
		{Hash: "TOOSMALL", Direction: "received", Memo: "invoice-7", Amount: "99999uaeth"},
		{Hash: "FAILED", Direction: "received", Memo: "invoice-7", Amount: "500000uaeth", Code: 5},
		{Hash: "OUTGOING", Direction: "sent", Memo: "invoice-7", Amount: "500000uaeth"},
	}
	min := math.NewInt(100_000)
	found, err := findPayment(f, "addr", "invoice-7", min)
	require.NoError(t, err)
	require.Nil(t, found)

	f.incoming = append(f.incoming, wallet.Transaction{Hash: "PAID", Direction: "received", Memo: "invoice-7", Amount: "150000uaeth", Height: 9})
	_, out, err := toolWaitForPayment(context.Background(), nil, waitForPaymentInput{Memo: "invoice-7", MinAmount: "100000", TimeoutSeconds: 1})
	require.NoError(t, err)
	require.True(t, out.Paid)
	require.Equal(t, "PAID", out.TxHash)
	require.Equal(t, "150000", out.Amount)
}

func TestState_SurvivesRestart(t *testing.T) {
	f := setupAgent(t)
	first, err := send(t, "persist", "1", "")
	require.NoError(t, err)

	// A fresh process reads everything back from the state file.
	st, err := loadState()
	require.NoError(t, err)
	rec := st.Sends["persist"]
	require.NotNil(t, rec)
	require.Equal(t, first.TxHash, rec.TxHash)
	require.NotEmpty(t, rec.TxBase64)

	again, err := send(t, "persist", "1", "")
	require.NoError(t, err)
	require.True(t, again.Replayed)
	require.Equal(t, f.broadcasts[0], f.broadcasts[1])
}
