package main

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The agent-facing contract (docs/API-STABILITY.md): these paths and
// fields only ever gain things. If a test here fails, a change removed or
// renamed something bots depend on. Add the new name next to the old one
// instead, and keep the old one for the overlap the policy promises.

// jsonFields is every JSON field name a type encodes, embedded structs
// included.
func jsonFields(t reflect.Type) map[string]bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	out := map[string]bool{}
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if f.Anonymous && name == "" {
			for k := range jsonFields(f.Type) {
				out[k] = true
			}
			continue
		}
		if name != "" && name != "-" && f.IsExported() {
			out[name] = true
		}
	}
	return out
}

func requireFields(t *testing.T, v any, want ...string) {
	t.Helper()
	have := jsonFields(reflect.TypeOf(v))
	for _, w := range want {
		require.True(t, have[w], "%T lost the stable field %q (docs/API-STABILITY.md)", v, w)
	}
}

func TestContract_StablePathsExist(t *testing.T) {
	routes := map[string]bool{"/api/openapi.json": true} // registered beside the table
	for _, rt := range apiRoutes() {
		routes[rt.path] = true
	}
	for _, p := range []string{"/api/agents", "/api/address", "/api/tx", "/api/openapi.json"} {
		require.True(t, routes[p], "stable path %s is no longer served", p)
	}
	doc, err := openAPIDoc()
	require.NoError(t, err)
	var spec struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(doc, &spec))
	for _, p := range []string{"/api/agents", "/api/address", "/api/tx"} {
		require.Contains(t, spec.Paths, p, "stable path %s is no longer in the OpenAPI spec", p)
	}
}

func TestContract_StableFields(t *testing.T) {
	requireFields(t, agentCardDTO{}, "name", "chainId", "height", "addressPrefix", "denom", "displayDenom",
		"decimals", "signatures", "endpoints", "faucet", "mcp", "paymentSchemes", "docs", "warnings")
	requireFields(t, agentEndpointsDTO{}, "rpc", "grpc", "faucet", "explorer", "seed")
	requireFields(t, agentFaucetDTO{}, "url", "request", "batch", "status", "reachable")
	requireFields(t, agentMCPDTO{}, "install", "init", "tools")

	requireFields(t, addressResponse{}, "address", "balance", "balances", "transactions")
	requireFields(t, coinDTO{}, "denom", "amount")
	requireFields(t, transactionDTO{}, "hash", "height", "code", "direction", "amount", "timestamp")

	requireFields(t, txExtrasDTO{}, "hash", "height", "code", "from", "to", "amount", "timestamp", "transfers")
}

func TestContract_LLMsTxtStartsWithTheStartPage(t *testing.T) {
	req := httptest.NewRequest("GET", "/llms.txt", nil)
	req.Host = "explorer.example"
	rec := httptest.NewRecorder()
	handleLLMsTxt(rec, req)
	body := rec.Body.String()
	_, after, ok := strings.Cut(body, "## Start here\n\n")
	require.True(t, ok, "llms.txt has a Start here section")
	require.True(t, strings.HasPrefix(after, "- [Start page](http://explorer.example/start.md)"),
		"the start page is the first link agents see")
	for _, p := range []string{"/start.md", "/api/agents", "/api/openapi.json"} {
		require.Contains(t, body, "http://explorer.example"+p)
	}
}
