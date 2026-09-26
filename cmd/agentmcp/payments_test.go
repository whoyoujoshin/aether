package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
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
	payments   []wallet.IncomingPayment
	// paymentsTo, if it has the address, answers scans of it instead of payments.
	paymentsTo map[string][]wallet.IncomingPayment
	signed     map[string][]byte
	height     int64
	// autoInclude puts every accepted transaction straight into a block.
	autoInclude bool

	checkTxCode uint32 // non-zero: reject the next new broadcast
	loseReply   bool   // next broadcast reaches the mempool but errors
	accountErr  error

	grant    *wallet.SendGrant
	grantErr error
	balances map[string]sdk.Coins
}

func newFakeChain() *fakeChain {
	return &fakeChain{mempool: map[string]bool{}, blocks: map[string]*wallet.TransactionDetail{}, signed: map[string][]byte{}, height: 100}
}

func (f *fakeChain) accountInfo(string) (uint64, uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return 7, f.seq, f.accountErr
}

func (f *fakeChain) sendGrant(string, string) (*wallet.SendGrant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.grantErr != nil {
		return nil, f.grantErr
	}
	if f.grant == nil {
		return nil, wallet.ErrGrantNotFound
	}
	return f.grant, nil
}

func (f *fakeChain) balance(a string) (sdk.Coins, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.balances[a], nil
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
		return wallet.BroadcastResult{TxHash: hash, Code: code, Codespace: "sdk", RawLog: "rejected"}, nil
	}
	f.mempool[hash] = true
	f.signed[hash] = s.Bytes
	if f.autoInclude {
		f.includeLocked(hash, 0)
	}
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
	return nil, nil
}

