package jobs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/whoyoujoshin/aether/wallet"
)

// ScanLimit bounds each page of a scan of the board's memos and of job
// escrows. A scan that fills a page goes on from where it stopped, so the
// board is never cut short.
const ScanLimit = 20000

// scanRounds bounds the pages one refresh reads; a board longer than that
// is read over several refreshes, and is refused until it is whole.
const scanRounds = 50

// ErrBoardIncomplete means the board's history wasn't read to the end, so
// no view of it is served: an incomplete board shows hired jobs as open
// and hides every newer post.
var ErrBoardIncomplete = errors.New("the job board's history isn't fully read yet; try again shortly")

// Board keeps a cached view of the board.
type Board struct {
	// Scan returns the payments to Address() at or above height from,
	// oldest first, with wallet.ErrTooMuchHistory (and what it has) when
	// it stops at its cap.
	Scan func(ctx context.Context, from int64) ([]wallet.IncomingPayment, error)
	// Escrows returns the escrows whose terms contain TermsPrefix created
	// at or above height from, oldest first, as Scan does.
	Escrows func(ctx context.Context, from int64) ([]wallet.EscrowCreated, error)
	TTL     time.Duration // default 30s

	mu      sync.Mutex
	at      time.Time
	cached  []Job
	refresh sync.Mutex
	history History
}

// FromClient reads the board through a node.
func FromClient(dial func() (*wallet.Client, error)) *Board {
	return &Board{
		Scan: func(ctx context.Context, from int64) ([]wallet.IncomingPayment, error) {
			c, err := dial()
			if err != nil {
				return nil, err
			}
			defer c.Close()
			return c.GetIncomingPayments(Address(), from, ScanLimit)
		},
		Escrows: func(ctx context.Context, from int64) ([]wallet.EscrowCreated, error) {
			c, err := dial()
			if err != nil {
				return nil, err
			}
			defer c.Close()
			return c.EscrowsCreatedWithTerms(ctx, TermsPrefix, from, ScanLimit)
		},
	}
}

// History is the board's memos and job escrows read so far. Each read
// asks only from the newest height it holds (inclusive) and pages on past
// a full page, so it reads the whole history once and then only what is
// new. The zero value is ready to use.
type History struct {
	mu       sync.Mutex
	payments []wallet.IncomingPayment
	escrows  []wallet.EscrowCreated
}

// Read brings the history up to date and returns all of it, oldest first.
// It fails rather than return part of it.
func (h *History) Read(scan func(from int64) ([]wallet.IncomingPayment, error), escrows func(from int64) ([]wallet.EscrowCreated, error)) ([]wallet.IncomingPayment, []wallet.EscrowCreated, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var err error
	if h.payments, err = catchUp(h.payments, func(p wallet.IncomingPayment) int64 { return p.Height }, scan); err != nil {
		return nil, nil, err
	}
	if h.escrows, err = catchUp(h.escrows, func(e wallet.EscrowCreated) int64 { return e.Height }, escrows); err != nil {
		return nil, nil, err
	}
	return append([]wallet.IncomingPayment(nil), h.payments...), append([]wallet.EscrowCreated(nil), h.escrows...), nil
}

// catchUp extends have, oldest first, to the end of what scan finds. It
// keeps whatever it read, even when it stops early with an error.
func catchUp[T any](have []T, height func(T) int64, scan func(from int64) ([]T, error)) ([]T, error) {
	for round := 0; round < scanRounds; round++ {
		from := int64(1)
		if n := len(have); n > 0 {
			from = height(have[n-1])
		}
		// The newest block held is read again in full: drop it first.
		keep := len(have)
		for keep > 0 && height(have[keep-1]) >= from {
			keep--
		}
		got, err := scan(from)
		capped := errors.Is(err, wallet.ErrTooMuchHistory)
		if err != nil && !capped {
			return have, err
		}
		if capped && (len(got) == 0 || height(got[len(got)-1]) <= from) {
			// One block holds more than a page: no way forward.
			return have, fmt.Errorf("%w: more than %d entries in block %d", ErrBoardIncomplete, ScanLimit, from)
		}
		have = append(have[:keep:keep], got...)
		if !capped {
			return have, nil
		}
	}
	return have, ErrBoardIncomplete
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
	payments, escrows, err := b.history.Read(
		func(from int64) ([]wallet.IncomingPayment, error) { return b.Scan(ctx, from) },
		func(from int64) ([]wallet.EscrowCreated, error) { return b.Escrows(ctx, from) })
	if err != nil {
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
