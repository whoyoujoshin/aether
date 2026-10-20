package main

import (
	"net/http"
	"runtime/debug"

	"github.com/whoyoujoshin/aether/a2a"
)

// --- GET /.well-known/agent-card.json ---
//
// The A2A v1.0 Agent Card (package a2a, docs/A2A.md). It is served only
// when --public-a2a names the endpoint it describes (agentmcp --http's
// /a2a behind the reverse proxy): a card is a promise that an A2A
// endpoint answers, so without one this path stays a 404.

// publicA2A is the public A2A JSON-RPC endpoint to advertise.
var publicA2A string

func handleAgentCard(w http.ResponseWriter, r *http.Request) {
	if publicA2A == "" {
		http.NotFound(w, r)
		return
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	writeJSON(w, http.StatusOK, a2a.Card(publicA2A, scheme+"://"+r.Host, buildRevision()))
}

// buildRevision is the commit the binary was built from, or "dev".
func buildRevision() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 7 {
				return s.Value[:7]
			}
		}
	}
	return "dev"
}
