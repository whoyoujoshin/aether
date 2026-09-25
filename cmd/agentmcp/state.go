package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Persisted to disk, not just memory: a restart must not reset the
// rolling spend window (turning "daily limit" into "limit per process
// lifetime") or forget which idempotency keys already produced a
// transaction (turning a retried call into a second payment).

const (
	spendWindow = 24 * time.Hour
	// idempotencyRetention is how long a key stays bound to its
	// transaction. A retry after this builds a new payment.
	idempotencyRetention = 7 * 24 * time.Hour
)

type spendEvent struct {
	Time   time.Time `json:"time"`
	Amount int64     `json:"amountUaeth"`
	TxHash string    `json:"txHash,omitempty"`
}

// sendRecord binds an idempotency key to one signed transaction. The
// signed bytes are kept so a retry re-broadcasts the identical
// transaction rather than building a new one: its account sequence is
// signed in, so the chain can include it at most once however many
// times it is broadcast.
type sendRecord struct {
	From      string    `json:"from"`              // the signer: this agent
	Granter   string    `json:"granter,omitempty"` // whose funds, in grant mode
	To        string    `json:"to"`
	Amount    string    `json:"amountUaeth"`
	Memo      string    `json:"memo,omitempty"`
	TxHash    string    `json:"txHash"`
	TxBase64  string    `json:"txBase64"`
	Sequence  uint64    `json:"sequence"`
	Accepted  bool      `json:"accepted"` // passed CheckTx at least once
	CreatedAt time.Time `json:"createdAt"`
}

type agentState struct {
	Events  []spendEvent            `json:"events"`
	Sends   map[string]*sendRecord  `json:"sends,omitempty"`
	Fetches map[string]*fetchRecord `json:"fetches,omitempty"` // fetch_paid's quoted invoices
	// Prepaid holds a deposit per seller (payTo) not yet known to be
	// credited.
	Prepaid map[string]*prepaidDeposit `json:"prepaid,omitempty"`
	// Approvals are payments waiting for the owner, by idempotency key.
	Approvals map[string]*approvalRequest `json:"approvals,omitempty"`
}

// stateMu serializes every read-modify-write of the state file, and
// is held across send_aeth's whole check-sign-broadcast-record
// sequence so two concurrent calls can't both pass the budget check.
var stateMu sync.Mutex

func loadState() (*agentState, error) {
	st := &agentState{Sends: map[string]*sendRecord{}, Fetches: map[string]*fetchRecord{}, Prepaid: map[string]*prepaidDeposit{}, Approvals: map[string]*approvalRequest{}}
	bz, err := os.ReadFile(stateFile)
	if os.IsNotExist(err) {
		return st, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read agent state: %w", err)
	}
	if err := json.Unmarshal(bz, st); err != nil {
		return nil, fmt.Errorf("failed to parse agent state: %w", err)
	}
	if st.Sends == nil {
		st.Sends = map[string]*sendRecord{}
	}
	if st.Fetches == nil {
		st.Fetches = map[string]*fetchRecord{}
	}
	if st.Prepaid == nil {
		st.Prepaid = map[string]*prepaidDeposit{}
	}
	if st.Approvals == nil {
		st.Approvals = map[string]*approvalRequest{}
	}
	return st, nil
}

func (st *agentState) save() error {
	st.prune(time.Now())
	bz, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(stateFile); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	// Write-then-rename so a crash mid-write can't leave a truncated
	// file that would lose every idempotency record.
	tmp := stateFile + ".tmp"
	if err := os.WriteFile(tmp, bz, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, stateFile)
}

func (st *agentState) prune(now time.Time) {
	kept := st.Events[:0]
	for _, e := range st.Events {
		if e.Time.After(now.Add(-spendWindow)) {
			kept = append(kept, e)
		}
	}
	st.Events = kept
	for key, rec := range st.Sends {
		if rec.CreatedAt.Before(now.Add(-idempotencyRetention)) {
			delete(st.Sends, key)
		}
	}
	for key, req := range st.Approvals {
		if req.CreatedAt.Before(now.Add(-idempotencyRetention)) {
			delete(st.Approvals, key)
		}
	}
	for key, rec := range st.Fetches {
		if rec.CreatedAt.Before(now.Add(-idempotencyRetention)) {
			delete(st.Fetches, key)
		}
	}
}

func (st *agentState) spentInWindow(now time.Time) int64 {
	var total int64
	for _, e := range st.Events {
		if e.Time.After(now.Add(-spendWindow)) {
			total += e.Amount
		}
	}
	return total
}

// retryAfter is how long until enough of the window's spending ages
// out for amount to fit under limit; ok is false if it never will.
func (st *agentState) retryAfter(now time.Time, amount, limit int64) (time.Duration, bool) {
	if amount > limit {
		return 0, false
	}
	var live []spendEvent
	for _, e := range st.Events {
		if e.Time.After(now.Add(-spendWindow)) {
			live = append(live, e)
		}
	}
	sort.Slice(live, func(i, j int) bool { return live[i].Time.Before(live[j].Time) })
	excess := st.spentInWindow(now) + amount - limit
	for _, e := range live {
		if excess <= 0 {
			break
		}
		excess -= e.Amount
		if excess <= 0 {
			return e.Time.Add(spendWindow).Sub(now), true
		}
	}
	return 0, excess <= 0
}

// releaseSpend drops the budget reservation for a transaction that
// definitively did not spend anything (rejected, or failed on-chain).
func (st *agentState) releaseSpend(txHash string) bool {
	for i, e := range st.Events {
		if e.TxHash == txHash {
			st.Events = append(st.Events[:i], st.Events[i+1:]...)
			return true
		}
	}
	return false
}

func (st *agentState) hasSpend(txHash string) bool {
	for _, e := range st.Events {
		if e.TxHash == txHash {
			return true
		}
	}
	return false
}

// nextSequence accounts for this server's own transactions still in
// the mempool: the chain reports the committed sequence, so without
// this a second payment within one block time (~60s) would reuse the
// first one's sequence and be rejected.
func (st *agentState) nextSequence(from string, chainSeq uint64) uint64 {
	next := chainSeq
	for _, rec := range st.Sends {
		if rec.From == from && rec.Accepted && rec.Sequence >= next {
			next = rec.Sequence + 1
		}
	}
	return next
}
