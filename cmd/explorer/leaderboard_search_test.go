package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/whoyoujoshin/aether/x/pow"
)

func TestLeaderboardDTO_AlwaysHasEntries(t *testing.T) {
	for _, tc := range []struct {
		in   *pow.QueryMinerLeaderboardResponse
		want string
	}{
		{&pow.QueryMinerLeaderboardResponse{Epoch: 78}, `{"epoch":78,"entries":[]}`},
		{&pow.QueryMinerLeaderboardResponse{Epoch: 5, Entries: []*pow.MinerLeaderboardEntry{{Address: "aether1x", Work: 0}}},
			`{"epoch":5,"entries":[{"address":"aether1x","work":0}]}`},
	} {
		got, err := json.Marshal(toLeaderboardDTO(tc.in))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != tc.want {
			t.Errorf("got %s, want %s", got, tc.want)
		}
	}
}

func TestSearch_Kinds(t *testing.T) {
	hash := strings.Repeat("ab", 32)
	for q, want := range map[string]string{
		"140":        `{"kind":"block","value":"140"}`,
		" 00140 ":    `{"kind":"block","value":"140"}`,
		"aether1abc": `{"kind":"address","value":"aether1abc"}`,
		hash:         `{"kind":"tx","value":"` + hash + `"}`,
		"0":          `{"kind":"address","value":"0"}`,
		"-5":         `{"kind":"address","value":"-5"}`,
	} {
		rec := httptest.NewRecorder()
		handleSearch(rec, httptest.NewRequest("GET", "/api/search?q="+strings.ReplaceAll(q, " ", "%20"), nil))
		if got := strings.TrimSpace(rec.Body.String()); got != want {
			t.Errorf("search %q: got %s, want %s", q, got, want)
		}
	}
}
