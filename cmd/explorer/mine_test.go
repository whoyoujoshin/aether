package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/miner"
	pow "github.com/whoyoujoshin/aether/x/pow"
)

func encodeTx(t *testing.T, msgs ...sdk.Msg) []byte {
	t.Helper()
	b := encoding().TxConfig.NewTxBuilder()
	require.NoError(t, b.SetMsgs(msgs...))
	bz, err := encoding().TxConfig.TxEncoder()(b.GetTx())
	require.NoError(t, err)
	return bz
}

func TestOnlyMiningSubmission(t *testing.T) {
	addr := sdk.AccAddress(bytes.Repeat([]byte{1}, 32)).String()
	native := &pow.MsgSubmitPoW{Miner: addr, Submission: &pow.MsgSubmitPoW_Native{Native: &pow.NativeSubmission{Height: 5, Difficulty: 1}}}
	require.NoError(t, onlyMiningSubmission(encodeTx(t, native)))

	send := &banktypes.MsgSend{FromAddress: addr, ToAddress: addr, Amount: sdk.NewCoins(sdk.NewInt64Coin("uaeth", 1))}
	require.ErrorContains(t, onlyMiningSubmission(encodeTx(t, send)), "only /aether.pow.v1.MsgSubmitPoW")
	require.ErrorContains(t, onlyMiningSubmission(encodeTx(t, native, send)), "one message, not 2")
	aux := &pow.MsgSubmitPoW{Miner: addr, Submission: &pow.MsgSubmitPoW_AuxPow{AuxPow: &pow.AuxPowData{}}}
	require.ErrorContains(t, onlyMiningSubmission(encodeTx(t, aux)), "only native")
	require.Error(t, onlyMiningSubmission([]byte("not a tx")))
}

func TestMineSubmitRefusesBeforeBroadcasting(t *testing.T) {
	mineLimiter = &windowLimiter{seen: map[string]*window{}}
	addr := sdk.AccAddress(bytes.Repeat([]byte{1}, 32)).String()
	send := &banktypes.MsgSend{FromAddress: addr, ToAddress: addr, Amount: sdk.NewCoins(sdk.NewInt64Coin("uaeth", 1))}
	post := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/mine/submit", strings.NewReader(body))
		req.RemoteAddr = "203.0.113.9:5555"
		handleMineSubmit(rec, req)
		return rec
	}
	rec := post(`{"tx":"` + base64.StdEncoding.EncodeToString(encodeTx(t, send)) + `"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "only /aether.pow.v1.MsgSubmitPoW")
	require.Equal(t, http.StatusBadRequest, post(`{"tx":"!!"}`).Code)

	for i := 0; i < 4; i++ {
		post(`{}`)
	}
	require.Equal(t, http.StatusTooManyRequests, post(`{}`).Code, "six a minute per visitor")

	rec = httptest.NewRecorder()
	handleMineSubmit(rec, httptest.NewRequest(http.MethodGet, "/api/mine/submit", nil))
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestWindowLimiter(t *testing.T) {
	l := &windowLimiter{seen: map[string]*window{}}
	for i := 0; i < 3; i++ {
		require.True(t, l.allow("a", 3, 50*time.Millisecond))
	}
	require.False(t, l.allow("a", 3, 50*time.Millisecond))
	require.True(t, l.allow("b", 3, 50*time.Millisecond), "per key")
	time.Sleep(60 * time.Millisecond)
	require.True(t, l.allow("a", 3, 50*time.Millisecond), "a new window")
}

func TestCallerIP(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "203.0.113.9:1234"
	r.Header.Set("X-Forwarded-For", "10.0.0.1")
	require.Equal(t, "203.0.113.9", callerIP(r), "only a local proxy's header counts")
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-Forwarded-For", "198.51.100.7, 192.0.2.4")
	require.Equal(t, "192.0.2.4", callerIP(r), "the entry the proxy added")
}

// Every miner asking at once is one read of the node.
func TestMineWorkCached(t *testing.T) {
	saved := mineWork
	defer func() { mineWork = saved }()
	reads := 0
	mineWork = &miner.Cache{TTL: time.Minute, Read: func(context.Context) (miner.Work, error) {
		reads++
		return miner.Work{ChainID: "aether-testnet-1", Height: 77, Hash: bytes.Repeat([]byte{0xcd}, 32), Difficulty: 355_000, RecencyWindow: 60}, nil
	}}
	for i := 0; i < 100; i++ {
		rec := httptest.NewRecorder()
		handleMineWork(rec, httptest.NewRequest(http.MethodGet, "/api/mine/work", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		var got mineWorkDTO
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		require.Equal(t, "355000", got.Difficulty)
		require.Equal(t, strings.Repeat("cd", 32), got.Hash)
		require.Len(t, got.Target, 64)
		require.Equal(t, "public, max-age=3", rec.Header().Get("Cache-Control"))
	}
	require.Equal(t, 1, reads)

	mineWork = &miner.Cache{TTL: time.Minute, Read: func(context.Context) (miner.Work, error) { return miner.Work{}, errors.New("down") }}
	rec := httptest.NewRecorder()
	handleMineWork(rec, httptest.NewRequest(http.MethodGet, "/api/mine/work", nil))
	require.Equal(t, http.StatusBadGateway, rec.Code)
}
