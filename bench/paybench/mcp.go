package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tool is an MCP tool as a model sees it.
type Tool struct {
	Name        string
	Description string
	Schema      map[string]any // JSON Schema of the input
}

// walletClient is the connection to the hosted test wallet.
type walletClient struct {
	session *mcp.ClientSession
	tools   []Tool
}

// benchTransport adds the benchmark's key, which the server may honour
// with its own allowance of new wallets.
type benchTransport struct {
	key  string
	base http.RoundTripper
}

func (t benchTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if t.key != "" {
		r = r.Clone(r.Context())
		r.Header.Set("X-Aether-Bench-Key", t.key)
	}
	return t.base.RoundTrip(r)
}

func dialWallet(ctx context.Context, endpoint, benchKey string) (*walletClient, error) {
	httpClient := &http.Client{Transport: benchTransport{key: benchKey, base: http.DefaultTransport}, Timeout: 5 * time.Minute}
	client := mcp.NewClient(&mcp.Implementation{Name: "paybench", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: httpClient, DisableStandaloneSSE: true}, nil)
	if err != nil {
		return nil, err
	}
	list, err := session.ListTools(ctx, nil)
	if err != nil {
		session.Close()
		return nil, err
	}
	w := &walletClient{session: session}
	for _, t := range list.Tools {
		schema := map[string]any{}
		if bz, err := json.Marshal(t.InputSchema); err == nil {
			_ = json.Unmarshal(bz, &schema)
		}
		if schema["type"] == nil {
			schema["type"] = "object"
		}
		if schema["properties"] == nil {
			schema["properties"] = map[string]any{}
		}
		w.tools = append(w.tools, Tool{Name: t.Name, Description: t.Description, Schema: schema})
	}
	return w, nil
}

func (w *walletClient) Close() { _ = w.session.Close() }

// callResult is a tool's answer: its text, its structured content if any,
// and whether it was an error.
type callResult struct {
	Text       string
	Structured map[string]any
	IsError    bool
}

func (w *walletClient) call(ctx context.Context, name string, args json.RawMessage) (callResult, error) {
	var a map[string]any
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return callResult{Text: "arguments are not a JSON object: " + err.Error(), IsError: true}, nil
		}
	}
	r, err := w.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: a})
	if err != nil {
		return callResult{}, err
	}
	out := callResult{IsError: r.IsError}
	var texts []string
	for _, c := range r.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			texts = append(texts, t.Text)
		}
	}
	out.Text = strings.Join(texts, "\n")
	if r.StructuredContent != nil {
		if bz, err := json.Marshal(r.StructuredContent); err == nil {
			_ = json.Unmarshal(bz, &out.Structured)
		}
	}
	return out, nil
}

// callJSON calls a tool for the harness itself and decodes its result.
func (w *walletClient) callJSON(ctx context.Context, name string, args map[string]any, into any) error {
	bz, _ := json.Marshal(args)
	r, err := w.call(ctx, name, bz)
	if err != nil {
		return err
	}
	if r.IsError {
		return fmt.Errorf("%s: %s", name, r.Text)
	}
	if r.Structured != nil {
		bz, _ = json.Marshal(r.Structured)
	} else {
		bz = []byte(r.Text)
	}
	return json.Unmarshal(bz, into)
}

type testWallet struct {
	Token   string
	Address string
	Balance int64 // uaeth, once funded
}

type walletState struct {
	Address  string `json:"address"`
	Funded   bool   `json:"funded"`
	Balances []struct {
		Denom string `json:"denom"`
		Base  string `json:"base"`
	} `json:"balances"`
}

func (s walletState) uaeth() int64 {
	for _, b := range s.Balances {
		if b.Denom == "uaeth" {
			var n int64
			fmt.Sscan(b.Base, &n)
			return n
		}
	}
	return 0
}

func (w *walletClient) state(ctx context.Context, token string) (walletState, error) {
	var s walletState
	err := w.callJSON(ctx, "get_test_wallet", map[string]any{"walletToken": token}, &s)
	return s, err
}

