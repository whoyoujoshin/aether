package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Usage is what a run cost in model terms.
type Usage struct {
	Turns int
	In    int64
	Out   int64
}

// ToolCaller runs one tool call; it returns the result text and whether
// it's an error.
type ToolCaller func(ctx context.Context, name string, args json.RawMessage) (string, bool)

// Agent drives one model through a task.
type Agent interface {
	Run(ctx context.Context, system, prompt string, tools []Tool, call ToolCaller, maxTurns int) (answer string, usage Usage, err error)
}

// --- Claude, through the Anthropic API ---

type anthropicAgent struct {
	client anthropic.Client
	model  string
}

func newAnthropicAgent(model string, opts ...option.RequestOption) *anthropicAgent {
	return &anthropicAgent{client: anthropic.NewClient(opts...), model: model} // ANTHROPIC_API_KEY
}

func (a *anthropicAgent) Run(ctx context.Context, system, prompt string, tools []Tool, call ToolCaller, maxTurns int) (string, Usage, error) {
	var params []anthropic.ToolUnionParam
	for _, t := range tools {
		schema := anthropic.ToolInputSchemaParam{Properties: t.Schema["properties"]}
		if req, ok := t.Schema["required"].([]any); ok {
			for _, r := range req {
				if s, ok := r.(string); ok {
					schema.Required = append(schema.Required, s)
				}
			}
		}
		tp := anthropic.ToolParam{Name: t.Name, Description: anthropic.String(t.Description), InputSchema: schema}
		params = append(params, anthropic.ToolUnionParam{OfTool: &tp})
	}
	messages := []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(prompt))}
	var u Usage
	answer := ""
	for u.Turns < maxTurns {
		resp, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
			Model:     a.model,
			MaxTokens: 16000,
			System:    []anthropic.TextBlockParam{{Text: system}},
			Tools:     params,
			Messages:  messages,
		})
		if err != nil {
			return answer, u, err
		}
		u.Turns++
		u.In += resp.Usage.InputTokens
		u.Out += resp.Usage.OutputTokens
		if resp.StopReason == anthropic.StopReasonRefusal {
			return answer, u, errors.New("the model declined (stop_reason refusal)")
		}
		messages = append(messages, resp.ToParam())

		var texts []string
		var results []anthropic.ContentBlockParamUnion
		for _, block := range resp.Content {
			switch b := block.AsAny().(type) {
			case anthropic.TextBlock:
				texts = append(texts, b.Text)
			case anthropic.ToolUseBlock:
				out, isErr := call(ctx, b.Name, json.RawMessage(b.JSON.Input.Raw()))
				results = append(results, anthropic.NewToolResultBlock(b.ID, out, isErr))
			}
		}
		if len(texts) > 0 {
			answer = strings.Join(texts, "\n")
		}
		if resp.StopReason != anthropic.StopReasonToolUse {
			return answer, u, nil
		}
		messages = append(messages, anthropic.NewUserMessage(results...))
	}
	return answer, u, fmt.Errorf("stopped after %d turns", maxTurns)
}

// --- any OpenAI-compatible chat completions API (GPT, open models) ---

type openAIAgent struct {
	baseURL string
	model   string
	apiKey  string
}

type oaMessage struct {
	Role       string       `json:"role"`
	Content    *string      `json:"content"`
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

type oaToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

var oaHTTP = &http.Client{Timeout: 10 * time.Minute}

func (a *openAIAgent) Run(ctx context.Context, system, prompt string, tools []Tool, call ToolCaller, maxTurns int) (string, Usage, error) {
	type fn struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	}
	type tool struct {
		Type     string `json:"type"`
		Function fn     `json:"function"`
	}
	var ts []tool
	for _, t := range tools {
		ts = append(ts, tool{Type: "function", Function: fn{Name: t.Name, Description: t.Description, Parameters: t.Schema}})
	}
	str := func(s string) *string { return &s }
	messages := []oaMessage{{Role: "system", Content: str(system)}, {Role: "user", Content: str(prompt)}}
	var u Usage
	answer := ""
	for u.Turns < maxTurns {
		body, _ := json.Marshal(map[string]any{"model": a.model, "messages": messages, "tools": ts})
		req, err := http.NewRequestWithContext(ctx, "POST", a.baseURL+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			return answer, u, err
		}
		req.Header.Set("Content-Type", "application/json")
		if a.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+a.apiKey)
		}
		resp, err := oaHTTP.Do(req)
		if err != nil {
			return answer, u, err
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			return answer, u, fmt.Errorf("chat completions: HTTP %d: %.400s", resp.StatusCode, raw)
		}
		var out struct {
			Choices []struct {
				Message      oaMessage `json:"message"`
				FinishReason string    `json:"finish_reason"`
			} `json:"choices"`
			Usage struct {
				Prompt     int64 `json:"prompt_tokens"`
				Completion int64 `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(raw, &out); err != nil || len(out.Choices) == 0 {
			return answer, u, fmt.Errorf("chat completions: unreadable reply: %.400s", raw)
		}
		u.Turns++
		u.In += out.Usage.Prompt
		u.Out += out.Usage.Completion
		msg := out.Choices[0].Message
		msg.Role = "assistant"
		messages = append(messages, msg)
		if msg.Content != nil && *msg.Content != "" {
			answer = *msg.Content
		}
		if len(msg.ToolCalls) == 0 {
			return answer, u, nil
		}
		for _, tc := range msg.ToolCalls {
			res, isErr := call(ctx, tc.Function.Name, json.RawMessage(tc.Function.Arguments))
			if isErr {
				res = "ERROR: " + res
			}
			messages = append(messages, oaMessage{Role: "tool", Content: str(res), ToolCallID: tc.ID})
		}
	}
	return answer, u, fmt.Errorf("stopped after %d turns", maxTurns)
}
