package jobs

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/whoyoujoshin/aether/wallet"
)

// ScanLimit bounds each scan of the board's memos and of job escrows.
const ScanLimit = 20000

// Board keeps a cached view of the board.
type Board struct {
	// Scan returns the payments to Address(), oldest first.
	Scan func(ctx context.Context) ([]wallet.IncomingPayment, error)
	// Escrows returns the escrows whose terms contain TermsPrefix,
	// oldest first.
	Escrows func(ctx context.Context) ([]wallet.EscrowCreated, error)
	TTL     time.Duration // default 30s

	mu      sync.Mutex
	at      time.Time
	cached  []Job
	refresh sync.Mutex
}

// FromClient reads the board through a node.
func FromClient(dial func() (*wallet.Client, error)) *Board {
	return &Board{
		Scan: func(ctx context.Context) ([]wallet.IncomingPayment, error) {
			c, err := dial()
			if err != nil {
				return nil, err
			}
			defer c.Close()
			return c.GetIncomingPayments(Address(), 1, ScanLimit)
		},
		Escrows: func(ctx context.Context) ([]wallet.EscrowCreated, error) {
			c, err := dial()
			if err != nil {
				return nil, err
			}
			defer c.Close()
			return c.EscrowsCreatedWithTerms(ctx, TermsPrefix, 1, ScanLimit)
		},
	}
}

// Jobs is the board, newest first, no older than TTL.
func (b *Board) Jobs(ctx context.Context) ([]Job, error) {
	ttl := b.TTL
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return b.within(ctx, ttl)
}

// Fresh is the board no older than maxAge: for checking what a
// transaction that just landed did.
func (b *Board) Fresh(ctx context.Context, maxAge time.Duration) ([]Job, error) {
	return b.within(ctx, maxAge)
}

func (b *Board) within(ctx context.Context, maxAge time.Duration) ([]Job, error) {
	if out, ok := b.cachedWithin(maxAge); ok {
		return out, nil
	}
	b.refresh.Lock() // one scan at a time
	defer b.refresh.Unlock()
	if out, ok := b.cachedWithin(maxAge); ok {
		return out, nil
	}
	payments, err := b.Scan(ctx)
	if err != nil && !errors.Is(err, wallet.ErrTooMuchHistory) {
		return nil, err
	}
	escrows, err := b.Escrows(ctx)
	if err != nil && !errors.Is(err, wallet.ErrTooMuchHistory) {
		return nil, err
	}
	jobs := Derive(payments, escrows)
	b.mu.Lock()
	b.cached, b.at = jobs, time.Now()
	b.mu.Unlock()
	return jobs, nil
}

func (b *Board) cachedWithin(maxAge time.Duration) ([]Job, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.at.IsZero() || time.Since(b.at) > maxAge {
		return nil, false
	}
	return b.cached, true
}

// Job is the job with this id.
func (b *Board) Job(ctx context.Context, id string, maxAge time.Duration) (*Job, error) {
	board, err := b.within(ctx, maxAge)
	if err != nil {
		return nil, err
	}
	for i := range board {
		if board[i].ID == id {
			j := board[i]
			return &j, nil
		}
	}
	return nil, nil
}

// Escrow states, as EscrowStatus reports them.
const (
	EscrowOpen     = "open"
	EscrowReleased = "released"
	EscrowRefunded = "refunded"
	EscrowUnknown  = "unknown" // settled moments ago, or the node lags
)

// EscrowState is where a hire's money is now.
type EscrowState struct {
	Status string `json:"status"`
	// By settled it: an address, or "expiry" at the deadline.
	By     string `json:"by,omitempty"`
	TxHash string `json:"txHash,omitempty"`
}

// EscrowStatus says whether escrow id is still open, and if not how it
// was settled.
func EscrowStatus(ctx context.Context, c *wallet.Client, id uint64) (EscrowState, error) {
	return EscrowStatusFrom(id,
		func(id uint64) error { _, err := c.Escrow(ctx, id); return err },
		func(id uint64) (*wallet.EscrowOutcome, error) { return c.EscrowOutcome(ctx, id) })
}

// EscrowStatusFrom is EscrowStatus over any reader: open returns nil for
// an open escrow and wallet.ErrEscrowNotFound for a settled one.
func EscrowStatusFrom(id uint64, open func(uint64) error, outcome func(uint64) (*wallet.EscrowOutcome, error)) (EscrowState, error) {
	if err := open(id); err == nil {
		return EscrowState{Status: EscrowOpen}, nil
	} else if !errors.Is(err, wallet.ErrEscrowNotFound) {
		return EscrowState{}, err
	}
	o, err := outcome(id)
	if errors.Is(err, wallet.ErrEscrowNotFound) {
		return EscrowState{Status: EscrowUnknown}, nil
	}
	if err != nil {
		return EscrowState{}, err
	}
	s := EscrowState{Status: EscrowRefunded, By: o.By, TxHash: o.TxHash}
	if o.Released {
		s.Status = EscrowReleased
	}
	return s, nil
}
