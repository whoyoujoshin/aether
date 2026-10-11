package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/stretchr/testify/require"
)

var testTools = []Tool{{Name: "get_balance", Description: "a balance", Schema: map[string]any{
	"type": "object", "properties": map[string]any{"address": map[string]any{"type": "string"}}, "required": []any{"address"},
}}}

// echoCaller records calls and answers each with a fixed result.
type echoCaller struct{ calls []string }

func (e *echoCaller) call(_ context.Context, name string, args json.RawMessage) (string, bool) {
	e.calls = append(e.calls, name+" "+string(args))
	return `{"balance":"5 AETH"}`, false
}

func TestAnthropicAgentRunsTheToolLoop(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/messages", r.URL.Path)
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		require.NoError(t, json.Unmarshal(raw, &body))
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "application/json")
		if len(bodies) == 1 {
			_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","stop_reason":"tool_use",
				"content":[{"type":"text","text":"Checking."},{"type":"tool_use","id":"toolu_1","name":"get_balance","input":{"address":"aether1x"}}],
				"usage":{"input_tokens":100,"output_tokens":20}}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"msg_2","type":"message","role":"assistant","model":"claude-opus-5-5","stop_reason":"end_turn",
			"content":[{"type":"text","text":"It holds 5 AETH."}],"usage":{"input_tokens":150,"output_tokens":10}}`)
	}))
	defer srv.Close()

	a := newAnthropicAgent("claude-opus-5-5", option.WithBaseURL(srv.URL), option.WithAPIKey("test"))
	ec := &echoCaller{}
	answer, u, err := a.Run(context.Background(), "system", "What's the balance of aether1x?", testTools, ec.call, 5)
	require.NoError(t, err)
	require.Equal(t, "It holds 5 AETH.", answer)
	require.Equal(t, Usage{Turns: 2, In: 250, Out: 30}, u)
	require.Equal(t, []string{`get_balance {"address":"aether1x"}`}, ec.calls)

	first := bodies[0]
	require.Equal(t, "claude-opus-5-5", first["model"])
	tool := first["tools"].([]any)[0].(map[string]any)
	require.Equal(t, "get_balance", tool["name"])
	require.Equal(t, []any{"address"}, tool["input_schema"].(map[string]any)["required"])
	// The second request carries the tool result for toolu_1.
	msgs := bodies[1]["messages"].([]any)
	require.Len(t, msgs, 3)
	result := msgs[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	require.Equal(t, "tool_result", result["type"])
	require.Equal(t, "toolu_1", result["tool_use_id"])
}

func TestAnthropicAgentReportsARefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","stop_reason":"refusal",
			"content":[],"usage":{"input_tokens":10,"output_tokens":0}}`)
	}))
	defer srv.Close()
	a := newAnthropicAgent("claude-opus-5-5", option.WithBaseURL(srv.URL), option.WithAPIKey("test"))
	_, _, err := a.Run(context.Background(), "s", "p", testTools, (&echoCaller{}).call, 5)
	require.ErrorContains(t, err, "refusal")
}

func TestABadKeyStopsTheRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
	}))
	defer srv.Close()
	a := newAnthropicAgent("claude-opus-5-5", option.WithBaseURL(srv.URL), option.WithAPIKey("bad"), option.WithMaxRetries(0))
	_, _, err := a.Run(context.Background(), "s", "p", testTools, (&echoCaller{}).call, 5)
	var se setupError
	require.True(t, errors.As(err, &se), "%v", err)

	oa := &openAIAgent{baseURL: srv.URL, model: "m", apiKey: "bad"}
	_, _, err = oa.Run(context.Background(), "s", "p", testTools, (&echoCaller{}).call, 5)
	require.True(t, errors.As(err, &se), "%v", err)
}

func TestEveryToolCarriesASchema(t *testing.T) {
	var body struct {
		Tools []map[string]any `json:"tools"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	tools := append([]Tool{{Name: "create_test_wallet", Description: "no arguments", Schema: map[string]any{"type": "object"}}}, testTools...)
	a := newAnthropicAgent("claude-opus-5-5", option.WithBaseURL(srv.URL), option.WithAPIKey("k"), option.WithMaxRetries(0))
	_, _, _ = a.Run(context.Background(), "s", "p", tools, (&echoCaller{}).call, 1)
	require.Len(t, body.Tools, 2)
	for _, tool := range body.Tools {
		schema, ok := tool["input_schema"].(map[string]any)
		require.True(t, ok, "%s has no input_schema", tool["name"])
		require.Equal(t, "object", schema["type"])
		require.NotNil(t, schema["properties"])
	}
}

func TestARateLimitDoesntStopTheRun(t *testing.T) {
	require.False(t, refusedAtSetup(http.StatusTooManyRequests, 0))
	require.False(t, refusedAtSetup(http.StatusBadRequest, 3)) // mid-task: that task's failure only
	require.True(t, refusedAtSetup(http.StatusNotFound, 0))
}

func TestOpenAIAgentRunsTheToolLoop(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		require.Equal(t, "Bearer k", r.Header.Get("Authorization"))
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		require.NoError(t, json.Unmarshal(raw, &body))
		bodies = append(bodies, body)
		if len(bodies) == 1 {
			_, _ = io.WriteString(w, `{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":null,
				"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_balance","arguments":"{\"address\":\"aether1x\"}"}}]}}],
				"usage":{"prompt_tokens":100,"completion_tokens":20}}`)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"5 AETH."}}],"usage":{"prompt_tokens":120,"completion_tokens":5}}`)
	}))
	defer srv.Close()

	a := &openAIAgent{baseURL: srv.URL + "/v1", model: "gpt-x", apiKey: "k"}
	ec := &echoCaller{}
	answer, u, err := a.Run(context.Background(), "system", "balance?", testTools, ec.call, 5)
	require.NoError(t, err)
	require.Equal(t, "5 AETH.", answer)
	require.Equal(t, Usage{Turns: 2, In: 220, Out: 25}, u)
	require.Equal(t, []string{`get_balance {"address":"aether1x"}`}, ec.calls)
	fn := bodies[0]["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)
	require.Equal(t, "get_balance", fn["name"])
	msgs := bodies[1]["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	require.Equal(t, "tool", last["role"])
	require.Equal(t, "call_1", last["tool_call_id"])
}

func TestReportRanksRunsAndMarksTasks(t *testing.T) {
	out := renderReport([]Run{
		{Label: "b", Results: []Result{{Task: "t1", Points: 0.5}, {Task: "t2"}}},
		{Label: "a", Results: []Result{{Task: "t1", Points: 1, Passed: true, Spent: 1000}, {Task: "t2", Points: 1, Passed: true}}},
	})
	lines := strings.Split(out, "\n")
	require.Contains(t, lines[2], "| a | 2.00 / 2 | ✓ | ✓ |")
	require.Contains(t, lines[3], "| b | 0.50 / 2 | 0.5 | ✗ |")
}

func TestQuestNameFitsTheQuest(t *testing.T) {
	require.Equal(t, "paybench claude-opus-5-5", questName("claude-opus-5-5"))
	require.LessOrEqual(t, len(questName("meta-llama/llama-4-maverick-17b-128e-instruct")), 32)
	require.Regexp(t, `^[A-Za-z0-9 ._-]{1,32}$`, questName("meta-llama/llama-4-maverick-17b-128e-instruct"))
}
