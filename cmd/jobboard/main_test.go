package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/jobs"
	"github.com/whoyoujoshin/aether/wallet"
)

// fakeChain is the board's memos and escrows, as a test adds them.
type fakeChain struct {
	mu       sync.Mutex
	payments []wallet.IncomingPayment
	escrows  []wallet.EscrowCreated
}

func (c *fakeChain) add(hash string, height int64, from, memo string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.payments = append(c.payments, wallet.IncomingPayment{Hash: hash, Height: height, From: from, Memo: memo,
		Amount: sdk.NewCoins(sdk.NewInt64Coin("uaeth", 1))})
}

func newTestServer(t *testing.T, c *fakeChain) *httptest.Server {
	st, err := openStore(t.TempDir(), 1<<20)
	require.NoError(t, err)
	s := &server{
		board: &jobs.Board{
			Scan: func(_ context.Context, from int64) ([]wallet.IncomingPayment, error) {
				c.mu.Lock()
				defer c.mu.Unlock()
				var out []wallet.IncomingPayment
				for _, p := range c.payments {
					if p.Height >= from {
						out = append(out, p)
					}
				}
				return out, nil
			},
			Escrows: func(_ context.Context, from int64) ([]wallet.EscrowCreated, error) {
				c.mu.Lock()
				defer c.mu.Unlock()
				var out []wallet.EscrowCreated
				for _, e := range c.escrows {
					if e.Height >= from {
						out = append(out, e)
					}
				}
				return out, nil
			},
		},
		store: st,
		escrow: func(context.Context, uint64) (jobs.EscrowState, error) {
			return jobs.EscrowState{Status: jobs.EscrowOpen}, nil
		},
	}
	srv := httptest.NewServer(s.handler())
	t.Cleanup(srv.Close)
	return srv
}

func put(t *testing.T, url string, body []byte) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPut, url+"/jobs/blobs/"+jobs.Hash(body), bytes.NewReader(body))
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	var b bytes.Buffer
	b.ReadFrom(res.Body)
	return res.StatusCode, b.String()
}

func getJSON(t *testing.T, url string, v any) int {
	t.Helper()
	res, err := http.Get(url)
	require.NoError(t, err)
	defer res.Body.Close()
	require.NoError(t, json.NewDecoder(res.Body).Decode(v))
	return res.StatusCode
}

func TestJobBoard(t *testing.T) {
	chain := &fakeChain{}
	srv := newTestServer(t, chain)

	spec, specHash, err := jobs.Spec{Title: "Translate a README", Description: "English to Spanish.", Budget: "1 AETH", Tags: []string{"translation"}}.Encode()
	require.NoError(t, err)

	// Nothing names it yet: refused.
	code, body := put(t, srv.URL, spec)
	require.Equal(t, http.StatusConflict, code, body)

	postTx := "00000000000000A1" + strings.Repeat("0", 48)
	chain.add(postTx, 10, "aether1buyer", jobs.PostMemo(specHash))
	code, _ = put(t, srv.URL, spec)
	require.Equal(t, http.StatusCreated, code)
	code, _ = put(t, srv.URL, spec)
	require.Equal(t, http.StatusOK, code, "storing it again is a no-op")

	// A body that isn't what its path names.
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/jobs/blobs/"+specHash, strings.NewReader("other"))
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode)

	var list listDTO
	require.Equal(t, http.StatusOK, getJSON(t, srv.URL+"/jobs", &list))
	require.Equal(t, jobs.Address(), list.Board)
	require.Len(t, list.Jobs, 1)
	require.Equal(t, "00000000000000a1", list.Jobs[0].ID)
	require.Equal(t, "Translate a README", list.Jobs[0].Title)
	require.True(t, list.Jobs[0].SpecAvailable)
	require.Equal(t, http.StatusOK, getJSON(t, srv.URL+"/jobs?q=spanish+readme", &list))
	require.Len(t, list.Jobs, 0, "the description isn't searched, only the title and tags")
	require.Equal(t, http.StatusOK, getJSON(t, srv.URL+"/jobs?q=translate", &list))
	require.Len(t, list.Jobs, 1)

	// Hired and delivered: the work is stored only from the hired seller.
	chain.mu.Lock()
	chain.escrows = append(chain.escrows, wallet.EscrowCreated{ID: 4, Payer: "aether1buyer", Payee: "aether1seller", Amount: "1000000uaeth",
		OnExpiry: "ON_EXPIRY_RELEASE", Terms: jobs.Terms("00000000000000a1"), Height: 12})
	chain.mu.Unlock()
	work := []byte("# Léame\n")
	impostor := []byte("not the seller's work")
	chain.add("B"+strings.Repeat("0", 63), 13, "aether1other", jobs.DeliverMemo("00000000000000a1", jobs.Hash(impostor)))
	chain.add("C"+strings.Repeat("0", 63), 14, "aether1seller", jobs.DeliverMemo("00000000000000a1", jobs.Hash(work)))
	code, _ = put(t, srv.URL, impostor)
	require.Equal(t, http.StatusConflict, code)
	code, _ = put(t, srv.URL, work)
	require.Equal(t, http.StatusCreated, code)

	var job jobDTO
	require.Equal(t, http.StatusOK, getJSON(t, srv.URL+"/jobs/00000000000000a1", &job))
	require.Equal(t, jobs.StateDelivered, job.State)
	require.Equal(t, "English to Spanish.", job.Spec.Description)
	require.Equal(t, "aether1seller", job.Hire.Seller)
	require.Equal(t, jobs.EscrowOpen, job.Escrow.Status)
	require.True(t, *job.Protected)
	require.Len(t, job.Deliveries, 1)
	require.True(t, job.Deliveries[0].Available)

	res, err = http.Get(srv.URL + job.Deliveries[0].URL)
	require.NoError(t, err)
	var got bytes.Buffer
	got.ReadFrom(res.Body)
	res.Body.Close()
	require.Equal(t, work, got.Bytes())
	require.Equal(t, "text/plain; charset=utf-8", res.Header.Get("Content-Type"))
	require.Equal(t, "nosniff", res.Header.Get("X-Content-Type-Options"))
	require.Contains(t, res.Header.Get("Content-Security-Policy"), "sandbox")

	var e map[string]string
	require.Equal(t, http.StatusNotFound, getJSON(t, srv.URL+"/jobs/00000000000000ff", &e))
	require.Equal(t, http.StatusBadRequest, getJSON(t, srv.URL+"/jobs/nope", &e))
	require.Equal(t, http.StatusBadRequest, getJSON(t, srv.URL+"/jobs?state=weird", &e))
	require.Equal(t, http.StatusNotFound, getJSON(t, srv.URL+"/jobs/blobs/"+strings.Repeat("0", 64), &e))
}

