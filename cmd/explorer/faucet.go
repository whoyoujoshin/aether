package main

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// faucetAPI is the faucet (cmd/faucet) the Faucet page talks to, through
// /api/faucet/, so the page stays same-origin and HTTPS whatever the
// faucet listens on. Empty: the page shows the faucet as unavailable.
var faucetAPI string

// faucetPaths are the faucet endpoints the page uses, and the methods
// each takes. Nothing else is forwarded.
var faucetPaths = map[string]string{
	"":          http.MethodGet, // what it gives and its limits
	"stats":     http.MethodGet,
	"drips":     http.MethodGet,
	"challenge": http.MethodGet,
	"status":    http.MethodGet,
	"request":   http.MethodPost,
}

// faucetProxy forwards /api/faucet/<endpoint> to target/<endpoint>. Like
// any reverse proxy it appends the caller's address to X-Forwarded-For,
// so a faucet that trusts this explorer (its --trusted-proxies) limits
// and tags the real visitor, not the explorer.
func faucetProxy(target string) (http.Handler, error) {
	u, err := url.Parse(strings.TrimRight(target, "/"))
	if err != nil {
		return nil, err
	}
	rp := httputil.NewSingleHostReverseProxy(u)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		endpoint := strings.TrimPrefix(r.URL.Path, "/api/faucet")
		endpoint = strings.Trim(endpoint, "/")
		method, ok := faucetPaths[endpoint]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if r.Method != method {
			w.Header().Set("Allow", method)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		r.URL.Path = "/" + endpoint
		r.URL.RawPath = ""
		r.Host = u.Host
		rp.ServeHTTP(w, r)
	}), nil
}

// registerFaucetProxy mounts /api/faucet/. It sits outside apiRoutes on
// purpose: these are the faucet's own endpoints, documented by the
// faucet (GET / on it), passed through unchanged, not explorer API.
func registerFaucetProxy(mux *http.ServeMux) error {
	if faucetAPI == "" {
		mux.HandleFunc("/api/faucet/", handleFaucetUnavailable)
		mux.HandleFunc("/api/faucet", handleFaucetUnavailable)
		return nil
	}
	fp, err := faucetProxy(faucetAPI)
	if err != nil {
		return err
	}
	mux.Handle("/api/faucet/", fp)
	mux.Handle("/api/faucet", fp)
	return nil
}

// handleFaucetUnavailable answers /api/faucet/ when no --faucet-api is set.
func handleFaucetUnavailable(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{
		"code":    "faucet_unavailable",
		"message": "this explorer isn't connected to a faucet (--faucet-api)",
	})
}
