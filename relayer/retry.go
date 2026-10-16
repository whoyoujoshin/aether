package relayer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// retryWaits are the pauses between attempts at a request a public RPC
// refused at its gateway, about four minutes in all. On 2026-10-16
// Osmosis's testnet RPC answered half of the seed's requests with 403
// for hours: a handshake of a hundred requests needs every one to get
// through, and a step that fails halfway leaves clients behind.
var retryWaits = []time.Duration{
	time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 15 * time.Second,
	30 * time.Second, 30 * time.Second, 30 * time.Second, 30 * time.Second, 30 * time.Second, 30 * time.Second,
}

// retryTransport retries a request only when it never reached the node:
// the gateway refused it (403, 429, 502, 503) or the connection never
// opened. A broadcast is safe to send again on those: if a first one got
// through after all, the node answers that the transaction is already in
// its cache (see SignAndBroadcast).
type retryTransport struct {
	base  http.RoundTripper
	waits []time.Duration
}

func (t retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			return nil, err
		}
		body = b
	}
	for attempt := 0; ; attempt++ {
		r := req.Clone(req.Context())
		if body != nil {
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		resp, err := t.base.RoundTrip(r)
		refused := (err != nil && neverSent(err)) || (err == nil && refusedStatus(resp.StatusCode))
		if !refused || attempt >= len(t.waits) {
			return resp, err
		}
		why := ""
		if resp != nil {
			why = resp.Status
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		} else {
			why = err.Error()
		}
		logRetry(req.URL.Host, why, attempt, t.waits)
		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-time.After(t.waits[attempt]):
		}
	}
}

// logRetry says a request was refused and when the next try goes, so a
// run that is waiting out a refusing RPC doesn't look stuck.
func logRetry(host, why string, attempt int, waits []time.Duration) {
	log.Printf("%s refused a request (%s): try %d of %d in %s", host, why, attempt+2, len(waits)+1, waits[attempt])
}

func refusedStatus(code int) bool {
	switch code {
	case http.StatusForbidden, http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable:
		return true
	}
	return false
}

// neverSent is a failure to open the connection, before any byte of the
// request went out.
func neverSent(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}

// retryUnary retries a gRPC call the server's gateway refused. Every gRPC
// call a Chain makes is a query (broadcasts go over RPC), so a retry
// can't do anything twice. gRPC reports HTTP 429, 502 and 503 as
// Unavailable and 403 as PermissionDenied.
func retryUnary(waits []time.Duration) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		for attempt := 0; ; attempt++ {
			err := invoker(ctx, method, req, reply, cc, opts...)
			if err == nil || attempt >= len(waits) || !refusedGRPC(err) {
				return err
			}
			target := "grpc"
			if cc != nil {
				target = cc.Target()
			}
			logRetry(target, status.Code(err).String(), attempt, waits)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(waits[attempt]):
			}
		}
	}
}

func refusedGRPC(err error) bool {
	s, ok := status.FromError(err)
	if !ok {
		return false
	}
	switch s.Code() {
	case codes.Unavailable, codes.ResourceExhausted:
		return true
	case codes.PermissionDenied:
		return strings.Contains(s.Message(), "403")
	}
	return false
}
