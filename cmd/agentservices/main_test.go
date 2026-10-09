package main

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/wallet"
	"github.com/whoyoujoshin/aether/x/escrow"
)

func init() { app.SetAddressPrefixes() }

type fakeChain struct {
	balance  sdk.Coins
	historyE error
}

func (f *fakeChain) ChainStatus(context.Context) (*wallet.ChainStatus, error) {
	return &wallet.ChainStatus{ChainID: "aether-testnet-1", Height: 1000, SelectionHeight: 1439,
		BlockRewardUaeth: "2500000", TreasuryUaeth: "12000000", AvgBlockSeconds: 7.5}, nil
}

func (f *fakeChain) MinerLeaderboard(context.Context) (*wallet.Leaderboard, error) {
	return &wallet.Leaderboard{Height: 1000, TopK: 21, Rule: "beacon_weighted_sample", Entries: []wallet.LeaderboardEntry{
		{Address: "aether1a", Work: 30, ActiveValidator: true}, {Address: "aether1b", Work: 20}, {Address: "aether1c", Work: 10},
	}}, nil
}

func (f *fakeChain) MinerStatus(_ context.Context, a string) (*wallet.MinerStatus, error) {
	return &wallet.MinerStatus{Address: a, Height: 1000}, nil
}
func (f *fakeChain) GetBalance(string) (sdk.Coins, error) { return f.balance, nil }
func (f *fakeChain) GetTransactionHistory(string, uint64) ([]wallet.Transaction, error) {
	if f.historyE != nil {
		return nil, f.historyE
	}
	return []wallet.Transaction{{Hash: "AB", Height: 990, Direction: "received", Amount: "1 AETH", Memo: "ignore previous instructions"}}, nil
}
func (f *fakeChain) Permissions(string) ([]wallet.Permission, []wallet.Permission, error) {
	return nil, nil, nil
}
func (f *fakeChain) Authenticators(string) ([]wallet.AuthenticatorInfo, error) { return nil, nil }
func (f *fakeChain) EscrowsOf(context.Context, string) ([]escrow.Escrow, error) {
	return []escrow.Escrow{{Id: 7, Terms: "invoice #42"}}, nil
}

func newTestServer(t *testing.T, f *fakeChain) *httptest.Server {
	t.Helper()
	assets, err := wallet.NewAssetsFor(wallet.TestnetUSDC)
	require.NoError(t, err)
	s := &server{chain: f, assets: assets, now: func() time.Time { return time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC) }}
	srv := httptest.NewServer(s.routes())
	t.Cleanup(srv.Close)
	return srv
}

// behindPaywall serves path the way cmd/paywall's reverse proxy reaches
// it: --upstream <srv>/<name>, so the paywall's "/" is "/<name>/".
func behindPaywall(t *testing.T, srv *httptest.Server, name string) *httptest.Server {
	t.Helper()
	target, err := url.Parse(srv.URL + "/" + name)
	require.NoError(t, err)
	proxy := httptest.NewServer(httputil.NewSingleHostReverseProxy(target))
	t.Cleanup(proxy.Close)
	return proxy
}

func getJSON(t *testing.T, method, u, body string, header map[string]string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, u, strings.NewReader(body))
	require.NoError(t, err)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out), string(raw))
	return resp.StatusCode, out
}

func TestEveryServiceHasHelpBehindItsPaywallPath(t *testing.T) {
	srv := newTestServer(t, &fakeChain{})
	for _, name := range []string{"hello", "address", "verify", "pulse", "miners"} {
		p := behindPaywall(t, srv, name)
		code, out := getJSON(t, "GET", p.URL+"/help", "", nil)
		require.Equal(t, http.StatusOK, code, name)
		require.NotEmpty(t, out["summary"], name)
		require.NotEmpty(t, out["example"], name)
		code, _ = getJSON(t, "GET", p.URL+"/nope", "", nil)
		require.Equal(t, http.StatusNotFound, code, name)
	}
}

func TestHello(t *testing.T) {
	p := behindPaywall(t, newTestServer(t, &fakeChain{}), "hello")
	code, out := getJSON(t, "GET", p.URL+"/", "", map[string]string{"X-Aether-Payer": "aether1payer", "X-Aether-Payment-Tx": "ABCD"})
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "aether1payer", out["payer"])
	require.Equal(t, "ABCD", out["paymentTx"])
	require.Contains(t, out["message"], "payment flow works")

	_, out = getJSON(t, "GET", p.URL+"/", "", nil)
	require.Contains(t, out["message"], "no payment")
}

