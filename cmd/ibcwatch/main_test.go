package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWatcherPostsOnlyChanges(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	expiry := now.Add(48 * time.Hour)
	var readErr error
	var posts []string
	w := newWatcher([]target{{"Osmosis's client of Aether", "https://rpc.example", "07-tendermint-5277"}},
		func(target) (time.Time, error) { return expiry, readErr },
		func(s string) error { posts = append(posts, s); return nil },
		24*time.Hour, 2)
	w.now = func() time.Time { return now }

	w.round() // fine at the start: the summary covers it
	if len(posts) != 0 {
		t.Fatalf("posted %q for a fine client at startup", posts)
	}
	if s := w.summary(); !strings.Contains(s, "expires in 48h") {
		t.Fatalf("summary %q", s)
	}

	now = now.Add(30 * time.Hour) // 18h left
	w.round()
	w.round() // still expiring: no repeat
	if len(posts) != 1 || !strings.HasPrefix(posts[0], "⚠️") || !strings.Contains(posts[0], "expires in 18h") {
		t.Fatalf("posts %q", posts)
	}

	now = now.Add(19 * time.Hour)
	w.round()
	if len(posts) != 2 || !strings.HasPrefix(posts[1], "🔴") {
		t.Fatalf("posts %q", posts)
	}

	expiry = now.Add(80 * time.Hour) // replaced or revived
	w.round()
	if len(posts) != 3 || !strings.Contains(posts[2], "fine again; it expires in 3d 8h") {
		t.Fatalf("posts %q", posts)
	}

	readErr = errors.New("HTTP 403")
	w.round() // one failure: not yet
	if len(posts) != 3 {
		t.Fatalf("posted after one failed read: %q", posts[3:])
	}
	w.round()
	if len(posts) != 4 || !strings.HasPrefix(posts[3], "❔") || !strings.Contains(posts[3], "HTTP 403") {
		t.Fatalf("posts %q", posts)
	}

	readErr = nil
	w.round()
	if len(posts) != 5 || !strings.Contains(posts[4], "can be read again") {
		t.Fatalf("posts %q", posts)
	}
}

func TestWatcherReportsABadClientAtStartup(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	var posts []string
	w := newWatcher([]target{{"A", "rpc", "07-tendermint-1"}},
		func(target) (time.Time, error) { return now.Add(-time.Hour), nil },
		func(s string) error { posts = append(posts, s); return nil }, 24*time.Hour, 3)
	w.now = func() time.Time { return now }
	w.round()
	if len(posts) != 1 || !strings.HasPrefix(posts[0], "🔴") {
		t.Fatalf("posts %q", posts)
	}
}

func TestWatchFlag(t *testing.T) {
	var ts targets
	if err := ts.Set("Aether's client of Injective, http://127.0.0.1:26657 ,07-tendermint-2"); err != nil {
		t.Fatal(err)
	}
	if ts[0] != (target{"Aether's client of Injective", "http://127.0.0.1:26657", "07-tendermint-2"}) {
		t.Fatalf("%+v", ts[0])
	}
	for _, bad := range []string{"", "a,b", "a,,c", "a,b,c,d"} {
		if err := ts.Set(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestPostDiscord(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	if err := postDiscord(srv.Client(), srv.URL, "⚠️ hello @everyone"); err != nil {
		t.Fatal(err)
	}
	if got["content"] != "⚠️ hello @everyone" {
		t.Fatalf("content %v", got["content"])
	}
	if am, _ := got["allowed_mentions"].(map[string]any); am == nil || len(am["parse"].([]any)) != 0 {
		t.Fatalf("allowed_mentions %v: an alert must ping nobody", got["allowed_mentions"])
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer bad.Close()
	if err := postDiscord(bad.Client(), bad.URL, "x"); err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("err %v", err)
	}
}
