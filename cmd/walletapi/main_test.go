package main

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/wallet"
)

func TestParseUaeth(t *testing.T) {
	for in, want := range map[string]int64{"1": 1, "1500000": 1_500_000, "010": 10, "0100000": 100_000} {
		got, err := parseUaeth(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got.Int64(), "%q must be read as base 10", in)
	}
	for _, in := range []string{"", "0", "-5", "0x10", "0b1", "0o7", "1_000", "1.5", "1e6", " 5", "abc"} {
		_, err := parseUaeth(in)
		require.Error(t, err, in)
	}
}

func TestAPIRequiresTokenAndLocalHost(t *testing.T) {
	apiToken, port = "s3cret", "8090"
	h := withCORS(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	for _, c := range []struct {
		name, host, token, method string
		want                      int
	}{
		{"no token", "localhost:8090", "", "GET", http.StatusUnauthorized},
		{"wrong token", "localhost:8090", "nope", "POST", http.StatusUnauthorized},
		{"token prefix", "localhost:8090", "s3cre", "GET", http.StatusUnauthorized},
		{"right token", "localhost:8090", "s3cret", "POST", http.StatusTeapot},
		{"loopback IP", "127.0.0.1:8090", "s3cret", "GET", http.StatusTeapot},
		{"DNS rebinding", "evil.example:8090", "s3cret", "GET", http.StatusForbidden},
		{"other port", "localhost:9999", "s3cret", "GET", http.StatusForbidden},
		{"preflight needs no token", "localhost:8090", "", "OPTIONS", http.StatusOK},
	} {
		req := httptest.NewRequest(c.method, "http://"+c.host+"/api/send", nil)
		if c.token != "" {
			req.Header.Set(TokenHeader, c.token)
		}
		rec := httptest.NewRecorder()
		h(rec, req)
		require.Equal(t, c.want, rec.Code, c.name)
	}
}

func TestGrantRequestValidation(t *testing.T) {
	apiToken, port = "s3cret", "8090"
	agent := sdk.AccAddress("agent_______________").String()
	for body, want := range map[string]string{
		`{"from":"x","grantee":"` + agent + `","limit":"0","expiresAt":` + future + `}`:                   "limit",
		`{"from":"x","grantee":"` + agent + `","limit":"010","expiresAt":1}`:                              "expiresAt",
		`{"from":"x","grantee":"` + agent + `","limit":"5","expiresAt":` + tooFar + `}`:                   "expiresAt",
		`{"from":"x","grantee":"` + agent + `","limit":"5","expiresAt":` + future + `,"feeLimit":"0x10"}`: "feeLimit",
		`not json`: "invalid request body",
	} {
		rec := httptest.NewRecorder()
		handleGrants(rec, httptest.NewRequest("POST", "/api/grants", strings.NewReader(body)))
		require.Equal(t, http.StatusBadRequest, rec.Code, body)
		require.Contains(t, rec.Body.String(), want, body)
	}
}

var (
	future = strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	tooFar = strconv.FormatInt(time.Now().Add(400*24*time.Hour).Unix(), 10)
)

func TestMergePermissions(t *testing.T) {
	a, b := "aether1a", "aether1b"
	out := wallet.MergePermissions(
		[]wallet.Grant{{Grantee: b, Kind: "send"}, {Grantee: a, Kind: "/cosmos.gov.v1.MsgVote"}},
		[]wallet.FeeAllowance{{Grantee: b, Kind: "basic"}, {Grantee: a, Kind: "basic"}},
		func(g wallet.Grant) string { return g.Grantee }, func(f wallet.FeeAllowance) string { return f.Grantee })
	require.Len(t, out, 2)
	require.Equal(t, a, out[0].Account)
	require.Nil(t, out[0].Send)
	require.Len(t, out[0].Other, 1)
	require.NotNil(t, out[0].Fees)
	require.Equal(t, b, out[1].Account)
	require.NotNil(t, out[1].Send)
	require.NotNil(t, out[1].Fees)
}
