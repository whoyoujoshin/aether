package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cometbft/cometbft/privval"
	"github.com/stretchr/testify/require"
)

const miner = "aether13cfj5example"

// nodeHome writes a priv_validator_key.json the way CometBFT does and
// returns the home directory and the key's public half.
func nodeHome(t *testing.T) (string, []byte) {
	t.Helper()
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "config"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(home, "data"), 0o700))
	pv := privval.GenFilePV(
		filepath.Join(home, "config", "priv_validator_key.json"),
		filepath.Join(home, "data", "priv_validator_state.json"),
	)
	pv.Save()
	return home, pv.Key.PubKey.Bytes()
}

var registerLine = regexp.MustCompile(`register-validator-pubkey ([0-9a-f]+) ([0-9a-f]+) --from (\S+)`)

// registered returns the pubkey and PoP signature the printed command
// would register, checking the signature verifies over the miner address.
func registered(t *testing.T, out string) []byte {
	t.Helper()
	m := registerLine.FindStringSubmatch(out)
	require.NotNil(t, m, out)
	pub, err := hex.DecodeString(m[1])
	require.NoError(t, err)
	sig, err := hex.DecodeString(m[2])
	require.NoError(t, err)
	require.Equal(t, miner, m[3])
	require.True(t, ed25519.Verify(pub, []byte(miner), sig))
	return pub
}

func TestDefaultRegistersTheNodesOwnKey(t *testing.T) {
	home, nodePub := nodeHome(t)
	var out bytes.Buffer
	require.NoError(t, run([]string{"--miner", miner, "--home", home}, &out))
	require.Equal(t, nodePub, registered(t, out.String()))
	require.Contains(t, out.String(), "Using this node's consensus key")
	require.Contains(t, out.String(), "--home "+home)
	require.NotContains(t, out.String(), "priv_key")
}

func TestKeyFileFlag(t *testing.T) {
	home, nodePub := nodeHome(t)
	var out bytes.Buffer
	keyFile := filepath.Join(home, "config", "priv_validator_key.json")
	require.NoError(t, run([]string{"--miner", miner, "--key-file", keyFile}, &out))
	require.Equal(t, nodePub, registered(t, out.String()))
}

func TestMissingKeyFileIsRefusedNotReplacedWithANewKey(t *testing.T) {
	var out bytes.Buffer
	err := run([]string{"--miner", miner, "--home", t.TempDir()}, &out)
	require.ErrorContains(t, err, "priv_validator_key.json")
	require.ErrorContains(t, err, "--new-key")
	require.Empty(t, out.String())
}

func TestInconsistentKeyFileIsRefused(t *testing.T) {
	home, _ := nodeHome(t)
	path := filepath.Join(home, "config", "priv_validator_key.json")
	bz, err := os.ReadFile(path)
	require.NoError(t, err)
	var f privValidatorKey
	require.NoError(t, json.Unmarshal(bz, &f))
	_, other, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	f.PubKey = keyFileFor(other).PubKey
	bz, err = json.Marshal(f)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, bz, 0o600))

	err = run([]string{"--miner", miner, "--home", home}, &bytes.Buffer{})
	require.ErrorContains(t, err, "inconsistent")
}

func TestNewKeyPrintsAKeyFileCometBFTLoads(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, run([]string{"--miner", miner, "--new-key"}, &out))
	pub := registered(t, out.String())
	require.Contains(t, out.String(), "NEW key")

	// The printed JSON, installed as a node's key file, is the key registered.
	s := out.String()
	jsonText := s[strings.Index(s, "{") : strings.LastIndex(s, "}")+1]
	home := t.TempDir()
	keyPath := filepath.Join(home, "priv_validator_key.json")
	statePath := filepath.Join(home, "priv_validator_state.json")
	require.NoError(t, os.WriteFile(keyPath, []byte(jsonText), 0o600))
	require.NoError(t, os.WriteFile(statePath, []byte(`{"height":"0","round":0,"step":0}`), 0o600))
	pv := privval.LoadFilePV(keyPath, statePath)
	require.Equal(t, pub, pv.Key.PubKey.Bytes())
}

func TestExistingPrivKeyB64(t *testing.T) {
	home, nodePub := nodeHome(t)
	bz, err := os.ReadFile(filepath.Join(home, "config", "priv_validator_key.json"))
	require.NoError(t, err)
	var f privValidatorKey
	require.NoError(t, json.Unmarshal(bz, &f))

	var out bytes.Buffer
	require.NoError(t, run([]string{"--miner", miner, "--existing-priv-key-b64", f.PrivKey.Value}, &out))
	require.Equal(t, nodePub, registered(t, out.String()))
	require.NotContains(t, out.String(), "--home")
}

func TestConflictingFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--miner", miner, "--new-key", "--existing-priv-key-b64", "x"},
		{"--miner", miner, "--new-key", "--home", "/tmp"},
		{"--miner", miner, "--existing-priv-key-b64", "x", "--key-file", "/tmp/k.json"},
		{"--home", "/tmp"},
	} {
		require.Error(t, run(args, &bytes.Buffer{}), args)
	}
}