func (f *fakeChain) incoming(address string, since int64, _ int) ([]wallet.IncomingPayment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []wallet.IncomingPayment
	payments := f.payments
	if to, ok := f.paymentsTo[address]; ok {
		payments = to
	}
	for _, p := range payments {
		if p.Height >= since {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *fakeChain) latestHeight() (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.height, nil
}

func (f *fakeChain) close() error { return nil }

func (f *fakeChain) include(hash string, code uint32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.includeLocked(hash, code)
}

// includeLocked records what the signed transaction actually does, so
// a seller verifying it sees the real memo and transfers.
func (f *fakeChain) includeLocked(hash string, code uint32) {
	delete(f.mempool, hash)
	f.height++
	d := &wallet.TransactionDetail{Hash: hash, Height: f.height, Code: code, Timestamp: time.Now().UTC().Format(time.RFC3339)}
	if bz := f.signed[hash]; bz != nil {
		tx, err := app.MakeEncodingConfig().TxConfig.TxDecoder()(bz)
		if err == nil {
			d.Memo = tx.(sdk.TxWithMemo).GetMemo()
			if code == 0 {
				for _, m := range tx.GetMsgs() {
					if send, ok := m.(*banktypes.MsgSend); ok {
						d.Transfers = append(d.Transfers, wallet.Transfer{From: send.FromAddress, To: send.ToAddress, Amount: sdk.Coins(send.Amount).String()})
					}
				}
			}
		}
	}
	f.blocks[hash] = d
	f.seq++
}

func setupAgent(t *testing.T) *fakeChain {
	t.Helper()
	dir := t.TempDir()
	keyringDir, keyringBackend, accountName = filepath.Join(dir, "keyring"), "test", "agent"
	stateFile = filepath.Join(dir, "state.json")
	chainID, perTxLimit, dailyLimit = "aether-testnet-1", 1_000_000, 5_000_000
	granter, feeGranter = "", ""
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
	v, ok := math.NewIntFromString(out.SpentLast24h.Uaeth)
	require.True(t, ok)
	return v.Int64()
}

// requireCode asserts err is a structured error with the given code.
func requireCode(t *testing.T, err error, code string) *agentError {
	t.Helper()
	require.Error(t, err)
	var ae *agentError
	require.True(t, errors.As(err, &ae), "want a structured error, got %v", err)
	require.Equal(t, code, ae.Code, ae.Message)
	return ae
}

// decode reads back what was actually signed.
func decode(t *testing.T, bz []byte) (memo string, sequence uint64) {
	t.Helper()
	tx := decodeTx(t, bz)
	sigs, err := tx.(authsigning.SigVerifiableTx).GetSignaturesV2()
	require.NoError(t, err)
	require.Len(t, sigs, 1)
	return tx.(sdk.TxWithMemo).GetMemo(), sigs[0].Sequence
}

func decodeTx(t *testing.T, bz []byte) sdk.Tx {
	t.Helper()
	tx, err := app.MakeEncodingConfig().TxConfig.TxDecoder()(bz)
	require.NoError(t, err)
	return tx
}

func TestSendAeth_RetryWithSameKeyNeverPaysTwice(t *testing.T) {
	f := setupAgent(t)

	first, err := send(t, "order-1", "250000uaeth", "invoice-42")
	require.NoError(t, err)
	require.Equal(t, statusPending, first.Status)
	require.False(t, first.Replayed)

	again, err := send(t, "order-1", "250000uaeth", "invoice-42")
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
	settled, err := send(t, "order-1", "250000uaeth", "invoice-42")
	require.NoError(t, err)
	require.Equal(t, statusConfirmed, settled.Status)
	require.Len(t, f.broadcasts, 2)
}

func TestSendAeth_LostBroadcastReplyIsSafeToRetry(t *testing.T) {
	f := setupAgent(t)
	f.loseReply = true

	_, err := send(t, "order-2", "100000uaeth", "")
	ae := requireCode(t, err, codeBroadcastUncertain)
	require.True(t, ae.Retryable)
	require.NotEmpty(t, ae.TxHash)
	require.Contains(t, ae.Message, "same idempotencyKey")

	retry, err := send(t, "order-2", "100000uaeth", "")
	require.NoError(t, err)
	require.True(t, retry.Replayed)
	require.Equal(t, statusPending, retry.Status)
	require.Equal(t, f.broadcasts[0], f.broadcasts[1])
	require.Equal(t, int64(100_000), spent(t))
}

func TestSendAeth_SameKeyForADifferentPaymentIsRefused(t *testing.T) {
	setupAgent(t)
	_, err := send(t, "order-3", "100000uaeth", "a")
	require.NoError(t, err)

	for _, tc := range []struct{ amount, memo string }{{"100001uaeth", "a"}, {"100000uaeth", "b"}} {
		_, err = send(t, "order-3", tc.amount, tc.memo)
		requireCode(t, err, codeIdempotencyConflict)
	}
	_, _, err = toolSendAeth(context.Background(), nil, sendAethInput{To: sdk.AccAddress("someone_else________").String(), Amount: "100000uaeth", Memo: "a", IdempotencyKey: "order-3"})
	requireCode(t, err, codeIdempotencyConflict)
}

func TestSendAeth_RejectionReleasesBudget(t *testing.T) {
	f := setupAgent(t)
	f.checkTxCode = 5

	out, err := send(t, "order-4", "300000uaeth", "")
	require.NoError(t, err)
	require.Equal(t, statusFailed, out.Status)
	require.Equal(t, codeInsufficientFunds, out.ErrorCode)
	require.Equal(t, int64(0), spent(t), "a rejected transaction spent nothing")
}

func TestSendAeth_RetryAfterRejectionReservesBudgetAgain(t *testing.T) {
	f := setupAgent(t)
	f.checkTxCode = 5
	out, err := send(t, "order-5", "400000uaeth", "")
	require.NoError(t, err)
	require.Equal(t, statusFailed, out.Status)
	require.Equal(t, int64(0), spent(t))

	// Same signed bytes, accepted this time (e.g. the account got funded).
	retry, err := send(t, "order-5", "400000uaeth", "")
	require.NoError(t, err)
	require.Equal(t, statusPending, retry.Status)
	require.Equal(t, int64(400_000), spent(t), "an accepted retry must count against the budget again")
}

func TestSendAeth_LimitsStillApply(t *testing.T) {
	setupAgent(t)
	_, err := send(t, "big", "1000001uaeth", "")
	requireCode(t, err, codePerTxLimit)

	for i := 0; i < 5; i++ {
		_, err = send(t, fmt.Sprintf("k%d", i), "1 AETH", "")
		require.NoError(t, err)
	}
	_, err = send(t, "k5", "1uaeth", "")
	ae := requireCode(t, err, codeDailyLimit)
	require.False(t, ae.Retryable)
	require.InDelta(t, spendWindow.Seconds(), float64(ae.RetryAfterSeconds), 60, "budget frees up when the first payment ages out")

	_, err = send(t, "", "1uaeth", "")
	requireCode(t, err, codeInvalidArgument)
}

func TestSendAeth_SecondPaymentBeforeFirstConfirmsUsesNextSequence(t *testing.T) {
	f := setupAgent(t)
	_, err := send(t, "a", "1uaeth", "")
	require.NoError(t, err)
	_, err = send(t, "b", "1uaeth", "")
	require.NoError(t, err)

	_, seqA := decode(t, f.broadcasts[0])
	_, seqB := decode(t, f.broadcasts[1])
	require.Equal(t, uint64(0), seqA)
	require.Equal(t, uint64(1), seqB, "the chain still reports sequence 0 while the first is in the mempool")
}

func TestTransactionStatus_ConfirmedAndFailed(t *testing.T) {
	f := setupAgent(t)
	ok, err := send(t, "ok", "200000uaeth", "")
	require.NoError(t, err)
	bad, err := send(t, "bad", "300000uaeth", "")
	require.NoError(t, err)
	require.Equal(t, int64(500_000), spent(t))

	_, st, err := toolGetTransactionStatus(context.Background(), nil, getTransactionStatusInput{Hash: ok.TxHash})
	require.NoError(t, err)
	require.Equal(t, statusPending, st.Status)

	f.include(ok.TxHash, 0)
	f.include(bad.TxHash, 11) // e.g. out of gas: in a block, but failed
	f.blocks[bad.TxHash].Codespace = "sdk"
	_, st, err = toolWaitForTransaction(context.Background(), nil, waitForTransactionInput{Hash: ok.TxHash, TimeoutSeconds: 1})
	require.NoError(t, err)
	require.Equal(t, statusConfirmed, st.Status)
	_, st, err = toolWaitForTransaction(context.Background(), nil, waitForTransactionInput{Hash: bad.TxHash, TimeoutSeconds: 1})
	require.NoError(t, err)
	require.Equal(t, statusFailed, st.Status)
	require.Equal(t, codeTxFailed, st.ErrorCode)
	require.Equal(t, int64(200_000), spent(t), "a transaction that failed on-chain gives its budget back")
}

func TestWaitForPayment_MatchesOnlyConfirmedExactMemoAndEnoughAmount(t *testing.T) {
	f := setupAgent(t)
	coins := func(s string) sdk.Coins { c, _ := sdk.ParseCoinsNormalized(s); return c }
	f.payments = []wallet.IncomingPayment{
		{Hash: "OLD", Height: 50, Memo: "invoice-7", Amount: coins("500000uaeth")}, // before sinceHeight
		{Hash: "WRONGMEMO", Height: 101, Memo: "invoice-9", Amount: coins("500000uaeth")},
		{Hash: "TOOSMALL", Height: 102, Memo: "invoice-7", Amount: coins("99999uaeth")},
		{Hash: "FAILED", Height: 103, Memo: "invoice-7", Amount: coins("500000uaeth"), Code: 5},
	}
	min := math.NewInt(100_000)
	found, err := findPayment(f, "addr", "invoice-7", min, 100)
	require.NoError(t, err)
	require.Nil(t, found)

	f.payments = append(f.payments, wallet.IncomingPayment{Hash: "PAID", Height: 104, Memo: "invoice-7", Amount: coins("150000uaeth"), From: "payer"})
	_, out, err := toolWaitForPayment(context.Background(), nil, waitForPaymentInput{Memo: "invoice-7", MinAmount: "0.1 AETH", SinceHeight: 100, TimeoutSeconds: 1})
	require.NoError(t, err)
	require.True(t, out.Paid)
	require.Equal(t, "PAID", out.TxHash)
	require.Equal(t, "payer", out.From)
	require.Equal(t, amountDTO{Uaeth: "150000", Aeth: "0.15"}, *out.Amount)

	_, _, err = toolWaitForPayment(context.Background(), nil, waitForPaymentInput{Memo: "invoice-7", MinAmount: "100000", TimeoutSeconds: 1})
	requireCode(t, err, codeInvalidAmount)
}

func TestState_SurvivesRestart(t *testing.T) {
	f := setupAgent(t)
	first, err := send(t, "persist", "1uaeth", "")
	require.NoError(t, err)

	// A fresh process reads everything back from the state file.
	st, err := loadState()
	require.NoError(t, err)
	rec := st.Sends["persist"]
	require.NotNil(t, rec)
	require.Equal(t, first.TxHash, rec.TxHash)
	require.NotEmpty(t, rec.TxBase64)

	again, err := send(t, "persist", "1uaeth", "")
	require.NoError(t, err)
	require.True(t, again.Replayed)
	require.Equal(t, f.broadcasts[0], f.broadcasts[1])
}
