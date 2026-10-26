package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPStatusAndValidatorPages(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "aether-chainwatch" {
			t.Errorf("user agent %q", r.Header.Get("User-Agent"))
		}
		fmt.Fprint(w, `{
		  "jsonrpc":"2.0","id":-1,
		  "result":{
		    "node_info":{"network":"aether-testnet-1","moniker":"seed"},
		    "sync_info":{"latest_block_height":"461000","latest_block_time":"2026-10-26T12:00:00.5Z","catching_up":false},
		    "validator_info":{"address":"f94934bdAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}
		  }
		}`)
	})
	mux.HandleFunc("/validators", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "1":
			fmt.Fprint(w, `{"result":{"block_height":"461000","validators":[{"address":"AAAA","voting_power":"10"},{"address":"BBBB","voting_power":"20"}],"count":"2","total":"3"}}`)
		case "2":
			fmt.Fprint(w, `{"result":{"block_height":"461000","validators":[{"address":"CCCC","voting_power":"30"}],"count":"1","total":"3"}}`)
		default:
			http.NotFound(w, r)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newHTTPChecker()
	ctx := context.Background()
	st, err := c.Status(ctx, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if st.Height != 461000 || st.CatchingUp || st.Network != "aether-testnet-1" || st.Validator != "F94934BDAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" {
		t.Fatalf("status = %+v", st)
	}
	if !st.LatestTime.Equal(time.Date(2026, 10, 26, 12, 0, 0, 500000000, time.UTC)) {
		t.Fatalf("time = %s", st.LatestTime)
	}
	vals, height, err := c.Validators(ctx, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if height != 461000 || len(vals) != 3 || vals[2].Power != 30 {
		t.Fatalf("vals = %+v height %d", vals, height)
	}
}

func TestHTTPStatusRejectsEmptyHeight(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/status") {
			fmt.Fprint(w, `{"result":{"sync_info":{"latest_block_height":"","catching_up":true}}}`)
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	c := newHTTPChecker()
	if _, err := c.Status(context.Background(), srv.URL); err == nil {
		t.Fatal("empty height accepted")
	}
}
