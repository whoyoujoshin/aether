package main

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/miner"
)

func TestMineStartValidation(t *testing.T) {
	keyringDir, keyringBackend = t.TempDir(), "test"
	for body, want := range map[string]string{
		`{"name":"x","threads":0}`:                              "threads must be 1 to",
		`{"name":"x","threads":` + strings.Repeat("9", 4) + `}`: "threads must be 1 to",
		`{"name":"nobody","threads":1}`:                         `nobody.info: key not found`,
		`not json`:                                              "invalid request body",
	} {
		rec := httptest.NewRecorder()
		handleMineStart(rec, httptest.NewRequest(http.MethodPost, "/api/mine/start", strings.NewReader(body)))
		require.Contains(t, rec.Body.String(), want, body)
		require.NotEqual(t, http.StatusOK, rec.Code, body)
	}
	rec := httptest.NewRecorder()
	handleMineStart(rec, httptest.NewRequest(http.MethodGet, "/api/mine/start", nil))
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestMineStatusIdle(t *testing.T) {
	mineCur = nil
	rec := httptest.NewRecorder()
	handleMineStatus(rec, httptest.NewRequest(http.MethodGet, "/api/mine/status", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"running":false`)
	require.Contains(t, rec.Body.String(), `"submissions":[]`)
	require.Contains(t, rec.Body.String(), `"maxThreads":`+strconv.Itoa(runtime.NumCPU()))
}

// A find made while another is on its way waits, and a newer one replaces
// it: the chain takes one native submission a block.
func TestMineQueueKeepsTheNewestFind(t *testing.T) {
	s := &mineSession{sending: true} // one already on its way
	s.enqueue(miner.Header{Height: 10, Nonce: 1})
	s.enqueue(miner.Header{Height: 11, Nonce: 2})
	require.Equal(t, 2, s.finds)
	require.Equal(t, uint64(2), s.queuedH.Nonce)
	require.Len(t, s.submissions, 2)
	require.Equal(t, "waiting", s.submissions[0].Status)
	require.Equal(t, "dropped", s.submissions[1].Status)
}

func TestRefusalReason(t *testing.T) {
	require.Equal(t, "another miner's find took this block", refusalReason("a PoW submission has already been accepted at height 7; try again next block"))
	require.Equal(t, "too late: the block it was mined on is too old", refusalReason("claimed height 3 is more than 60 blocks behind current height 90"))
	require.Equal(t, "out of gas", refusalReason("out of gas"))
}
