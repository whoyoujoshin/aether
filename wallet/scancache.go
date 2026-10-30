package wallet

import (
	"errors"
	"os"
	"sync"
)

// Scans of a chain's whole history (GetIncomingPayments,
// EscrowsCreatedWithTerms) ran from their starting height on every
// call: the directory and job board from block 1 every refresh, each
// re-decoding every matching transaction on the node they ask, a
// validator on the seed. A committed block never changes, so this
// cache keeps each scan's results and the next call asks the node only
// for what's new: from the height of the newest result it holds,
// inclusive, so the rest of that block is read again and nothing is
// skipped (the node indexes blocks in order). Results are the same as
// a full scan's.
//
// A scan that errors or hits its cap isn't kept. AETHER_SCAN_CACHE=off
// turns the cache off (every call scans in full, as before).

// scanCache holds one entry per (node, scan, key).
type scanCache[T any] struct {
	mu      sync.Mutex
	entries map[string]*scanEntry[T]
}

type scanEntry[T any] struct {
	mu    sync.Mutex
	from  int64 // the lowest height the entry covers
	items []T   // oldest first, every one at or above from
	ok    bool  // the entry holds a complete scan
}

// scanFn scans from a height, oldest first, as the uncached functions do.
type scanFn[T any] func(from int64, maxResults int) ([]T, error)

// scanCacheMaxItems bounds an entry: past it, results below the
// requested height are dropped.
const scanCacheMaxItems = 20_000

func scanCacheOff() bool { return os.Getenv("AETHER_SCAN_CACHE") == "off" }

func (c *scanCache[T]) entry(key string) *scanEntry[T] {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]*scanEntry[T]{}
	}
	e := c.entries[key]
	if e == nil {
		e = &scanEntry[T]{}
		c.entries[key] = e
	}
	return e
}

// scan returns what scanFn(since, maxResults) would: every result at or
// above since, oldest first, with ErrTooMuchHistory (and the oldest
// maxResults) past maxResults.
func (c *scanCache[T]) scan(key string, since int64, maxResults int, height func(T) int64, full scanFn[T]) ([]T, error) {
	if since < 1 {
		since = 1
	}
	if scanCacheOff() {
		return full(since, maxResults)
	}
	e := c.entry(key)
	e.mu.Lock() // one refresh per entry at a time; callers waiting reuse it
	defer e.mu.Unlock()

	if !e.ok || since < e.from {
		items, err := full(since, maxResults)
		if err != nil {
			e.ok = false
			return items, err
		}
		e.from, e.items, e.ok = since, items, true
		return e.window(since, maxResults, height)
	}

	// Resume at the newest result's height (inclusive), or at the
	// entry's start if it holds none.
	resume := e.from
	if n := len(e.items); n > 0 {
		resume = height(e.items[n-1])
	}
	keep := 0
	for keep < len(e.items) && height(e.items[keep]) < resume {
		keep++
	}
	fresh, err := full(resume, maxResults)
	if err != nil {
		if !errors.Is(err, ErrTooMuchHistory) {
			return nil, err
		}
		// Too much new history to hold: answer this call in full, and
		// start over next time.
		e.ok = false
		return full(since, maxResults)
	}
	e.items = append(e.items[:keep:keep], fresh...)
	if len(e.items) > scanCacheMaxItems && since > e.from {
		drop := 0
		for drop < len(e.items) && height(e.items[drop]) < since {
			drop++
		}
		e.items = append([]T(nil), e.items[drop:]...)
		e.from = since
	}
	return e.window(since, maxResults, height)
}

// window copies the results at or above since; ErrTooMuchHistory and
// the oldest maxResults past maxResults.
func (e *scanEntry[T]) window(since int64, maxResults int, height func(T) int64) ([]T, error) {
	start := 0
	for start < len(e.items) && height(e.items[start]) < since {
		start++
	}
	out := append([]T(nil), e.items[start:]...)
	if maxResults > 0 && len(out) >= maxResults {
		return out[:maxResults], ErrTooMuchHistory
	}
	return out, nil
}

var (
	incomingScans scanCache[IncomingPayment]
	escrowScans   scanCache[EscrowCreated]
)
