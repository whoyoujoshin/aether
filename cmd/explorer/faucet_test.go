package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFaucetProxyForwardsOnlyThePagesEndpoints(t *testing.T) {
	var got []string
	var xff string
	faucet := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.RequestURI())
		xff = r.Header.Get("X-Forwarded-For")
		body, _ := io.ReadAll(r.Body)
		w.Write(append([]byte("ok:"), body...))
	}))
	defer faucet.Close()
	faucetAPI = faucet.URL
	defer func() { faucetAPI = "" }()
	mux := http.NewServeMux()
	require.NoError(t, registerFaucetProxy(mux))

	call := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.RemoteAddr = "203.0.113.7:4444"
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	require.Equal(t, "ok:", call(http.MethodGet, "/api/faucet/stats", "").Body.String())
	require.Equal(t, "ok:", call(http.MethodGet, "/api/faucet/challenge?address=aether1x", "").Body.String())
	require.Equal(t, `ok:{"address":"aether1x"}`, call(http.MethodPost, "/api/faucet/request", `{"address":"aether1x"}`).Body.String())
	require.Equal(t, "203.0.113.7", xff, "the visitor's address goes on to the faucet")

	require.Equal(t, http.StatusNotFound, call(http.MethodPost, "/api/faucet/agents", "{}").Code, "registration isn't the page's to proxy")
	require.NotEqual(t, http.StatusOK, call(http.MethodGet, "/api/faucet/../../etc", "").Code, "the mux cleans the path; nothing goes to the faucet")
	require.Equal(t, http.StatusMethodNotAllowed, call(http.MethodGet, "/api/faucet/request", "").Code)
	require.Equal(t, []string{"GET /stats", "GET /challenge?address=aether1x", "POST /request"}, got)
}

func TestFaucetUnavailableWithoutFaucetAPI(t *testing.T) {
	mux := http.NewServeMux()
	require.NoError(t, registerFaucetProxy(mux))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/faucet/stats", nil))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Contains(t, rec.Body.String(), "faucet_unavailable")
}

func TestNodeLocationsRefuseWhatShouldntBePublished(t *testing.T) {
	list, err := parseNodeLocations([]byte(`[
		{"address":"aether1seed","label":"seed","city":"New York","country":"US","region":"N. America","lat":40.71,"lon":-74.01},
		{"address":"aether1home","label":"peer-1","city":"Somewhere","country":"US","region":"N. America","lat":39.8,"lon":-98.6,"precision":"country"}
	]`))
	require.NoError(t, err)
	require.Equal(t, "city", list[0].Precision)
	require.Equal(t, "New York", list[0].City)
	require.Equal(t, "", list[1].City, "a country-only entry never shows its city")

	for _, bad := range []string{
		`[{"address":"aether1a","country":"US","region":"x","lat":1,"lon":1,"ip":"1.2.3.4"}]`,
		`[{"address":"cosmos1a","country":"US","region":"x","lat":1,"lon":1}]`,
		`[{"address":"aether1a","country":"US","lat":1,"lon":1}]`,
		`[{"address":"aether1a","country":"US","region":"x","lat":100,"lon":1}]`,
		`[{"address":"aether1a","country":"US","region":"x","lat":1,"lon":1,"precision":"street"}]`,
	} {
		_, err := parseNodeLocations([]byte(bad))
		require.Error(t, err, bad)
	}
}
