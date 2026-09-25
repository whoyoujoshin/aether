package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseUaeth(t *testing.T) {
	for in, want := range map[string]int64{"1": 1, "1500000": 1_500_000, "010": 10, "0100000": 100_000} {
		got, err := parseUaeth(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got.Int64(), "%q must be read as base 10", in)
	}
	for _, in := range []string{"", "0", "-5", "0x10", "0b1", "0o7", "1_000", "1.5", "1e6", " 5", "abc"} {
		_, err := parseUaeth(in)
		require.Error(t, err, in)
	}
}
