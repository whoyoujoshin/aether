package paywall

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"cosmossdk.io/math"
)

// FileLedger is a Ledger kept in one JSON file, rewritten atomically
// (temp file, fsync, rename) on every change, so a crash never loses or
// half-writes a balance. It suits one server process; several instances
// need a shared Ledger (e.g. a database) instead.
type FileLedger struct {
	mu    sync.Mutex
	path  string // "" keeps everything in memory (tests)
	state ledgerState
}

type ledgerState struct {
	Balances map[string]string         `json:"balances"` // account -> uaeth
	Deposits map[string]depositRecord  `json:"deposits"` // tx hash -> credit
	Requests map[string]chargedRequest `json:"requests"` // account/requestID -> charge
	// account/withdrawalID -> withdrawal. Kept for good: they're the
	// record of money paid back.
	Withdrawals map[string]Withdrawal `json:"withdrawals,omitempty"`
}

// Withdrawal is a prepaid balance paid back to its account.
type Withdrawal struct {
	ID        string `json:"id"`
	Account   string `json:"account"`
	Requested string `json:"requested"` // "all" or uaeth, as asked
	Amount    string `json:"amount"`    // uaeth
	Status    string `json:"status"`    // reserved, pending or confirmed
	// The signed payout, kept from before it's broadcast until it's in
	// a block, so it is only ever re-sent, never paid a second time.
	TxHash   string    `json:"txHash,omitempty"`
	TxBytes  []byte    `json:"txBytes,omitempty"`
	Sequence uint64    `json:"sequence,omitempty"`
	At       time.Time `json:"at"`
	// SequenceSpentAt is when the payout's sequence was first seen used
	// while the payout itself wasn't on chain.
	SequenceSpentAt time.Time `json:"sequenceSpentAt,omitempty"`
}

// Withdrawal statuses.
const (
	WithdrawalReserved  = "reserved"  // deducted, payout not yet signed
	WithdrawalPending   = "pending"   // payout signed (maybe broadcast), not in a block
	WithdrawalConfirmed = "confirmed" // payout in a block
)

var (
	ErrLedgerInsufficient = errors.New("insufficient balance")
	ErrLedgerBelowMinimum = errors.New("below the minimum withdrawal")
	errWithdrawalNotFound = errors.New("withdrawal not found")
)

type depositRecord struct {
	Account string    `json:"account"`
	Amount  string    `json:"amount"`
	At      time.Time `json:"at"`
}

type chargedRequest struct {
	Amount      string    `json:"amount"`
	ForgetAfter time.Time `json:"forgetAfter"`
}

// NewFileLedger opens (or creates) the ledger at path.
func NewFileLedger(path string) (*FileLedger, error) {
	l := &FileLedger{path: path, state: ledgerState{
		Balances: map[string]string{}, Deposits: map[string]depositRecord{}, Requests: map[string]chargedRequest{},
		Withdrawals: map[string]Withdrawal{},
	}}
	if path == "" {
		return l, nil
	}
	bz, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return l, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(bz, &l.state); err != nil {
		return nil, fmt.Errorf("prepaid ledger %s is unreadable: %w", path, err)
	}
	if l.state.Balances == nil {
		l.state.Balances = map[string]string{}
	}
	if l.state.Deposits == nil {
		l.state.Deposits = map[string]depositRecord{}
	}
	if l.state.Requests == nil {
		l.state.Requests = map[string]chargedRequest{}
	}
	if l.state.Withdrawals == nil {
		l.state.Withdrawals = map[string]Withdrawal{}
	}
	return l, nil
}

func (l *FileLedger) balance(account string) math.Int {
	if v, ok := math.NewIntFromString(l.state.Balances[account]); ok {
		return v
	}
	return math.ZeroInt()
}

func (l *FileLedger) Balance(account string) (math.Int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.balance(account), nil
}

func (l *FileLedger) Credit(depositTx, account string, amount math.Int) (math.Int, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, done := l.state.Deposits[depositTx]; done {
		return l.balance(account), false, nil
	}
	prev := l.state.Balances[account]
	next := l.balance(account).Add(amount)
	l.state.Balances[account] = next.String()
	l.state.Deposits[depositTx] = depositRecord{Account: account, Amount: amount.String(), At: time.Now().UTC()}
	if err := l.save(); err != nil {
		l.restoreBalance(account, prev)
		delete(l.state.Deposits, depositTx)
		return math.Int{}, false, err
	}
	return next, true, nil
}

