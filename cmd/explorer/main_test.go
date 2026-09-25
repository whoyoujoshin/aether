package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSPAFallback_ServesTheAppAsHTML(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><div id=root></div>"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "assets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("console.log(1)"), 0o644))
	srv := httptest.NewServer(spaFallback(dir, http.FileServer(http.Dir(dir))))
	defer srv.Close()

	// A client-side route opened directly (bookmark, shared link, refresh).
	for _, path := range []string{"/services", "/tx/ABCD", "/blocks/12"} {
		resp, err := http.Get(srv.URL + path)
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, path)
		require.Equal(t, "text/html; charset=utf-8", resp.Header.Get("Content-Type"), path)
	}
	resp, err := http.Get(srv.URL + "/assets/app.js")
	require.NoError(t, err)
	resp.Body.Close()
	require.Contains(t, resp.Header.Get("Content-Type"), "javascript")
}
