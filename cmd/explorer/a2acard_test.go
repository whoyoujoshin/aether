package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The card is served only when --public-a2a names an endpoint, and has
// every field lf.a2a.v1 marks REQUIRED, in its JSON names.
func TestA2AAgentCard(t *testing.T) {
	defer func(prev string) { publicA2A = prev }(publicA2A)

	publicA2A = ""
	rec := httptest.NewRecorder()
	handleAgentCard(rec, httptest.NewRequest(http.MethodGet, "/.well-known/agent-card.json", nil))
	require.Equal(t, http.StatusNotFound, rec.Code)

	publicA2A = "https://explorer.example/a2a"
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/.well-known/agent-card.json", nil)
	req.Host = "explorer.example"
	req.Header.Set("X-Forwarded-Proto", "https")
	handleAgentCard(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var card map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &card))
	for _, f := range []string{"name", "description", "supportedInterfaces", "version", "capabilities", "defaultInputModes", "defaultOutputModes", "skills"} {
		require.NotEmpty(t, card[f], f)
	}
	ifaces := card["supportedInterfaces"].([]any)
	require.Len(t, ifaces, 1)
	iface := ifaces[0].(map[string]any)
	require.Equal(t, map[string]any{"url": "https://explorer.example/a2a", "protocolBinding": "JSONRPC", "protocolVersion": "1.0"}, iface)
	require.Equal(t, "https://explorer.example", card["provider"].(map[string]any)["url"])
	require.Equal(t, map[string]any{"streaming": false, "pushNotifications": false, "extendedAgentCard": false}, card["capabilities"])

	var ids []string
	for _, s := range card["skills"].([]any) {
		skill := s.(map[string]any)
		for _, f := range []string{"id", "name", "description", "tags"} {
			require.NotEmpty(t, skill[f], f)
		}
		ids = append(ids, skill["id"].(string))
	}
	require.ElementsMatch(t, publicMCPTools, ids, "the card's skills are the public tools")
}

// llms.txt and /api/agents point at the card only when there is one.
func TestA2AListedOnlyWithACard(t *testing.T) {
	defer func(prev string) { publicA2A = prev }(publicA2A)
	for _, endpoint := range []string{"", "https://explorer.example/a2a"} {
		publicA2A = endpoint
		rec := httptest.NewRecorder()
		handleLLMsTxt(rec, httptest.NewRequest(http.MethodGet, "/llms.txt", nil))
		require.Equal(t, endpoint != "", strings.Contains(rec.Body.String(), "/.well-known/agent-card.json"), endpoint)

		rec = httptest.NewRecorder()
		handleAgents(rec, httptest.NewRequest(http.MethodGet, "/api/agents", nil))
		var card agentCardDTO
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &card))
		require.Equal(t, endpoint != "", card.A2A != nil, endpoint)
	}
}
