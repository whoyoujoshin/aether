// Package directory lists paid services (package paywall) from
// announcements on chain, so agents can find them without a central
// registry.
//
// A seller announces a service by sending AnnounceAmount uaeth to
// Address() with memo AnnouncePrefix + its base URL (DelistPrefix to
// withdraw it). The cost deters spam, and Address() is derived from a
// name, so nobody holds its key. A listing is shown only if the
// manifest at that URL (paywall.ManifestPath) names the announcer as its
// payee: nobody can list someone else's service under their own address.
//
// Everything a listing says besides the payee, network and price comes
// from the service itself and is untrusted.
package directory

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/address"

	"github.com/whoyoujoshin/aether/paywall"
	"github.com/whoyoujoshin/aether/wallet"
)

const (
	AnnouncePrefix = "x402-service:"
	DelistPrefix   = "x402-delist:"
	// AnnounceAmount (uaeth) must be sent with an announcement.
	AnnounceAmount = 1
	maxURLLength   = 200
)

// Address is where announcements are sent. It's derived from a fixed
// name, so no key for it exists: what's sent there stays there.
func Address() string {
	return sdk.AccAddress(address.Module("aether-x402-directory")).String()
}

// NormalizeURL validates a service's base URL and puts it in the form
// announcements are compared in.
func NormalizeURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("service URL %q must be an absolute http(s) URL", raw)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("service URL %q must not have credentials, a query or a fragment", raw)
	}
	u.Scheme, u.Host = strings.ToLower(u.Scheme), strings.ToLower(u.Host)
	s := strings.TrimRight(u.String(), "/")
	if len(s) > maxURLLength {
		return "", fmt.Errorf("service URL is longer than %d characters", maxURLLength)
	}
	return s, nil
}

// Announcement is a service listed on chain and not since delisted.
type Announcement struct {
	URL       string
	Announcer string // who sent it: the service's payee must be this account
	TxHash    string
	Height    int64
}

// Announcements folds payments to Address() (oldest first) into the
// current announcements, newest first.
func Announcements(payments []wallet.IncomingPayment) []Announcement {
	type key struct{ announcer, url string }
	current := map[key]Announcement{}
	for _, p := range payments {
		if p.Code != 0 || p.From == "" || p.Amount.AmountOf(wallet.BaseDenom).LT(sdkInt(AnnounceAmount)) {
			continue
		}
		var raw string
		delist := false
		switch {
		case strings.HasPrefix(p.Memo, AnnouncePrefix):
			raw = strings.TrimPrefix(p.Memo, AnnouncePrefix)
		case strings.HasPrefix(p.Memo, DelistPrefix):
			raw, delist = strings.TrimPrefix(p.Memo, DelistPrefix), true
		default:
			continue
		}
		u, err := NormalizeURL(raw)
		if err != nil {
			continue
		}
		k := key{p.From, u}
		if delist {
			delete(current, k)
		} else {
			current[k] = Announcement{URL: u, Announcer: p.From, TxHash: p.Hash, Height: p.Height}
		}
	}
	out := make([]Announcement, 0, len(current))
	for _, a := range current {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Height != out[j].Height {
			return out[i].Height > out[j].Height
		}
		return out[i].URL < out[j].URL
	})
	return out
}

// Listing is a verified service.
type Listing struct {
	Announcement
	Manifest paywall.Manifest
	// Reputation is set when the Directory can scan payees (ScanPayee).
	Reputation *Reputation
}

// Verify checks a manifest against its announcement.
func Verify(a Announcement, m *paywall.Manifest, network string) error {
	switch {
	case m.X402Version != paywall.X402Version:
		return fmt.Errorf("unsupported x402Version %d", m.X402Version)
	case m.Network != network:
		return fmt.Errorf("manifest is for network %q, not %q", m.Network, network)
	case m.PayTo != a.Announcer:
		return errors.New("the manifest's payee is not the account that announced it")
	}
	if _, err := wallet.ParseUaeth(m.Price); err != nil {
		return fmt.Errorf("manifest price: %w", err)
	}
	return nil
}

