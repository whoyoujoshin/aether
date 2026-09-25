package directory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/whoyoujoshin/aether/paywall"
)

const (
	fetchTimeout     = 5 * time.Second
	maxManifestBytes = 64 << 10
)

// ErrPrivateAddress means a service URL resolved to a loopback, private
// or otherwise internal address.
var ErrPrivateAddress = errors.New("refusing to fetch from a private or internal address")

// SafeFetcher fetches manifests from URLs that anyone can announce on
// chain, so by default it refuses internal addresses: otherwise an
// announcement could make whoever lists services (an explorer server, an
// agent's machine) probe its own network. The check runs on the address
// actually dialed, so DNS can't swap it after the check. allowPrivate is
// for local devnets and tests.
func SafeFetcher(allowPrivate bool) Fetcher {
	dialer := &net.Dialer{Timeout: fetchTimeout}
	transport := &http.Transport{
		Proxy: nil, // the address check must apply to the real destination
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				if !allowPrivate && isInternal(ip) {
					continue
				}
				return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			}
			return nil, ErrPrivateAddress
		},
		ResponseHeaderTimeout: fetchTimeout,
		MaxIdleConns:          16,
		IdleConnTimeout:       30 * time.Second,
	}
	client := &http.Client{
		Transport:     transport,
		Timeout:       fetchTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return func(ctx context.Context, baseURL string) (*paywall.Manifest, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+paywall.ManifestPath, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("manifest: HTTP %d", resp.StatusCode)
		}
		var m paywall.Manifest
		if err := json.NewDecoder(io.LimitReader(resp.Body, maxManifestBytes)).Decode(&m); err != nil {
			return nil, fmt.Errorf("manifest: %w", err)
		}
		return &m, nil
	}
}

func isInternal(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() ||
		cgnat.Contains(ip) || ip == metadataV4
}

var (
	cgnat      = netip.MustParsePrefix("100.64.0.0/10")
	metadataV4 = netip.MustParseAddr("169.254.169.254") // also link-local; explicit for clarity
)