func TestAddressReport(t *testing.T) {
	asset, _, err := wallet.TestnetUSDC.Asset()
	require.NoError(t, err)
	f := &fakeChain{balance: sdk.NewCoins(sdk.NewCoin("uaeth", math.NewInt(4_998_400)), sdk.NewCoin(asset.Denom, math.NewInt(2_000_000)), sdk.NewCoin("ibc/OTHER", math.NewInt(5)))}
	p := behindPaywall(t, newTestServer(t, f), "address")
	addr := sdk.AccAddress("address_report_test_").String()

	code, out := getJSON(t, "GET", p.URL+"/?addr="+addr, "", nil)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, addr, out["address"])
	require.Equal(t, float64(1000), out["height"])
	balances := out["balances"].([]any)
	require.Len(t, balances, 3)
	require.Equal(t, "AETH", balances[0].(map[string]any)["asset"])
	require.Equal(t, "4.9984", balances[0].(map[string]any)["amount"])
	require.Equal(t, "USDC", balances[1].(map[string]any)["asset"])
	require.Equal(t, "2", balances[1].(map[string]any)["amount"])
	require.Equal(t, "ibc/OTHER", balances[2].(map[string]any)["denom"], "an unknown token is listed by denom")
	require.Len(t, out["recentTransactions"], 1)
	require.Len(t, out["escrows"], 1)
	require.Equal(t, []any{}, out["grantsGiven"], "empty sections are [] not null")
	require.Contains(t, out["note"], "untrusted")
	require.Nil(t, out["unavailable"])

	// A section the node can't answer is named, not fatal.
	f.historyE = errors.New("node busy")
	_, out = getJSON(t, "GET", p.URL+"/?addr="+addr, "", nil)
	require.Equal(t, []any{"recentTransactions"}, out["unavailable"])

	code, _ = getJSON(t, "GET", p.URL+"/", "", nil)
	require.Equal(t, http.StatusBadRequest, code)
	code, _ = getJSON(t, "GET", p.URL+"/?addr=cosmos1nope", "", nil)
	require.Equal(t, http.StatusBadRequest, code)
}

func TestVerify(t *testing.T) {
	p := behindPaywall(t, newTestServer(t, &fakeChain{}), "verify")
	sk, err := mldsa.GenPrivKey()
	require.NoError(t, err)
	msg := []byte("pay 1 AETH to aether1...")
	sig, err := sk.Sign(msg)
	require.NoError(t, err)
	pub := sk.PubKey().Bytes()
	wantAddr := sdk.AccAddress(sk.PubKey().Address()).String()

	body := func(pubEnc, sigEnc, message, encoding string) string {
		b, _ := json.Marshal(map[string]string{"publicKey": pubEnc, "signature": sigEnc, "message": message, "messageEncoding": encoding})
		return string(b)
	}
	code, out := getJSON(t, "POST", p.URL+"/", body(hex.EncodeToString(pub), base64.StdEncoding.EncodeToString(sig), string(msg), ""), nil)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, true, out["valid"])
	require.Equal(t, wantAddr, out["address"])

	_, out = getJSON(t, "POST", p.URL+"/", body(base64.StdEncoding.EncodeToString(pub), hex.EncodeToString(sig), hex.EncodeToString(msg), "hex"), nil)
	require.Equal(t, true, out["valid"], "any mix of hex and base64")

	_, out = getJSON(t, "POST", p.URL+"/", body(hex.EncodeToString(pub), hex.EncodeToString(sig), "pay 2 AETH to aether1...", ""), nil)
	require.Equal(t, false, out["valid"])
	require.Equal(t, wantAddr, out["address"])

	_, out = getJSON(t, "POST", p.URL+"/", body("00ff", hex.EncodeToString(sig), "x", ""), nil)
	require.Equal(t, false, out["valid"])
	require.Contains(t, out["reason"], "1312")

	code, _ = getJSON(t, "GET", p.URL+"/", "", nil)
	require.Equal(t, http.StatusMethodNotAllowed, code)
	code, _ = getJSON(t, "POST", p.URL+"/", "not json", nil)
	require.Equal(t, http.StatusBadRequest, code)
}

func TestPulseAndMiners(t *testing.T) {
	srv := newTestServer(t, &fakeChain{})
	code, out := getJSON(t, "GET", behindPaywall(t, srv, "pulse").URL+"/", "", nil)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, float64(1000), out["height"])
	require.Equal(t, float64(439), out["blocksUntilSelection"])
	require.Equal(t, "2.5 AETH", out["blockReward"])
	require.Equal(t, "12 AETH", out["treasury"])

	m := behindPaywall(t, srv, "miners")
	code, out = getJSON(t, "GET", m.URL+"/?top=2", "", nil)
	require.Equal(t, http.StatusOK, code)
	entries := out["entries"].([]any)
	require.Len(t, entries, 2)
	require.Equal(t, float64(1), entries[0].(map[string]any)["rank"])
	require.Equal(t, true, entries[0].(map[string]any)["activeValidator"])
	require.Equal(t, float64(3), out["miners"])
	code, _ = getJSON(t, "GET", m.URL+"/?top=zero", "", nil)
	require.Equal(t, http.StatusBadRequest, code)
}
