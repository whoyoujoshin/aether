package shopper

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExpandBlockDataFeed(t *testing.T) {
	syns, err := LoadSynonyms()
	require.NoError(t, err)
	sets := Expand(syns, "block-data feed")
	require.NotEmpty(t, sets)
	hay := "the aether chain right now: height, block time, difficulty, block reward"
	require.True(t, Matches(sets, hay), "block-data feed should reach pulse through the shipped synonyms")
	require.False(t, Matches(sets, "this epoch's proof-of-work leaderboard: miners by work"))
}

func TestExpandEmpty(t *testing.T) {
	syns, err := LoadSynonyms()
	require.NoError(t, err)
	require.Nil(t, Expand(syns, "   "))
	require.False(t, Matches(nil, "block time"))
}

func TestParseSynonymsRejectsAMissingTab(t *testing.T) {
	_, err := ParseSynonyms("block-data block\n")
	require.Error(t, err)
}