func (l *FileLedger) Charge(account, requestID string, amount math.Int, forgetAfter time.Time) (math.Int, bool, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := account + "/" + requestID
	bal := l.balance(account)
	if _, seen := l.state.Requests[key]; seen {
		return bal, false, false, nil
	}
	if bal.LT(amount) {
		return bal, false, true, nil
	}
	prev := l.state.Balances[account]
	next := bal.Sub(amount)
	l.state.Balances[account] = next.String()
	l.state.Requests[key] = chargedRequest{Amount: amount.String(), ForgetAfter: forgetAfter}
	if err := l.save(); err != nil {
		l.restoreBalance(account, prev)
		delete(l.state.Requests, key)
		return math.Int{}, false, true, err
	}
	return next, true, true, nil
}

func (l *FileLedger) Refund(account, requestID string, amount math.Int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := account + "/" + requestID
	charge, charged := l.state.Requests[key]
	if !charged {
		return nil
	}
	prev := l.state.Balances[account]
	l.state.Balances[account] = l.balance(account).Add(amount).String()
	delete(l.state.Requests, key)
	if err := l.save(); err != nil {
		l.restoreBalance(account, prev)
		l.state.Requests[key] = charge
		return err
	}
	return nil
}

func (l *FileLedger) ReserveWithdrawal(account, id string, amount *math.Int, min math.Int, at time.Time) (Withdrawal, math.Int, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := account + "/" + id
	bal := l.balance(account)
	if w, seen := l.state.Withdrawals[key]; seen {
		return w, bal, false, nil
	}
	requested, take := "all", bal
	if amount != nil {
		requested, take = amount.String(), *amount
	}
	if !bal.IsPositive() || take.GT(bal) || !take.IsPositive() {
		return Withdrawal{}, bal, false, ErrLedgerInsufficient
	}
	if take.LT(min) && !take.Equal(bal) {
		return Withdrawal{}, bal, false, ErrLedgerBelowMinimum
	}
	prev := l.state.Balances[account]
	next := bal.Sub(take)
	w := Withdrawal{ID: id, Account: account, Requested: requested, Amount: take.String(), Status: WithdrawalReserved, At: at.UTC()}
	l.state.Balances[account] = next.String()
	l.state.Withdrawals[key] = w
	if err := l.save(); err != nil {
		l.restoreBalance(account, prev)
		delete(l.state.Withdrawals, key)
		return Withdrawal{}, math.Int{}, false, err
	}
	return w, next, true, nil
}

func (l *FileLedger) SaveWithdrawal(w Withdrawal) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := w.Account + "/" + w.ID
	prev, ok := l.state.Withdrawals[key]
	if !ok {
		return errWithdrawalNotFound
	}
	l.state.Withdrawals[key] = w
	if err := l.save(); err != nil {
		l.state.Withdrawals[key] = prev
		return err
	}
	return nil
}

func (l *FileLedger) CancelWithdrawal(account, id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := account + "/" + id
	w, ok := l.state.Withdrawals[key]
	if !ok {
		return errWithdrawalNotFound
	}
	amount, ok := math.NewIntFromString(w.Amount)
	if !ok {
		return fmt.Errorf("withdrawal %s has an unreadable amount %q", key, w.Amount)
	}
	prev := l.state.Balances[account]
	l.state.Balances[account] = l.balance(account).Add(amount).String()
	delete(l.state.Withdrawals, key)
	if err := l.save(); err != nil {
		l.restoreBalance(account, prev)
		l.state.Withdrawals[key] = w
		return err
	}
	return nil
}

func (l *FileLedger) restoreBalance(account, prev string) {
	if prev == "" {
		delete(l.state.Balances, account)
	} else {
		l.state.Balances[account] = prev
	}
}

func (l *FileLedger) save() error {
	now := time.Now()
	for k, r := range l.state.Requests {
		if !r.ForgetAfter.IsZero() && now.After(r.ForgetAfter) {
			delete(l.state.Requests, k)
		}
	}
	if l.path == "" {
		return nil
	}
	bz, err := json.MarshalIndent(l.state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return err
	}
	tmp := l.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(bz); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, l.path)
}
