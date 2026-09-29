package cli

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseAmount(t *testing.T) {
	for in, want := range map[string]string{
		"5000000uaeth":      "5000000uaeth",
		"5aeth":             "5000000uaeth",
		"1.5AETH":           "1500000uaeth",
		"0.000001aeth":      "1uaeth",
		"2aeth,3ibc/ABC":    "3ibc/ABC,2000000uaeth",
		"1aeth,500000uaeth": "1500000uaeth",
	} {
		got, err := ParseAmount(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got.String(), in)
	}
	for _, bad := range []string{"", "0uaeth", "0.0000001aeth", "1.5uaeth", "-1uaeth", "abc"} {
		_, err := ParseAmount(bad)
		require.Error(t, err, bad)
	}
}
