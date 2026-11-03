package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	cmttypes "github.com/cometbft/cometbft/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/app"
)

// baseGenesis is what `aetherd init` writes: every module's defaults and
// the consensus params, no validators, no accounts.
func baseGenesis(t *testing.T) []byte {
	t.Helper()
	state := app.ModuleBasics.DefaultGenesis(app.MakeEncodingConfig().Codec)
	stateBz, err := json.Marshal(state)
	require.NoError(t, err)
	g := genutiltypes.NewAppGenesisWithVersion("aether-rc-test", stateBz)
	g.Consensus = genutiltypes.NewConsensusGenesis(cmttypes.DefaultConsensusParams().ToProto(), nil)
	bz, err := json.Marshal(g)
	require.NoError(t, err)
	return bz
}

func keyB64(n byte) json.RawMessage {
	return json.RawMessage(fmt.Sprintf("%q", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{n}, 32))))
}

func miner(n byte) string {
	return sdk.MustBech32ifyAddressBytes("aether", bytes.Repeat([]byte{n}, 32))
}

func threeOperators() []Operator {
	return []Operator{
		{Name: "carol", ConsensusPubKey: keyB64(3), Miner: miner(30)},
		{Name: "alice", ConsensusPubKey: keyB64(1), Miner: miner(10)},
		// The JSON `aetherd comet show-validator` prints is accepted as is.
		{Name: "bob", ConsensusPubKey: json.RawMessage(fmt.Sprintf(`{"@type":"/cosmos.crypto.ed25519.PubKey","key":%s}`, keyB64(2))), Miner: miner(20)},
	}
}

const when = "2026-12-01T16:00:00Z"

func TestBuild_GenesisForTheOperators(t *testing.T) {
	out, err := build(baseGenesis(t), threeOperators(), "aether-rc-1", when)
	require.NoError(t, err)

	// It loads and validates as the node will load it.
	g, err := genutiltypes.AppGenesisFromReader(bytes.NewReader(out))
	require.NoError(t, err)
	require.NoError(t, g.ValidateAndComplete())
	require.Equal(t, "aether-rc-1", g.ChainID)
	require.Equal(t, int64(1), g.InitialHeight)
	require.Equal(t, when, g.GenesisTime.UTC().Format("2006-01-02T15:04:05Z"))
	require.Len(t, g.Consensus.Validators, 3)
	for i, v := range g.Consensus.Validators {
		require.Equal(t, int64(genesisPower), v.Power, v.Name)
		require.Equal(t, []string{"alice", "bob", "carol"}[i], v.Name, "validators in name order")
	}

	// The escrow lock outlasts the evidence window, which stays 48 hours.
	require.Equal(t, int64(freshEvidenceAgeBlocks), g.Consensus.Params.Evidence.MaxAgeNumBlocks)
	require.Equal(t, 48*time.Hour, g.Consensus.Params.Evidence.MaxAgeDuration)
	require.Greater(t, int64(freshBondCooldown)*5, int64(48*60*60), "even at 5 s blocks the lock is longer than 48 h")

	var state map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(g.AppState, &state))
	var powState struct {
		Params struct {
			BondCooldown int64 `json:"bond_cooldown"`
		} `json:"params"`
	}
	require.NoError(t, json.Unmarshal(state["pow"], &powState))
	require.Equal(t, int64(freshBondCooldown), powState.Params.BondCooldown)
	var auth struct {
		Accounts []struct {
			Address       string `json:"address"`
			AccountNumber string `json:"account_number"`
		} `json:"accounts"`
	}
	require.NoError(t, json.Unmarshal(state["auth"], &auth))
	require.Len(t, auth.Accounts, 3)
	for i, a := range auth.Accounts {
		require.Equal(t, fmt.Sprint(i), a.AccountNumber)
		if i > 0 {
			require.Less(t, auth.Accounts[i-1].Address, a.Address, "accounts in address order")
		}
	}
	var bank struct {
		Balances []json.RawMessage `json:"balances"`
		Supply   []json.RawMessage `json:"supply"`
	}
	require.NoError(t, json.Unmarshal(state["bank"], &bank))
	require.Empty(t, bank.Balances, "nobody is given AETH")
	require.Empty(t, bank.Supply)

	// The same input gives the same bytes, whatever order operators come in.
	ops := threeOperators()
	ops[0], ops[2] = ops[2], ops[0]
	again, err := build(baseGenesis(t), ops, "aether-rc-1", when)
	require.NoError(t, err)
	require.Equal(t, out, again)
}

func TestBuild_Refusals(t *testing.T) {
	base := baseGenesis(t)
	dupKey := threeOperators()
	dupKey[1].ConsensusPubKey = keyB64(3)
	dupMiner := threeOperators()
	dupMiner[2].Miner = miner(30)
	dupName := threeOperators()
	dupName[2].Name = "carol"
	badMiner := threeOperators()
	badMiner[0].Miner = "cosmos1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"
	shortKey := threeOperators()
	shortKey[0].ConsensusPubKey = json.RawMessage(`"AAAA"`)
	secpKey := threeOperators()
	secpKey[0].ConsensusPubKey = json.RawMessage(`{"@type":"/cosmos.crypto.secp256k1.PubKey","key":"AAAA"}`)

	var funded map[string]any
	require.NoError(t, json.Unmarshal(base, &funded))
	funded["app_state"].(map[string]any)["bank"].(map[string]any)["balances"] = []any{map[string]any{"address": miner(9), "coins": []any{}}}
	fundedBz, _ := json.Marshal(funded)

	for _, c := range []struct {
		name    string
		base    []byte
		ops     []Operator
		chainID string
		want    string
	}{
		{"testnet chain ID", base, threeOperators(), "aether-testnet-1", "isn't a fresh chain"},
		{"other devnet", base, threeOperators(), "aether-dev", "isn't a fresh chain"},
		{"nobody", base, nil, "aether-rc-1", "lists nobody"},
		{"key twice", base, dupKey, "aether-rc-1", "consensus key is listed twice"},
		{"miner twice", base, dupMiner, "aether-rc-1", "listed twice; one miner holds one seat"},
		{"name twice", base, dupName, "aether-rc-1", "name is listed twice"},
		{"not an aether address", base, badMiner, "aether-rc-1", "isn't an aether1 address"},
		{"short key", base, shortKey, "aether-rc-1", "an ed25519 key is 32"},
		{"not ed25519", base, secpKey, "aether-rc-1", "consensus keys are ed25519"},
		{"balance in base", fundedBz, threeOperators(), "aether-1", "fair launch"},
	} {
		_, err := build(c.base, c.ops, c.chainID, when)
		require.Error(t, err, c.name)
		require.True(t, strings.Contains(err.Error(), c.want), "%s: %v", c.name, err)
	}
	_, err := build(base, threeOperators(), "aether-rc-1", "tomorrow")
	require.ErrorContains(t, err, "--genesis-time")
}