// newFundedWallet makes the task's wallet and waits for its funding.
func (w *walletClient) newFundedWallet(ctx context.Context) (*testWallet, error) {
	var created struct {
		WalletToken string `json:"walletToken"`
		Address     string `json:"address"`
	}
	if err := w.callJSON(ctx, "create_test_wallet", map[string]any{}, &created); err != nil {
		return nil, err
	}
	tw := &testWallet{Token: created.WalletToken, Address: created.Address}
	for {
		s, err := w.state(ctx, tw.Token)
		if err == nil && s.Funded {
			tw.Balance = s.uaeth()
			return tw, nil
		}
		select {
		case <-ctx.Done():
			return nil, errors.New("its funding didn't land in time")
		case <-time.After(3 * time.Second):
		}
	}
}

// Call is one tool call an agent made.
type Call struct {
	Tool    string          `json:"tool"`
	Args    json.RawMessage `json:"args"`
	Result  string          `json:"result"` // truncated
	IsError bool            `json:"isError,omitempty"`
	Seconds float64         `json:"seconds"`
}

// recorder passes an agent's tool calls to the server and keeps what the
// scorer needs: the wallets it made and the payments it reported.
type recorder struct {
	w       *walletClient
	calls   []Call
	created []string        // walletTokens from create_test_wallet
	bought  []boughtCall    // buy_service results
	sent    []string        // send_from_test_wallet tx hashes
	results []callResultLog // every result, for answer checks
}

type boughtCall struct {
	URL    string
	TxHash string
	PayTo  string
	Amount int64 // base units
	Denom  string
	Body   string
	Status string
}

type callResultLog struct {
	Tool string
	Text string
}

func (r *recorder) wroteChain() bool {
	return len(r.created) > 0 || len(r.bought) > 0 || len(r.sent) > 0
}

const maxResultChars = 12000

// transportFailure starts the result of a call that failed on the way to or
// from the server, as opposed to a tool's own error (see notMeasured).
const transportFailure = "tool call failed: "

// call runs one tool call for the agent. The text goes back to the model
// (an error result's text explains the error).
func (r *recorder) call(ctx context.Context, name string, args json.RawMessage) (string, bool) {
	start := time.Now()
	res, err := r.w.call(ctx, name, args)
	if err != nil {
		res = callResult{Text: transportFailure + err.Error(), IsError: true}
	}
	text := res.Text
	if text == "" && res.Structured != nil {
		bz, _ := json.Marshal(res.Structured)
		text = string(bz)
	}
	if len(text) > maxResultChars {
		text = text[:maxResultChars] + " [truncated]"
	}
	logged := text
	if len(logged) > 2000 {
		logged = logged[:2000] + " [truncated]"
	}
	r.calls = append(r.calls, Call{Tool: name, Args: args, Result: logged, IsError: res.IsError, Seconds: time.Since(start).Seconds()})
	r.results = append(r.results, callResultLog{Tool: name, Text: text})
	if !res.IsError && res.Structured != nil {
		s := res.Structured
		str := func(k string) string { v, _ := s[k].(string); return v }
		switch name {
		case "create_test_wallet":
			if t := str("walletToken"); t != "" {
				r.created = append(r.created, t)
			}
		case "send_from_test_wallet":
			if h := str("txHash"); h != "" {
				r.sent = append(r.sent, h)
			}
		case "buy_service":
			var in struct {
				URL string `json:"url"`
			}
			_ = json.Unmarshal(args, &in)
			b := boughtCall{URL: in.URL, TxHash: str("txHash"), PayTo: str("payTo"), Body: str("body"), Status: str("status")}
			if amt, ok := s["amount"].(map[string]any); ok {
				b.Denom, _ = amt["denom"].(string)
				if base, ok := amt["base"].(string); ok {
					fmt.Sscan(base, &b.Amount)
				}
			}
			r.bought = append(r.bought, b)
		}
	}
	return text, res.IsError
}
