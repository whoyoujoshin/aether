package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/whoyoujoshin/aether/wallet"
)

// Packaging for MCP clients and the MCP Registry:
//
//	agentmcp mcpb-manifest --version 0.2.1-testnet
//	    the MCPB bundle's manifest.json (https://github.com/modelcontextprotocol/mcpb)
//	agentmcp server-json --version 0.2.1-testnet --url <.mcpb release URL> --sha256 <hex> [--remote <streamable HTTP URL>]
//	    the MCP Registry's server.json for that bundle. --remote is optional
//	    and defaults to empty (no remotes field). The same registry name is
//	    kept; do not publish a version from here.
//
// scripts/package-mcpb.sh builds the bundle from these; the release
// workflow runs it for every tag.

const (
	registryName = "io.github.whoyoujoshin/aether-wallet"
	repoURL      = "https://github.com/whoyoujoshin/aether"
	// The registry caps this at 100 characters.
	shortDescription = "Testnet wallet for AI agents on Aether: pay, get paid and buy from paid APIs, with spending caps."
)

type mcpbTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type mcpbUserConfig struct {
	Type        string `json:"type"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
	Default     any    `json:"default,omitempty"`
	Min         *int64 `json:"min,omitempty"`
}

type mcpbCommand struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

type mcpbManifest struct {
	ManifestVersion string `json:"manifest_version"`
	Name            string `json:"name"`
	DisplayName     string `json:"display_name"`
	Version         string `json:"version"`
	Description     string `json:"description"`
	LongDescription string `json:"long_description"`
	Icon            string `json:"icon"`
	Author          struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"author"`
	Repository struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"repository"`
	Homepage      string   `json:"homepage"`
	Documentation string   `json:"documentation"`
	Support       string   `json:"support"`
	License       string   `json:"license"`
	Keywords      []string `json:"keywords"`
	Server        struct {
		Type       string `json:"type"`
		EntryPoint string `json:"entry_point"`
		MCPConfig  struct {
			mcpbCommand
			PlatformOverrides map[string]mcpbCommand `json:"platform_overrides"`
		} `json:"mcp_config"`
	} `json:"server"`
	Tools         []mcpbTool                `json:"tools"`
	Compatibility map[string]any            `json:"compatibility"`
	UserConfig    map[string]mcpbUserConfig `json:"user_config"`
}

// registeredTools lists newServer's tools the way a client sees them.
func registeredTools(ctx context.Context) ([]mcpbTool, error) {
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := newServer().Connect(ctx, serverT, nil)
	if err != nil {
		return nil, err
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "mcpb-manifest"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		return nil, err
	}
	defer cs.Close()
	var tools []mcpbTool
	for t, err := range cs.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		tools = append(tools, mcpbTool{Name: t.Name, Description: t.Description})
	}
	return tools, nil
}

func buildMCPBManifest(ctx context.Context, ver string) (*mcpbManifest, error) {
	tools, err := registeredTools(ctx)
	if err != nil {
		return nil, err
	}
	m := &mcpbManifest{
		ManifestVersion: "0.3",
		Name:            "aether-wallet",
		DisplayName:     "Aether testnet wallet",
		Version:         ver,
		Description:     shortDescription,
		LongDescription: "A wallet an AI agent can use on the Aether public testnet (`aether-testnet-1`): check its balance, send AETH, " +
			"invoice and wait for payment, pay for HTTP 402 APIs, find paid services, and top itself up from the testnet faucet.\n\n" +
			"On first use it creates its own account in the keyring folder below. Spending is capped per payment and per 24 hours by this server. " +
			"**Testnet only: the account is a disposable hot wallet, so never send it anything of value.** The node is reached over plain HTTP.\n\n" +
			"Owner approvals, webhook alerts and spending from your own account under a chain-enforced grant are available when you run `agentmcp` yourself: " +
			repoURL + "#ai-agent-wallet-mcp",
		Icon:          "icon.png",
		Homepage:      repoURL + "#ai-agents-start-here",
		Documentation: repoURL + "#ai-agent-wallet-mcp",
		Support:       repoURL + "/issues",
		License:       "MIT",
		Keywords:      []string{"aether", "wallet", "payments", "x402", "testnet", "crypto", "agents"},
		Tools:         tools,
		Compatibility: map[string]any{"platforms": []string{"darwin", "win32", "linux"}},
	}
	m.Author.Name, m.Author.URL = "Aether", repoURL
	m.Repository.Type, m.Repository.URL = "git", repoURL

	one := int64(1)
	m.UserConfig = map[string]mcpbUserConfig{
		"per_tx_limit": {Type: "number", Title: "Per-payment limit (uaeth)", Required: false, Default: 1_000_000, Min: &one,
			Description: "Most the agent may send in one payment, in uaeth (1 AETH = 1,000,000 uaeth)."},
		"daily_limit": {Type: "number", Title: "Daily limit (uaeth)", Required: false, Default: 5_000_000, Min: &one,
			Description: "Most the agent may spend in any rolling 24 hours, in uaeth."},
		"keyring_dir": {Type: "directory", Title: "Keyring folder", Required: false, Default: "${HOME}/.aether-agent",
			Description: "Where the agent's key and spending records live. Keep it private: whoever can read it controls the account."},
		"usdc_per_tx_limit": {Type: "string", Title: "USDC per-payment limit", Required: false, Default: "1 USDC",
			Description: `Most the agent may send in one USDC payment, with its unit (e.g. "1 USDC"). USDC is Circle's testnet USDC from Injective, through Osmosis; its caps are separate from AETH's.`},
		"usdc_daily_limit": {Type: "string", Title: "USDC daily limit", Required: false, Default: "5 USDC",
			Description: `Most USDC the agent may spend in any rolling 24 hours, with its unit (e.g. "5 USDC").`},
	}
	usdc := wallet.TestnetUSDC

	// One bundle for every platform. MCPB picks a command per OS but not
	// per CPU, so on macOS a launcher picks the Intel or Apple Silicon
	// binary; Windows gets .exe appended to the default command.
	args := []string{
		"--grpc", testnetGRPC, "--rpc", testnetRPC, "--chain-id", "aether-testnet-1",
		"--keyring-dir", "${user_config.keyring_dir}",
		"--per-tx-limit", "${user_config.per_tx_limit}",
		"--daily-limit", "${user_config.daily_limit}",
		"--usdc-path", usdc.Path, "--usdc-base-denom", usdc.BaseDenom, "--usdc-issuer", usdc.Issuer,
		"--usdc-per-tx-limit", "${user_config.usdc_per_tx_limit}",
		"--usdc-daily-limit", "${user_config.usdc_daily_limit}",
	}
	s := &m.Server
	s.Type, s.EntryPoint = "binary", "server/agentmcp"
	s.MCPConfig.Command, s.MCPConfig.Args = "${__dirname}/server/agentmcp", args
	s.MCPConfig.PlatformOverrides = map[string]mcpbCommand{
		"win32":  {Command: "${__dirname}/server/agentmcp.exe", Args: args},
		"darwin": {Command: "/bin/sh", Args: append([]string{"${__dirname}/server/darwin/agentmcp.sh"}, args...)},
	}
	return m, nil
}

func buildServerJSON(ver, bundleURL, sha, remote string) (map[string]any, error) {
	if !strings.Contains(bundleURL, "mcp") {
		return nil, errors.New("the registry requires the bundle URL to contain \"mcp\"")
	}
	if len(sha) != 64 || strings.Trim(strings.ToLower(sha), "0123456789abcdef") != "" {
		return nil, fmt.Errorf("--sha256 must be 64 hex characters, got %q", sha)
	}
	remote, err := parseRemoteURL(remote)
	if err != nil {
		return nil, err
	}
	doc := map[string]any{
		"$schema":     "https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json",
		"name":        registryName,
		"title":       "Aether testnet wallet",
		"description": shortDescription,
		"version":     ver,
		"websiteUrl":  repoURL + "#ai-agents-start-here",
		"repository":  map[string]string{"url": repoURL, "source": "github"},
		"packages": []map[string]any{{
			"registryType": "mcpb",
			"identifier":   bundleURL,
			"version":      ver,
			"fileSha256":   strings.ToLower(sha),
			"transport":    map[string]string{"type": "stdio"},
		}},
	}
	// Empty by default. The live URL is filled in after the public
	// process is actually deployed; publishing it early would point
	// clients at a host that still serves the explorer SPA.
	if remote != "" {
		doc["remotes"] = []map[string]string{{
			"type": "streamable-http",
			"url":  remote,
		}}
	}
	return doc, nil
}

func runPackaging(args []string, out io.Writer) error {
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	ver := fs.String("version", "", "bundle version, e.g. 0.2.1-testnet (the release tag without its v)")
	url := fs.String("url", "", "server-json: the .mcpb file's release download URL")
	sha := fs.String("sha256", "", "server-json: the .mcpb file's SHA-256, hex")
	remote := fs.String("remote", "", "server-json: optional public Streamable HTTP URL; empty omits remotes")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *ver == "" || strings.HasPrefix(*ver, "v") {
		return fmt.Errorf("--version is required, without a leading v (got %q)", *ver)
	}
	var doc any
	var err error
	switch args[0] {
	case "mcpb-manifest":
		doc, err = buildMCPBManifest(context.Background(), *ver)
	case "server-json":
		doc, err = buildServerJSON(*ver, *url, *sha, *remote)
	}
	if err != nil {
		return err
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(doc)
}

func isPackagingCommand(args []string) bool {
	return len(args) > 1 && (args[1] == "mcpb-manifest" || args[1] == "server-json")
}

func runPackagingMain() {
	if err := runPackaging(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
