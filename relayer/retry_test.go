package relayer

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var testWaits = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}

func TestRetryTransportReplaysRefusedRequests(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"method":"broadcast_tx_sync"}` {
			t.Errorf("attempt %d got body %q", calls.Load()+1, body)
		}
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := &http.Client{Transport: retryTransport{base: http.DefaultTransport, waits: testWaits}}
	resp, err := c.Post(srv.URL, "application/json", strings.NewReader(`{"method":"broadcast_tx_sync"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || calls.Load() != 3 {
		t.Fatalf("status %d after %d calls", resp.StatusCode, calls.Load())
	}
}

func TestRetryTransportGivesUp(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	c := &http.Client{Transport: retryTransport{base: http.DefaultTransport, waits: testWaits}}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests || calls.Load() != int32(len(testWaits)+1) {
		t.Fatalf("status %d after %d calls", resp.StatusCode, calls.Load())
	}
}

func TestRetryTransportLeavesOtherAnswersAlone(t *testing.T) {
	for _, code := range []int{http.StatusBadRequest, http.StatusInternalServerError, http.StatusGatewayTimeout} {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.WriteHeader(code)
		}))
		c := &http.Client{Transport: retryTransport{base: http.DefaultTransport, waits: testWaits}}
		resp, err := c.Get(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		srv.Close()
		// 504 may mean the node got the request and is slow: sending it
		// again isn't known to be harmless.
		if calls.Load() != 1 {
			t.Errorf("HTTP %d sent %d times", code, calls.Load())
		}
	}
}

func TestRetryTransportRetriesARefusedConnection(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close() // nothing listens: every dial is refused

	var dials atomic.Int32
	base := &http.Transport{DialContext: func(ctx context.Context, network, a string) (net.Conn, error) {
		dials.Add(1)
		return (&net.Dialer{}).DialContext(ctx, network, a)
	}}
	c := &http.Client{Transport: retryTransport{base: base, waits: testWaits}}
	_, err = c.Get("http://" + addr)
	if err == nil {
		t.Fatal("expected an error")
	}
	if dials.Load() != int32(len(testWaits)+1) {
		t.Fatalf("%d dials", dials.Load())
	}
}

func TestRetryTransportStopsWithTheContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	c := &http.Client{Transport: retryTransport{base: http.DefaultTransport, waits: []time.Duration{time.Hour}}}
	start := time.Now()
	if _, err := c.Do(req); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("waited past the context")
	}
}

func TestRetryUnary(t *testing.T) {
	cases := []struct {
		err   error
		calls int
	}{
		{status.Error(codes.Unavailable, "unexpected HTTP status code received from server: 503"), 4},
		{status.Error(codes.ResourceExhausted, "too many requests"), 4},
		{status.Error(codes.PermissionDenied, "unexpected HTTP status code received from server: 403 (Forbidden)"), 4},
		{status.Error(codes.PermissionDenied, "signature verification failed"), 1},
		{status.Error(codes.NotFound, "account not found"), 1},
		{errors.New("not a status"), 1},
	}
	for _, tc := range cases {
		calls := 0
		invoker := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
			calls++
			return tc.err
		}
		err := retryUnary(testWaits)(context.Background(), "/x", nil, nil, nil, invoker)
		if err != tc.err || calls != tc.calls {
			t.Errorf("%v: %d calls, err %v", tc.err, calls, err)
		}
	}

	calls := 0
	invoker := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		calls++
		if calls < 3 {
			return status.Error(codes.Unavailable, "503")
		}
		return nil
	}
	if err := retryUnary(testWaits)(context.Background(), "/x", nil, nil, nil, invoker); err != nil || calls != 3 {
		t.Fatalf("%d calls, err %v", calls, err)
	}
}
