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
			Scan: func(context.Context) ([]wallet.IncomingPayment, error) {
				c.mu.Lock()
				defer c.mu.Unlock()
				return append([]wallet.IncomingPayment(nil), c.payments...), nil
			},
			Escrows: func(context.Context) ([]wallet.EscrowCreated, error) {
				c.mu.Lock()
				defer c.mu.Unlock()
				return append([]wallet.EscrowCreated(nil), c.escrows...), nil
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
	_, err = st.put(jobs.Hash([]byte("12345678")), []byte("12345678"))
	require.NoError(t, err)
	_, err = st.put(jobs.Hash([]byte("abc")), []byte("abc"))
	require.ErrorIs(t, err, errFull)

	// A reopened store counts what's there.
	again, err := openStore(st.dir, 10)
	require.NoError(t, err)
	require.Equal(t, int64(8), again.used)
}
