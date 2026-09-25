package main

import (
	"context"
	"log"
	"sync"
	"time"

	rpchttp "github.com/cometbft/cometbft/rpc/client/http"
)

// Waiting tools (wait_for_transaction, wait_for_payment, fetch_paid)
// re-check the chain when a new block is committed, pushed over the
// node's CometBFT websocket, instead of polling on a timer. One
// subscription serves every waiting call.
//
// The push is only a wake-up: what's checked is always read from the
// node's indexed state, so a missed or late event costs latency, never
// a missed payment. Without a live feed they fall back to polling.

var (
	pollInterval   = 3 * time.Second  // with no live block feed
	safetyInterval = 15 * time.Second // re-check anyway, even with one
	// feedStaleAfter: blocks are ~60s apart, so this long without one
	// means the feed (or the chain) is stuck.
	feedStaleAfter = 2 * time.Minute
)

type blockNotifier struct {
	mu        sync.Mutex
	next      chan struct{} // closed on the next block
	lastBlock time.Time
}

var blocks = newBlockNotifier()

func newBlockNotifier() *blockNotifier { return &blockNotifier{next: make(chan struct{})} }

func (n *blockNotifier) signal() {
	n.mu.Lock()
	defer n.mu.Unlock()
	close(n.next)
	n.next = make(chan struct{})
	n.lastBlock = time.Now()
}

// wait returns at the next block, after a fallback interval, at
// deadline, or when ctx ends -- whichever is first.
func (n *blockNotifier) wait(ctx context.Context, deadline time.Time) {
	n.mu.Lock()
	next := n.next
	interval := pollInterval
	if !n.lastBlock.IsZero() && time.Since(n.lastBlock) < feedStaleAfter {
		interval = safetyInterval
	}
	n.mu.Unlock()

	if until := time.Until(deadline); until < interval {
		interval = until
	}
	if interval <= 0 {
		return
	}
	t := time.NewTimer(interval)
	defer t.Stop()
	select {
	case <-next:
	case <-t.C:
	case <-ctx.Done():
	}
}

// run keeps a NewBlock subscription open on rpcURL until ctx ends.
func (n *blockNotifier) run(ctx context.Context, rpcURL string) {
	backoff := 5 * time.Second
	for ctx.Err() == nil {
		err := n.subscribe(ctx, rpcURL)
		if ctx.Err() != nil {
			return
		}
		log.Printf("block feed from %s unavailable (%v); polling every %s until it's back", rpcURL, err, pollInterval)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, time.Minute)
	}
}

func (n *blockNotifier) subscribe(ctx context.Context, rpcURL string) error {
	c, err := rpchttp.New(rpcURL, "/websocket")
	if err != nil {
		return err
	}
	if err := c.Start(); err != nil {
		return err
	}
	defer func() { _ = c.Stop() }()
	subCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	events, err := c.Subscribe(subCtx, "agentmcp", "tm.event='NewBlock'", 8)
	cancel()
	if err != nil {
		return err
	}
	log.Printf("block feed from %s connected", rpcURL)
	// The client reconnects and resubscribes by itself; events stops
	// only when we stop it.
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-events:
			n.signal()
		}
	}
}
