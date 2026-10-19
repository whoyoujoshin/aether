package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/wallet"
)

// fakeOpenRouter answers /models and /chat/completions like OpenRouter.
type fakeOpenRouter struct {
	mu        sync.Mutex
	prompt    string // dollars per input token
	reply     string
	cost      float64
	status    int // of /chat/completions; 0: 200
	calls     int
	modelHits int
	last      map[string]any
	auth      string
}

func (f *fakeOpenRouter) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/models":
			f.modelHits++
			prompt := f.prompt
			if prompt == "" {
				prompt = "0.0000001"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{
				map[string]any{"id": "other/model", "pricing": map[string]string{"prompt": "1", "completion": "1"}},
				map[string]any{"id": "openai/gpt-oss-120b", "pricing": map[string]string{"prompt": prompt, "completion": "0.0000005"}},
			}})
		case "/chat/completions":
			f.calls++
			f.auth = r.Header.Get("Authorization")
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &f.last)
			if f.status != 0 {
				w.WriteHeader(f.status)
				_, _ = w.Write([]byte(`{"error":{"message":"insufficient credits"}}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"model":   "openai/gpt-oss-120b",
				"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": f.reply}, "finish_reason": "stop"}},
				"usage":   map[string]any{"prompt_tokens": 20, "completion_tokens": 30, "cost": f.cost},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

type inferenceFixture struct {
	srv  *httptest.Server // behind the paywall's path
	or   *fakeOpenRouter
	inf  *inferenceService
	now  *time.Time
	call func(payer, body string) (int, map[string]any, http.Header)
}

func newInferenceFixture(t *testing.T, cfg inferenceConfig) *inferenceFixture {
	t.Helper()
	or := &fakeOpenRouter{reply: "Escrow, receipts and reputation.", cost: 0.0000173}
	orSrv := or.server(t)
	now := time.Date(2026, 10, 19, 23, 0, 0, 0, time.UTC)
	cfg.Key, cfg.BaseURL, cfg.Model = "sk-or-test-secret", orSrv.URL, "openai/gpt-oss-120b"
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 1024
	}
	if cfg.MaxInputChars == 0 {
		cfg.MaxInputChars = 16000
	}
	if cfg.MaxCallPico == 0 {
		cfg.MaxCallPico = 2_000_000_000 // $0.002
	}
	if cfg.DailyPico == 0 {
		cfg.DailyPico = picoPerUSD // $1
	}
	if cfg.PayerDailyCalls == 0 {
		cfg.PayerDailyCalls = 20
	}
	cfg.ReasoningEffort = "low"
	inf := newInferenceService(cfg, func() time.Time { return now })
	assets, err := wallet.NewAssetsFor(wallet.TestnetUSDC)
	require.NoError(t, err)
	s := &server{chain: &fakeChain{}, assets: assets, now: func() time.Time { return now }, inference: inf}
	srv := httptest.NewServer(s.routes())
	t.Cleanup(srv.Close)
	p := behindPaywall(t, srv, "inference")
	f := &inferenceFixture{srv: p, or: or, inf: inf, now: &now}
	f.call = func(payer, body string) (int, map[string]any, http.Header) {
		req, err := http.NewRequest("POST", p.URL+"/", strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("X-Aether-Payer", payer)
		req.Header.Set("X-Aether-Payment-Tx", "TX1")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		var out map[string]any
		raw, _ := io.ReadAll(resp.Body)
		require.NoError(t, json.Unmarshal(raw, &out), string(raw))
		return resp.StatusCode, out, resp.Header
	}
	return f
}

func TestInference_Serves(t *testing.T) {
	f := newInferenceFixture(t, inferenceConfig{})
	code, out, _ := f.call("aether1payer", `{"prompt": "Name three uses of a blockchain for AI agents."}`)
	require.Equal(t, http.StatusOK, code, out)
	require.Equal(t, "Escrow, receipts and reputation.", out["content"])
	require.Equal(t, "stop", out["finishReason"])
	require.Equal(t, "aether1payer", out["payer"])
	require.Equal(t, "TX1", out["paymentTx"])
	require.Contains(t, out["note"], "untrusted")
	usage := out["usage"].(map[string]any)
	require.EqualValues(t, 20, usage["promptTokens"])
	require.Equal(t, "0.0000173", usage["costUSD"], "OpenRouter's own cost when it gives one")

	require.Equal(t, "Bearer sk-or-test-secret", f.or.auth)
	require.Equal(t, "openai/gpt-oss-120b", f.or.last["model"])
	require.EqualValues(t, 1024, f.or.last["max_tokens"])
	require.Equal(t, map[string]any{"effort": "low"}, f.or.last["reasoning"])
	msgs := f.or.last["messages"].([]any)
	require.Equal(t, map[string]any{"role": "user", "content": "Name three uses of a blockchain for AI agents."}, msgs[0])
	require.Equal(t, int64(17_300_000), f.inf.spentPico, "today's spend is the call's real cost")

	// maxTokens may lower the cap, never raise it; prices come from the cache.
	code, _, _ = f.call("aether1payer", `{"messages": [{"role": "system", "content": "Be brief."}, {"role": "user", "content": "Hi"}], "maxTokens": 99999}`)
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 1024, f.or.last["max_tokens"])
	code, _, _ = f.call("aether1payer", `{"prompt": "Hi", "maxTokens": 50}`)
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 50, f.or.last["max_tokens"])
	require.Equal(t, 1, f.or.modelHits)
}

func TestInference_MalformedRequestsAre4xx(t *testing.T) {
	f := newInferenceFixture(t, inferenceConfig{MaxInputChars: 100})
	for body, want := range map[string]string{
		`not json`: "JSON",
		`{}`:       "nothing to answer",
		`{"prompt": "a", "messages": [{"role": "user", "content": "b"}]}`:                         "not both",
		`{"messages": [{"role": "tool", "content": "x"}]}`:                                        "role",
		`{"messages": [{"role": "user", "content": "x"}, {"role": "assistant", "content": "y"}]}`: "last message",
		`{"prompt": "` + strings.Repeat("x", 101) + `"}`:                                          "at most 100",
		`{"prompt": "x", "temperature": 3}`:                                                       "temperature",
	} {
		code, out, _ := f.call("aether1p", body)
		require.Equal(t, http.StatusBadRequest, code, body)
		require.Contains(t, out["error"], want, body)
	}
	req, _ := http.NewRequest("GET", f.srv.URL+"/", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	require.Zero(t, f.or.calls, "a malformed request never reaches the model")
}

func TestInference_RefusalsAre5xxSoThePaymentIsReleased(t *testing.T) {
	// The payer's daily calls.
	f := newInferenceFixture(t, inferenceConfig{PayerDailyCalls: 2})
	for i := 0; i < 2; i++ {
		code, _, _ := f.call("aether1a", `{"prompt": "hi"}`)
		require.Equal(t, http.StatusOK, code)
	}
	code, out, h := f.call("aether1a", `{"prompt": "hi"}`)
	require.Equal(t, http.StatusServiceUnavailable, code)
	require.Contains(t, out["error"], "2 calls for today")
	require.Contains(t, out["error"], "payment can be used again")
	require.Equal(t, "3601", h.Get("Retry-After"), "until midnight UTC")
	code, _, _ = f.call("aether1b", `{"prompt": "hi"}`)
	require.Equal(t, http.StatusOK, code, "another payer still may")
	*f.now = f.now.Add(2 * time.Hour) // the next UTC day
	code, _, _ = f.call("aether1a", `{"prompt": "hi"}`)
	require.Equal(t, http.StatusOK, code, "a new day")

	// The daily budget: each call reserves its worst case (here about
	// 513 million picodollars: 1024 reply tokens at $0.0000005) before
	// it runs, and keeps only what it cost. One worst case fits in 520
	// million; after one call has cost 17.3 million, the next doesn't.
	f = newInferenceFixture(t, inferenceConfig{DailyPico: 520_000_000, MaxCallPico: 520_000_000})
	code, _, _ = f.call("aether1a", `{"prompt": "hi"}`)
	require.Equal(t, http.StatusOK, code)
	code, out, _ = f.call("aether1b", `{"prompt": "hi"}`)
	require.Equal(t, http.StatusServiceUnavailable, code)
	require.Contains(t, out["error"], "budget is spent")
	require.Equal(t, 1, f.or.calls)

	// A model dearer than the per-call limit.
	f = newInferenceFixture(t, inferenceConfig{})
	f.or.prompt = "0.001"
	code, out, _ = f.call("aether1a", `{"prompt": "hi"}`)
	require.Equal(t, http.StatusServiceUnavailable, code)
	require.Contains(t, out["error"], "costs more")
	require.Zero(t, f.or.calls)

	// The upstream failing: 502, and the reservation and call released.
	f = newInferenceFixture(t, inferenceConfig{PayerDailyCalls: 1})
	f.or.status = http.StatusPaymentRequired
	code, out, _ = f.call("aether1a", `{"prompt": "hi"}`)
	require.Equal(t, http.StatusBadGateway, code)
	require.NotContains(t, out["error"], "credits", "the upstream's error stays in the log")
	require.Zero(t, f.inf.spentPico)
	f.or.status = 0
	code, _, _ = f.call("aether1a", `{"prompt": "hi"}`)
	require.Equal(t, http.StatusOK, code, "the failed call didn't use the payer's one call")
}

func TestInference_HelpIsLiveAndNeverShowsTheKey(t *testing.T) {
	f := newInferenceFixture(t, inferenceConfig{PayerDailyCalls: 7})
	code, out := getJSON(t, "GET", f.srv.URL+"/help", "", nil)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "Aether LLM inference", out["name"])
	require.Equal(t, "POST", out["method"])
	limits := out["limits"].(map[string]any)
	require.Equal(t, "openai/gpt-oss-120b", limits["model"])
	require.EqualValues(t, 7, limits["callsPerPayerDaily"])
	require.Equal(t, true, limits["availableToday"])
	require.Equal(t, "2026-10-20T00:00:00Z", limits["resetsAt"])
	raw, _ := json.Marshal(out)
	require.NotContains(t, string(raw), "sk-or-test-secret")

	f.inf.spentPico = f.inf.cfg.DailyPico
	f.inf.day = "2026-10-19"
	_, out = getJSON(t, "GET", f.srv.URL+"/help", "", nil)
	require.Equal(t, false, out["limits"].(map[string]any)["availableToday"])
}

func TestInference_OffWithoutAKey(t *testing.T) {
	srv := newTestServer(t, &fakeChain{})
	resp, err := http.Get(srv.URL + "/inference/help")
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestInferenceFromFlags(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "key")
	require.NoError(t, os.WriteFile(key, []byte(" sk-or-x \n"), 0o600))
	cfg, err := inferenceFromFlags(key, "https://openrouter.ai/api/v1/", "m", 1024, 16000, "0.002", "1", 20, "low")
	require.NoError(t, err)
	require.Equal(t, "sk-or-x", cfg.Key)
	require.Equal(t, "https://openrouter.ai/api/v1", cfg.BaseURL)
	require.Equal(t, int64(2_000_000_000), cfg.MaxCallPico)
	require.Equal(t, int64(picoPerUSD), cfg.DailyPico)

	empty := filepath.Join(dir, "empty")
	require.NoError(t, os.WriteFile(empty, []byte("\n"), 0o600))
	_, err = inferenceFromFlags(empty, "u", "m", 1, 1, "0.002", "1", 1, "")
	require.ErrorContains(t, err, "empty")
	_, err = inferenceFromFlags(key, "u", "m", 1, 1, "0.002", "0.001", 1, "")
	require.ErrorContains(t, err, "daily")
	_, err = inferenceFromFlags(key, "u", "m", 1, 1, "free", "1", 1, "")
	require.ErrorContains(t, err, "max-call")
}

func TestUsdToPico(t *testing.T) {
	for in, want := range map[string]int64{"0.0000001": 100_000, "0.0000005": 500_000, "1": picoPerUSD, "0.0000000000001": 1, "0": 0} {
		got, err := usdToPico(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got, in)
	}
	_, err := usdToPico("abc")
	require.Error(t, err)
	require.Equal(t, "0.0000173", picoToUSD(17_300_000))
	require.Equal(t, "0", picoToUSD(0))
}