// Fetcher fetches a service's manifest from its base URL.
type Fetcher func(ctx context.Context, baseURL string) (*paywall.Manifest, error)

// Directory keeps a cached, verified list of services.
type Directory struct {
	// Scan returns payments to Address(), oldest first.
	Scan    func() ([]wallet.IncomingPayment, error)
	Fetch   Fetcher
	Network string
	TTL     time.Duration // default 2m

	// ScanPayee, with LatestHeight, adds each listing's Reputation: it
	// returns payments to address at or above sinceHeight, oldest first.
	ScanPayee    func(address string, sinceHeight int64) ([]wallet.IncomingPayment, error)
	LatestHeight func() (int64, error)
	Window       int64 // blocks reputation looks back; default DefaultWindow

	mu      sync.Mutex
	at      time.Time
	cached  []Listing
	refresh sync.Mutex
}

// Listings returns verified services, newest first, refreshing the
// cache when it's older than TTL.
func (d *Directory) Listings(ctx context.Context) ([]Listing, error) {
	ttl := d.TTL
	if ttl <= 0 {
		ttl = 2 * time.Minute
	}
	d.mu.Lock()
	if !d.at.IsZero() && time.Since(d.at) < ttl {
		out := d.cached
		d.mu.Unlock()
		return out, nil
	}
	d.mu.Unlock()

	d.refresh.Lock() // one refresh at a time
	defer d.refresh.Unlock()
	d.mu.Lock()
	if !d.at.IsZero() && time.Since(d.at) < ttl {
		out := d.cached
		d.mu.Unlock()
		return out, nil
	}
	d.mu.Unlock()

	payments, err := d.Scan()
	if err != nil && !errors.Is(err, wallet.ErrTooMuchHistory) {
		return nil, err
	}
	listings := Resolve(ctx, Announcements(payments), d.Network, d.Fetch)
	if d.ScanPayee != nil && d.LatestHeight != nil {
		d.assess(listings, payments)
	}
	d.mu.Lock()
	d.cached, d.at = listings, time.Now()
	d.mu.Unlock()
	return listings, nil
}

// assess adds reputations to listings. A payee that can't be scanned
// just gets none.
func (d *Directory) assess(listings []Listing, directoryPayments []wallet.IncomingPayment) {
	height, err := d.LatestHeight()
	if err != nil {
		return
	}
	window := d.Window
	if window <= 0 {
		window = DefaultWindow
	}
	since := height - window
	if since < 1 {
		since = 1
	}
	ratings := Ratings(directoryPayments, since)
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i := range listings {
		wg.Add(1)
		go func(l *Listing) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			paid, err := d.ScanPayee(l.Manifest.PayTo, since)
			if err != nil && !errors.Is(err, wallet.ErrTooMuchHistory) {
				return
			}
			rep := Assess(l.URL, l.Manifest.PayTo, paid, ratings, since)
			l.Reputation = &rep
		}(&listings[i])
	}
	wg.Wait()
}

// Resolve fetches and verifies each announcement's manifest, dropping
// any that fail.
func Resolve(ctx context.Context, anns []Announcement, network string, fetch Fetcher) []Listing {
	results := make([]*Listing, len(anns))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, a := range anns {
		wg.Add(1)
		go func(i int, a Announcement) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			m, err := fetch(ctx, a.URL)
			if err != nil || Verify(a, m, network) != nil {
				return
			}
			results[i] = &Listing{Announcement: a, Manifest: *m}
		}(i, a)
	}
	wg.Wait()
	out := []Listing{}
	for _, l := range results {
		if l != nil {
			out = append(out, *l)
		}
	}
	return out
}

func sdkInt(v int64) sdkmath.Int { return sdkmath.NewInt(v) }
