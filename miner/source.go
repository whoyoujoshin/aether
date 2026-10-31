package miner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// FromExplorer reads the work from an Aether explorer's /api/mine/work,
// which answers every miner from one cached read of its node.
func FromExplorer(ctx context.Context, client *http.Client, explorer string) (Work, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(explorer, "/")+"/api/mine/work", nil)
	if err != nil {
		return Work{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return Work{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return Work{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return Work{}, fmt.Errorf("%s/api/mine/work: HTTP %d", explorer, resp.StatusCode)
	}
	var w Work
	if err := json.Unmarshal(body, &w); err != nil {
		return Work{}, fmt.Errorf("%s/api/mine/work: %w", explorer, err)
	}
	return w, nil
}

// Cache answers with the last work read for up to TTL, and at most one
// read is in flight: however many miners ask, the node is asked about
// once per TTL.
type Cache struct {
	TTL  time.Duration
	Read func(context.Context) (Work, error)

	mu     sync.Mutex
	work   Work
	at     time.Time // last read, good or not
	goodAt time.Time // last good read
	err    error
}

// Get is the cached work, read again once older than TTL. A failed read
// is cached for TTL too, so a down node isn't asked by every miner.
func (c *Cache) Get(ctx context.Context) (Work, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && time.Since(c.at) < c.TTL {
		return c.work, c.err
	}
	w, err := c.Read(ctx)
	c.at = time.Now()
	if err == nil {
		c.work, c.goodAt, c.err = w, c.at, nil
		return w, nil
	}
	if !c.goodAt.IsZero() && time.Since(c.goodAt) < 10*c.TTL {
		// Through a short outage, the last good work: a header on it may
		// still land.
		c.err = nil
		return c.work, nil
	}
	c.work, c.err = Work{}, err
	return Work{}, err
}
