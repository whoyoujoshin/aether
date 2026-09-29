package main

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/wallet"
	"github.com/whoyoujoshin/aether/x/escrow"
)

// escrowFake is fakeChain plus an x/escrow that applies the escrow
// messages it's sent, the way the chain would.
type escrowFake struct {
	*fakeChain
	mu        sync.Mutex
	nextID    uint64
	open      map[uint64]escrow.Escrow
	createdBy map[string]uint64
	outcomes  map[uint64]*wallet.EscrowOutcome
	notActive bool
}

func setupEscrow(t *testing.T) (*escrowFake, string) {
	t.Helper()
	f := setupAgent(t)
	f.autoInclude = true
	e := &escrowFake{fakeChain: f, nextID: 1, open: map[uint64]escrow.Escrow{}, createdBy: map[string]uint64{}, outcomes: map[uint64]*wallet.EscrowOutcome{}}
	dialChain = func() (chain, error) { return e, nil }
	w, err := newWallet()
	require.NoError(t, err)
	acc, err := getOrCreateAgentAccount(w)
	require.NoError(t, err)
	return e, acc.Address
}

func (e *escrowFake) broadcast(s wallet.SignedTx) (wallet.BroadcastResult, error) {
	res, err := e.fakeChain.broadcast(s)
	if err != nil || res.Code != 0 || !e.fakeChain.autoInclude {
		return res, err
	}
	tx, derr := app.MakeEncodingConfig().TxConfig.TxDecoder()(s.Bytes)
	if derr != nil {
		return res, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	fail := func(code uint32, log string) {
		e.fakeChain.mu.Lock()
		d := e.fakeChain.blocks[res.TxHash]
		d.Code, d.Codespace, d.RawLog = code, "escrow", log
		e.fakeChain.mu.Unlock()
	}
	for _, m := range tx.GetMsgs() {
		switch m := m.(type) {
		case *escrow.MsgCreateEscrow:
			id := e.nextID
			e.nextID++
			e.open[id] = escrow.Escrow{Id: id, Payer: m.Payer, Payee: m.Payee, Arbiter: m.Arbiter, Amount: m.Amount, ExpiresAt: m.ExpiresAt, OnExpiry: m.OnExpiry, Terms: m.Terms}
			e.createdBy[res.TxHash] = id
		case *escrow.MsgReleaseEscrow:
			e.settle(m.Sender, m.Id, true, res.TxHash, fail)
		case *escrow.MsgRefundEscrow:
			e.settle(m.Sender, m.Id, false, res.TxHash, fail)
		}
	}
	return res, err
}

func (e *escrowFake) settle(sender string, id uint64, release bool, hash string, fail func(uint32, string)) {
	es, ok := e.open[id]
	if !ok {
		fail(4, "escrow not found")
		return
	}
	if (release && sender != es.Payer && sender != es.Arbiter) || (!release && sender != es.Payee && sender != es.Arbiter) {
		fail(5, "not allowed")
		return
	}
	delete(e.open, id)
	e.outcomes[id] = &wallet.EscrowOutcome{ID: id, Released: release, By: sender, TxHash: hash, Payer: es.Payer, Payee: es.Payee, Amount: es.Amount.String()}
}

func (e *escrowFake) expire(id uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	es := e.open[id]
	delete(e.open, id)
	e.outcomes[id] = &wallet.EscrowOutcome{ID: id, Released: es.OnExpiry == escrow.ON_EXPIRY_RELEASE, By: escrow.ByExpiry, Payer: es.Payer, Payee: es.Payee, Amount: es.Amount.String()}
}

func (e *escrowFake) escrow(id uint64) (*escrow.Escrow, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.notActive {
		return nil, wallet.ErrEscrowNotActive
	}
	if es, ok := e.open[id]; ok {
		return &es, nil
	}
	return nil, wallet.ErrEscrowNotFound
}

func (e *escrowFake) escrowsOf(a string) ([]escrow.Escrow, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []escrow.Escrow
	for id := uint64(1); id < e.nextID+10; id++ {
		if es, ok := e.open[id]; ok && (es.Payer == a || es.Payee == a || es.Arbiter == a) {
			out = append(out, es)
		}
	}
	return out, nil
}

func (e *escrowFake) escrowCreatedID(h string) (uint64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id, ok := e.createdBy[h]; ok {
		return id, nil
	}
	return 0, fmt.Errorf("%w: %s", wallet.ErrTransactionNotFound, h)
}

func (e *escrowFake) escrowOutcome(id uint64) (*wallet.EscrowOutcome, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if o, ok := e.outcomes[id]; ok {
		return o, nil
	}
	return nil, wallet.ErrEscrowNotFound
}

func worker() string  { return sdk.AccAddress("escrow_worker_agent_").String() }
func arbiter() string { return sdk.AccAddress("escrow_arbiter______").String() }

func createEscrow(t *testing.T, key, amount string, mut ...func(*createEscrowInput)) (escrowTxOutput, error) {
	t.Helper()
	in := createEscrowInput{Payee: worker(), Amount: amount, ExpiresIn: "72h", OnExpiry: "refund", Arbiter: arbiter(), Terms: "job #1", IdempotencyKey: key, WaitSeconds: 1}
	for _, m := range mut {
		m(&in)
	}
	_, out, err := toolCreateEscrow(context.Background(), nil, in)
	return out, err
}

func TestCreateEscrow_LocksAndCountsAgainstTheBudget(t *testing.T) {
	e, agent := setupEscrow(t)
	out, err := createEscrow(t, "job-1", "0.5 AETH")
	require.NoError(t, err)
	require.Equal(t, statusConfirmed, out.Status)
	require.Equal(t, uint64(1), out.EscrowID)
	require.Equal(t, "500000", out.Amount.Uaeth)
	require.NotNil(t, out.Escrow)
	require.Equal(t, "payer", out.Escrow.YourRole)
	require.Equal(t, []string{"release"}, out.Escrow.YouMay)
	require.Equal(t, "refund", out.Escrow.OnExpiry)
	require.InDelta(t, 72*3600, out.Escrow.SecondsLeft, 5)
	require.Equal(t, agent, e.open[1].Payer)
	require.EqualValues(t, 500_000, spent(t), "locking money is spending")

	// A retry with the same key re-reports; it doesn't lock twice.
	before := len(e.broadcasts)
	again, err := createEscrow(t, "job-1", "0.5 AETH")
	require.NoError(t, err)
	require.True(t, again.Replayed)
	require.Equal(t, out.TxHash, again.TxHash)
	require.Equal(t, uint64(1), again.EscrowID)
	require.Len(t, e.broadcasts, before, "nothing re-sent for a confirmed escrow")
	require.EqualValues(t, 500_000, spent(t))

	_, err = createEscrow(t, "job-1", "0.6 AETH")
	requireCode(t, err, codeIdempotencyConflict)
	_, err = createEscrow(t, "job-1", "0.5 AETH", func(in *createEscrowInput) { in.OnExpiry = "release" })
	requireCode(t, err, codeIdempotencyConflict)
}

func TestCreateEscrow_Limits(t *testing.T) {
	setupEscrow(t)
	_, err := createEscrow(t, "big", "1.5 AETH")
	requireCode(t, err, codePerTxLimit)
	for i := 0; i < 5; i++ {
		_, err := createEscrow(t, fmt.Sprintf("job-%d", i), "1 AETH")
		require.NoError(t, err)
	}
	_, err = createEscrow(t, "one-more", "0.1 AETH")
	requireCode(t, err, codeDailyLimit)
}

func TestCreateEscrow_Refusals(t *testing.T) {
	e, _ := setupEscrow(t)
	for _, tc := range []struct {
		mut  func(*createEscrowInput)
		code string
	}{
		{func(in *createEscrowInput) { in.OnExpiry = "maybe" }, codeInvalidArgument},
		{func(in *createEscrowInput) { in.ExpiresIn = "30s" }, codeInvalidArgument},
		{func(in *createEscrowInput) { in.ExpiresIn = "9000h" }, codeInvalidArgument},
		{func(in *createEscrowInput) { in.Payee = "nope" }, codeInvalidAddress},
		{func(in *createEscrowInput) { in.Arbiter = "nope" }, codeInvalidAddress},
		{func(in *createEscrowInput) { in.IdempotencyKey = "" }, codeInvalidArgument},
	} {
		_, err := createEscrow(t, "k", "0.1 AETH", tc.mut)
		requireCode(t, err, tc.code)
	}
	_, err := createEscrow(t, "k", "100000")
	requireCode(t, err, codeInvalidAmount)

	e.notActive = true
	_, err = createEscrow(t, "k", "0.1 AETH")
	requireCode(t, err, codeEscrowNotActive)
	e.notActive = false

	granter = sdk.AccAddress("some_granter________").String()
	_, err = createEscrow(t, "k", "0.1 AETH")
	requireCode(t, err, codeEscrowGrantMode)
	granter = ""
	require.EqualValues(t, 0, spent(t), "nothing refused was counted")
}

func TestCreateEscrow_PendingThenFollowedUp(t *testing.T) {
	e, _ := setupEscrow(t)
	e.fakeChain.autoInclude = false
	out, err := createEscrow(t, "slow", "0.2 AETH")
	require.NoError(t, err)
	require.Equal(t, statusPending, out.Status)
	require.Zero(t, out.EscrowID)

	// It lands; the escrow fake applies it when it's re-sent and included.
	e.fakeChain.autoInclude = true
	e.fakeChain.mu.Lock()
	delete(e.fakeChain.mempool, out.TxHash)
	e.fakeChain.mu.Unlock()
	again, err := createEscrow(t, "slow", "0.2 AETH")
	require.NoError(t, err)
	require.Equal(t, statusConfirmed, again.Status)
	require.Equal(t, out.TxHash, again.TxHash, "the same transaction, re-sent")
	require.Equal(t, uint64(1), again.EscrowID)
	require.EqualValues(t, 200_000, spent(t))
}

func TestCreateEscrow_NeedsOwnerApprovalAboveThreshold(t *testing.T) {
	setupEscrow(t)
	setupApprovals(t, 100_000)
	out, err := createEscrow(t, "needs-ok", "0.5 AETH")
	require.NoError(t, err)
	require.Equal(t, statusPendingApproval, out.Status)
	require.NotEmpty(t, out.ApprovalID)
	require.EqualValues(t, 0, spent(t))
}

func TestReleaseAndRefund(t *testing.T) {
	e, agent := setupEscrow(t)
	out, err := createEscrow(t, "job", "0.5 AETH")
	require.NoError(t, err)

	// The payer can't refund to itself; only the payee or arbiter can.
	_, _, err = toolRefundEscrow(context.Background(), nil, settleEscrowInput{ID: out.EscrowID, WaitSeconds: 1})
	requireCode(t, err, codeEscrowNotAllowed)

	_, rel, err := toolReleaseEscrow(context.Background(), nil, settleEscrowInput{ID: out.EscrowID, WaitSeconds: 1})
	require.NoError(t, err)
	require.Equal(t, statusReleased, rel.Status)
	require.Equal(t, agent, rel.SettledBy)
	require.Equal(t, "500000", rel.Amount.Uaeth)

	// Asking again reports the settlement; it sends nothing new.
	before := len(e.broadcasts)
	_, again, err := toolReleaseEscrow(context.Background(), nil, settleEscrowInput{ID: out.EscrowID, WaitSeconds: 1})
	require.NoError(t, err)
	require.Equal(t, statusReleased, again.Status)
	require.Equal(t, rel.TxHash, again.TxHash)
	require.Len(t, e.broadcasts, before)

	_, got, err := toolGetEscrow(context.Background(), nil, getEscrowInput{ID: out.EscrowID})
	require.NoError(t, err)
	require.Equal(t, statusReleased, got.Status)
	require.Equal(t, agent, got.SettledBy)

	// As the payee of someone else's escrow: it may refund, not release.
	e.open[9] = escrow.Escrow{Id: 9, Payer: recipient(), Payee: agent, Amount: sdk.NewCoins(sdk.NewInt64Coin("uaeth", 300_000)), ExpiresAt: time.Now().Add(time.Hour).Unix(), OnExpiry: escrow.ON_EXPIRY_RELEASE}
	_, _, err = toolReleaseEscrow(context.Background(), nil, settleEscrowInput{ID: 9, WaitSeconds: 1})
	requireCode(t, err, codeEscrowNotAllowed)
	_, list, err := toolListEscrows(context.Background(), nil, listEscrowsInput{})
	require.NoError(t, err)
	require.Len(t, list.Escrows, 1)
	require.Equal(t, "payee", list.Escrows[0].YourRole)
	require.Equal(t, []string{"refund"}, list.Escrows[0].YouMay)
	_, ref, err := toolRefundEscrow(context.Background(), nil, settleEscrowInput{ID: 9, WaitSeconds: 1})
	require.NoError(t, err)
	require.Equal(t, statusRefunded, ref.Status)

	_, _, err = toolReleaseEscrow(context.Background(), nil, settleEscrowInput{ID: 42, WaitSeconds: 1})
	requireCode(t, err, codeEscrowNotFound)
}

func TestGetEscrow(t *testing.T) {
	e, _ := setupEscrow(t)
	out, err := createEscrow(t, "job", "0.5 AETH", func(in *createEscrowInput) { in.OnExpiry = "release" })
	require.NoError(t, err)

	_, got, err := toolGetEscrow(context.Background(), nil, getEscrowInput{TxHash: out.TxHash})
	require.NoError(t, err)
	require.Equal(t, statusOpen, got.Status)
	require.Equal(t, out.EscrowID, got.EscrowID)
	require.Equal(t, "job #1", got.Escrow.Terms)
	require.Equal(t, arbiter(), got.Escrow.Arbiter)

	e.expire(out.EscrowID)
	_, got, err = toolGetEscrow(context.Background(), nil, getEscrowInput{ID: out.EscrowID})
	require.NoError(t, err)
	require.Equal(t, statusReleased, got.Status)
	require.Equal(t, escrow.ByExpiry, got.SettledBy)
	require.Empty(t, got.SettleTxHash, "the deadline settles it outside any transaction")

	_, got, err = toolGetEscrow(context.Background(), nil, getEscrowInput{TxHash: "NOT-IN-A-BLOCK"})
	require.NoError(t, err)
	require.Equal(t, statusPending, got.Status)

	_, _, err = toolGetEscrow(context.Background(), nil, getEscrowInput{})
	requireCode(t, err, codeInvalidArgument)
}

func TestEscrowChainErrorCodes(t *testing.T) {
	require.Equal(t, codeEscrowTooManyOpen, chainErrorCode("escrow", 3, "", codeTxFailed))
	require.Equal(t, codeEscrowNotAllowed, chainErrorCode("escrow", 5, "", codeTxFailed))
	require.Equal(t, codeEscrowInvalid, chainErrorCode("escrow", 2, "", codeTxFailed))
}
