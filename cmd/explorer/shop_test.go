package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShopCoversOnlyAListedPrefix(t *testing.T) {
	require.True(t, shopCovers("https://example.test/svc/hello", "https://example.test/svc/hello"))
	require.True(t, shopCovers("https://example.test/svc/hello", "https://example.test/svc/hello/help"))
	require.False(t, shopCovers("https://example.test/svc/hello", "https://evil.test/svc/hello"))
}