func TestStoreQuota(t *testing.T) {
	st, err := openStore(t.TempDir(), 10)
	require.NoError(t, err)
	put := func(text string, goesFirst func(string) bool) error {
		_, err := st.put(jobs.Hash([]byte(text)), []byte(text), goesFirst)
		return err
	}
	var evicted []string
	st.onEvict = func(h string) { evicted = append(evicted, h) }

	require.NoError(t, put("1234", nil))
	time.Sleep(10 * time.Millisecond)
	require.NoError(t, put("5678", nil))
	require.ErrorIs(t, put("this is longer than ten", nil), errFull, "bigger than the whole store")

	// Full: the oldest goes to make room.
	require.NoError(t, put("abc", nil))
	require.Equal(t, []string{jobs.Hash([]byte("1234"))}, evicted)
	require.False(t, st.has(jobs.Hash([]byte("1234"))))
	require.Equal(t, int64(7), st.used)

	// What the caller marks goes first, even if newer.
	evicted = nil
	abc := jobs.Hash([]byte("abc"))
	require.NoError(t, put("wxyz", func(h string) bool { return h == abc }))
	require.Equal(t, []string{abc}, evicted)
	require.True(t, st.has(jobs.Hash([]byte("5678"))))

	// A deleted text can be stored again.
	time.Sleep(10 * time.Millisecond)
	require.NoError(t, put("1234", nil))
	require.True(t, st.has(jobs.Hash([]byte("1234"))))

	// A reopened store counts what's there, and keeps making room.
	again, err := openStore(st.dir, 10)
	require.NoError(t, err)
	require.Equal(t, st.used, again.used)
	_, err = again.put(jobs.Hash([]byte("zz")), []byte("zz"), nil)
	require.NoError(t, err)
	require.LessOrEqual(t, again.used, int64(10))
}

func TestMakeRoomFrom(t *testing.T) {
	board := []jobs.Job{
		{SpecHash: "open-spec", State: jobs.StateOpen},
		{SpecHash: "hired-spec", State: jobs.StateHired, Deliveries: []jobs.Delivery{{Hash: "delivery"}}},
		{SpecHash: "withdrawn-spec", State: jobs.StateClosed},
	}
	first := makeRoomFrom(board)
	require.False(t, first("open-spec"))
	require.False(t, first("hired-spec"))
	require.False(t, first("delivery"))
	require.True(t, first("withdrawn-spec"))
	require.True(t, first("named-by-no-job"))
}
