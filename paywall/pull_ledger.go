package paywall

import (
	"fmt"
	"sort"
	"time"

	"cosmossdk.io/math"
)

// FileLedger's PullLedger.

type pullRecord struct {
	Accrued string      `json:"accrued,omitempty"` // uaeth
	Unpaid  string      `json:"unpaid,omitempty"`
	Open    *Collection `json:"open,omitempty"`
}

var _ PullLedger = (*FileLedger)(nil)

func amountOr0(s string) math.Int {
	if a, ok := math.NewIntFromString(s); ok {
		return a
	}
	return math.ZeroInt()
}

func str(a math.Int) string {
	if a.IsZero() {
		return ""
	}
	return a.String()
}

func (l *FileLedger) pullRec(account string) *pullRecord {
	r := l.state.Pull[account]
	if r == nil {
		r = &pullRecord{}
		l.state.Pull[account] = r
	}
	return r
}

func (l *FileLedger) pullAccount(account string) PullAccount {
	a := PullAccount{Accrued: math.ZeroInt(), InFlight: math.ZeroInt(), Unpaid: math.ZeroInt()}
	if r := l.state.Pull[account]; r != nil {
		a.Accrued, a.Unpaid = amountOr0(r.Accrued), amountOr0(r.Unpaid)
		if r.Open != nil {
			a.InFlight = amountOr0(r.Open.Amount)
		}
	}
	return a
}

// dropIfEmpty forgets an account that owes nothing.
func (l *FileLedger) dropIfEmpty(account string) {
	if r := l.state.Pull[account]; r != nil && r.Accrued == "" && r.Unpaid == "" && r.Open == nil {
		delete(l.state.Pull, account)
	}
}

func (l *FileLedger) Accrue(account, requestID string, amount, limit math.Int, forgetAfter time.Time) (math.Int, bool, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := account + "/" + requestID
	accrued := l.pullAccount(account).Accrued
	if _, seen := l.state.PullRequests[key]; seen {
		return accrued, false, false, nil
	}
	next := accrued.Add(amount)
	if next.GT(limit) {
		return accrued, false, true, nil
	}
	r := l.pullRec(account)
	prev := r.Accrued
	r.Accrued = str(next)
	l.state.PullRequests[key] = chargedRequest{Amount: amount.String(), ForgetAfter: forgetAfter}
	if err := l.save(); err != nil {
		r.Accrued = prev
		delete(l.state.PullRequests, key)
		l.dropIfEmpty(account)
		return accrued, false, false, err
	}
	return next, true, true, nil
}

func (l *FileLedger) Unaccrue(account, requestID string, amount math.Int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := account + "/" + requestID
	if _, ok := l.state.PullRequests[key]; !ok {
		return fmt.Errorf("request %s was not charged", key)
	}
	accrued := l.pullAccount(account).Accrued
	if accrued.LT(amount) {
		return fmt.Errorf("request %s is already being collected", key)
	}
	r := l.pullRec(account)
	prev, prevReq := r.Accrued, l.state.PullRequests[key]
	r.Accrued = str(accrued.Sub(amount))
	delete(l.state.PullRequests, key)
	l.dropIfEmpty(account)
	if err := l.save(); err != nil {
		l.pullRec(account).Accrued = prev
		l.state.PullRequests[key] = prevReq
		return err
	}
	return nil
}

func (l *FileLedger) PullAccount(account string) (PullAccount, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.pullAccount(account), nil
}

func (l *FileLedger) Collectable() ([]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for a, r := range l.state.Pull {
		if r.Accrued != "" || r.Open != nil {
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (l *FileLedger) OpenCollection(account, id string, at time.Time) (Collection, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.state.Pull[account]
	if r == nil {
		return Collection{}, false, nil
	}
	if r.Open != nil {
		return *r.Open, true, nil
	}
	if r.Accrued == "" {
		return Collection{}, false, nil
	}
	c := Collection{ID: id, Account: account, Amount: r.Accrued, Status: WithdrawalReserved, At: at}
	prev := r.Accrued
	r.Open, r.Accrued = &c, ""
	if err := l.save(); err != nil {
		r.Open, r.Accrued = nil, prev
		return Collection{}, false, err
	}
	return c, true, nil
}

func (l *FileLedger) SaveCollection(c Collection) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.state.Pull[c.Account]
	if r == nil || r.Open == nil || r.Open.ID != c.ID {
		return fmt.Errorf("collection %s is not open", c.ID)
	}
	prev := *r.Open
	*r.Open = c
	if err := l.save(); err != nil {
		*r.Open = prev
		return err
	}
	return nil
}

func (l *FileLedger) CloseCollection(account string, collected bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.state.Pull[account]
	if r == nil || r.Open == nil {
		return fmt.Errorf("%s has no open collection", account)
	}
	orig, prevUnpaid := *r.Open, r.Unpaid
	c := orig
	c.TxBytes = nil
	if collected {
		c.Status = WithdrawalConfirmed
	} else {
		c.Status = CollectionFailed
		r.Unpaid = str(amountOr0(r.Unpaid).Add(amountOr0(c.Amount)))
	}
	l.state.Collections[c.ID] = c
	r.Open = nil
	l.dropIfEmpty(account)
	if err := l.save(); err != nil {
		rec := l.pullRec(account)
		rec.Open, rec.Unpaid = &orig, prevUnpaid
		delete(l.state.Collections, c.ID)
		return err
	}
	return nil
}

func (l *FileLedger) Reinstate(account string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.state.Pull[account]
	if r == nil || r.Unpaid == "" {
		return nil
	}
	prevA, prevU := r.Accrued, r.Unpaid
	r.Accrued, r.Unpaid = str(amountOr0(r.Accrued).Add(amountOr0(r.Unpaid))), ""
	if err := l.save(); err != nil {
		r.Accrued, r.Unpaid = prevA, prevU
		return err
	}
	return nil
}
