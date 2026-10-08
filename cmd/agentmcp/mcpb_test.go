package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/wallet"
)

func TestMCPBManifest(t *testing.T) {
	m, err := buildMCPBManifest(context.Background(), "0.2.1-testnet")
	require.NoError(t, err)
	require.Equal(t, "0.2.1-testnet", m.Version)
	require.Len(t, m.Tools, 24, "every registered tool is declared")
	require.Equal(t, []string{repoURL + "/blob/main/docs/PRIVACY.md"}, m.Privacy, "directories ask for a privacy policy")
	_, err = os.Stat("../../docs/PRIVACY.md")
	require.NoError(t, err, "the linked policy exists")
	for _, tool := range m.Tools {
		require.NotEmpty(t, tool.Description, tool.Name)
	}

	// Every command runs agentmcp against the public testnet with the
	// user's settings, and every setting it references exists and has a default.
	cfg := m.Server.MCPConfig
	commands := map[string]mcpbCommand{"default": cfg.mcpbCommand}
	for os, c := range cfg.PlatformOverrides {
		commands[os] = c
	}
	require.Len(t, commands, 3)
	ref := regexp.MustCompile(`\$\{user_config\.([a-z_]+)\}`)
	for os, c := range commands {
		joined := strings.Join(c.Args, " ")
		require.Contains(t, joined, "--grpc "+testnetGRPC+" --rpc "+testnetRPC+" --chain-id aether-testnet-1", os)
		for _, key := range ref.FindAllStringSubmatch(joined, -1) {
			uc, ok := m.UserConfig[key[1]]
			require.True(t, ok, "%s references undefined user_config.%s", os, key[1])
			require.NotNil(t, uc.Default, "user_config.%s needs a default: clients may not substitute blanks", key[1])
		}
	}
	// The bundle knows the testnet's USDC, and its caps are settings
	// that parse as USDC.
	joined := strings.Join(cfg.Args, " ")
	require.Contains(t, joined, "--usdc-path "+wallet.TestnetUSDC.Path+" --usdc-base-denom "+wallet.TestnetUSDC.BaseDenom+" --usdc-issuer Injective")
	for _, key := range []string{"usdc_per_tx_limit", "usdc_daily_limit"} {
		require.Contains(t, joined, "${user_config."+key+"}")
		assets, err := wallet.NewAssetsFor(wallet.TestnetUSDC)
		require.NoError(t, err)
		as, _, err := assets.Parse(m.UserConfig[key].Default.(string))
		require.NoError(t, err, key)
		require.Equal(t, "USDC", as.Symbol, key)
	}
	require.Equal(t, "${__dirname}/server/agentmcp", cfg.Command)
	require.Equal(t, "${__dirname}/server/agentmcp.exe", cfg.PlatformOverrides["win32"].Command)
	require.Equal(t, "${__dirname}/server/darwin/agentmcp.sh", cfg.PlatformOverrides["darwin"].Args[0], "scripts/mcpb/agentmcp.sh picks the Mac's CPU")
}

func TestServerJSON(t *testing.T) {
	var out bytes.Buffer
	sha := strings.Repeat("ab", 32)
	url := "https://github.com/whoyoujoshin/aether/releases/download/v0.2.1-testnet/aether-wallet.mcpb"
	require.NoError(t, runPackaging([]string{"server-json", "--version", "0.2.1-testnet", "--url", url, "--sha256", sha}, &out))
	var doc struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Version     string `json:"version"`
		Packages    []struct {
			RegistryType, Identifier, Version, FileSha256 string
		} `json:"packages"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &doc))
	require.Equal(t, registryName, doc.Name)
	require.LessOrEqual(t, len(doc.Description), 100, "the registry's limit")
	require.Equal(t, "0.2.1-testnet", doc.Version)
	require.Len(t, doc.Packages, 1)
	require.Equal(t, "mcpb", doc.Packages[0].RegistryType)
	require.Equal(t, url, doc.Packages[0].Identifier)
	require.Equal(t, sha, doc.Packages[0].FileSha256)
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out.Bytes(), &raw))
	_, hasRemotes := raw["remotes"]
	require.False(t, hasRemotes, "remotes defaults to empty and is omitted")

	out.Reset()
	remote := "https://explorer.example/mcp"
	require.NoError(t, runPackaging([]string{"server-json", "--version", "0.2.1-testnet", "--url", url, "--sha256", sha, "--remote", remote}, &out))
	var withRemote struct {
		Name     string `json:"name"`
		Packages []struct {
			RegistryType string `json:"registryType"`
		} `json:"packages"`
		Remotes []struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"remotes"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &withRemote))
	require.Equal(t, registryName, withRemote.Name)
	require.Len(t, withRemote.Packages, 1, "packages stay; remotes are added beside them")
	require.Equal(t, "mcpb", withRemote.Packages[0].RegistryType)
	require.Equal(t, []struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	}{{Type: "streamable-http", URL: remote}}, withRemote.Remotes)

	for _, bad := range [][]string{
		{"server-json", "--version", "v0.2.1-testnet", "--url", url, "--sha256", sha},              // tag, not version
		{"server-json", "--version", "0.2.1-testnet", "--url", url, "--sha256", "abc"},             // not a SHA-256
		{"server-json", "--version", "0.2.1-testnet", "--url", "https://x/y.zip", "--sha256", sha}, // registry needs "mcp" in the URL
		{"server-json", "--version", "0.2.1-testnet", "--url", url, "--sha256", sha, "--remote", "not-a-url"},
	} {
		require.Error(t, runPackaging(bad, &bytes.Buffer{}), bad)
	}
}
