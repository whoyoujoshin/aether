package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHelicasePaths(t *testing.T) {
	paths, err := helicasePaths("", "")
	require.NoError(t, err)
	require.Empty(t, paths, "both empty: Helicase is off")

	paths, err = helicasePaths("https://rpc.osmo:443", "07-tendermint-1")
	require.NoError(t, err)
	require.Equal(t, []helicasePath{{"https://rpc.osmo:443", "07-tendermint-1"}}, paths, "one chain, as before")

	paths, err = helicasePaths("https://rpc.osmo:443, https://rpc.inj:443", "07-tendermint-1,07-tendermint-2")
	require.NoError(t, err)
	require.Equal(t, []helicasePath{{"https://rpc.osmo:443", "07-tendermint-1"}, {"https://rpc.inj:443", "07-tendermint-2"}}, paths)

	_, err = helicasePaths("https://rpc.osmo:443,https://rpc.inj:443", "07-tendermint-1")
	require.ErrorContains(t, err, "one client per chain")
	_, err = helicasePaths("a,b", "07-tendermint-1,07-tendermint-1")
	require.ErrorContains(t, err, "twice")
}
