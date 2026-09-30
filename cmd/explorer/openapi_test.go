package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAPI_DescribesEveryRoute(t *testing.T) {
	rec := httptest.NewRecorder()
	handleOpenAPI(rec, httptest.NewRequest("GET", "/api/openapi.json", nil))
	require.Equal(t, 200, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var doc struct {
		OpenAPI string `json:"openapi"`
		Paths   map[string]struct {
			Get struct {
				Summary    string `json:"summary"`
				Parameters []struct {
					Name     string `json:"name"`
					Required bool   `json:"required"`
				} `json:"parameters"`
				Responses map[string]json.RawMessage `json:"responses"`
			} `json:"get"`
		} `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &doc))
	require.Equal(t, "3.1.0", doc.OpenAPI)

	routes := apiRoutes()
	require.Len(t, doc.Paths, len(routes))
	for _, rt := range routes {
		op, ok := doc.Paths[rt.path]
		require.True(t, ok, rt.path)
		require.NotEmpty(t, op.Get.Summary, rt.path)
		require.Contains(t, string(op.Get.Responses["200"]), `"properties"`, "%s has no response schema", rt.path)
		for _, p := range rt.params {
			found := false
			for _, q := range op.Get.Parameters {
				found = found || (q.Name == p.name && q.Required == p.required)
			}
			require.True(t, found, "%s: parameter %s", rt.path, p.name)
		}
	}
}

// Every /api/ route main serves comes from apiRoutes (so it's in the
// document), apart from the document itself.
func TestOpenAPI_NoRouteRegisteredOutsideTheTable(t *testing.T) {
	src, err := os.ReadFile("main.go")
	require.NoError(t, err)
	literal := regexp.MustCompile(`HandleFunc\("(/api/[^"]*)"`).FindAllStringSubmatch(string(src), -1)
	for _, m := range literal {
		require.Equal(t, "/api/openapi.json", m[1], "register API routes in apiRoutes, not main")
	}
}

// Each handler reads exactly the query parameters its route documents.
func TestOpenAPI_ParametersMatchHandlers(t *testing.T) {
	files := []string{"main.go", "detail.go", "grants.go", "ibc.go", "miner.go", "services.go", "agents.go", "helix.go"}
	var src strings.Builder
	for _, f := range files {
		bz, err := os.ReadFile(f)
		require.NoError(t, err)
		src.Write(bz)
	}
	read := map[string]bool{}
	for _, m := range regexp.MustCompile(`URL\.Query\(\)\.Get\("([a-z]+)"\)`).FindAllStringSubmatch(src.String(), -1) {
		read[m[1]] = true
	}
	documented := map[string]bool{}
	for _, rt := range apiRoutes() {
		for _, p := range rt.params {
			documented[p.name] = true
		}
	}
	require.Equal(t, read, documented)
}

func TestLLMsTxt(t *testing.T) {
	defer func(f string) { publicFaucet = f }(publicFaucet)
	publicFaucet = "https://faucet.example/request"
	req := httptest.NewRequest("GET", "/llms.txt", nil)
	req.Host = "explorer.example"
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	handleLLMsTxt(rec, req)

	body := rec.Body.String()
	require.Equal(t, "text/plain; charset=utf-8", rec.Header().Get("Content-Type"))
	require.True(t, strings.HasPrefix(body, "# Aether\n\n> "), "llms.txt starts with an H1 and a blockquote summary")
	for _, want := range []string{
		"https://explorer.example/api/agents",
		"https://explorer.example/api/openapi.json",
		"POST https://faucet.example/request/batch",
		"GET https://faucet.example/status?address=",
		"io.github.whoyoujoshin/aether-wallet",
		"send_aeth",
	} {
		require.Contains(t, body, want)
	}
}

func TestFaucetEndpoints(t *testing.T) {
	batch, status := faucetEndpoints("https://f.example/request")
	require.Equal(t, "https://f.example/request/batch", batch)
	require.Equal(t, "https://f.example/status", status)
	batch, _ = faucetEndpoints("https://f.example/drip")
	require.Empty(t, batch, "an unknown faucet layout isn't guessed at")
}
