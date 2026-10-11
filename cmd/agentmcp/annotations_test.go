package main

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// listTools returns the tools server serves, as a client sees them.
func listTools(t *testing.T, server *mcp.Server) []*mcp.Tool {
	t.Helper()
	ctx := context.Background()
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, serverT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "annotations-test"}, nil).Connect(ctx, clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { cs.Close() })
	var tools []*mcp.Tool
	for tool, err := range cs.Tools(ctx, nil) {
		require.NoError(t, err)
		tools = append(tools, tool)
	}
	return tools
}

// Every tool a client sees carries a title and says whether it is
// read-only or destructive: Anthropic's connector directory requires it,
// and clients use it to decide which calls to confirm first.
func TestToolAnnotations(t *testing.T) {
	wallet := listTools(t, newServer())
	sandbox := listTools(t, newSandboxServer())
	names := map[string]bool{}
	for _, tool := range append(append([]*mcp.Tool{}, wallet...), sandbox...) {
		names[tool.Name] = true
	}
	require.Len(t, names, len(toolLabels), "every toolLabels entry is a tool the wallet or the test wallet server registers, and the reverse")
	var destructives []string
	for _, tool := range wallet {
		a := tool.Annotations
		require.NotNil(t, a, tool.Name)
		require.NotEmpty(t, tool.Title, tool.Name)
		require.Equal(t, tool.Title, a.Title, tool.Name)
		require.NotNil(t, a.DestructiveHint, "%s states destructiveHint explicitly: its default is true", tool.Name)
		require.False(t, a.ReadOnlyHint && *a.DestructiveHint, tool.Name)
		if *a.DestructiveHint {
			destructives = append(destructives, tool.Name)
		}
	}
	// What moves the agent's money out for good, and nothing else.
	require.ElementsMatch(t, []string{"send_aeth", "fetch_paid", "create_escrow", "release_escrow", "refund_escrow"}, destructives)

	// The test wallet server: what moves a test wallet's money is marked.
	require.Len(t, sandbox, len(sandboxToolNames))
	var sandboxDestructive []string
	for _, tool := range sandbox {
		require.NotEmpty(t, tool.Title, tool.Name)
		require.NotNil(t, tool.Annotations.DestructiveHint, tool.Name)
		if *tool.Annotations.DestructiveHint {
			sandboxDestructive = append(sandboxDestructive, tool.Name)
		}
	}
	require.ElementsMatch(t, []string{"send_from_test_wallet", "buy_service"}, sandboxDestructive)

	// The public endpoint holds no key: all of it is read-only.
	public := listTools(t, newPublicServer())
	require.Len(t, public, len(publicToolNames))
	for _, tool := range public {
		require.NotNil(t, tool.Annotations, tool.Name)
		require.True(t, tool.Annotations.ReadOnlyHint, tool.Name)
		require.False(t, *tool.Annotations.DestructiveHint, tool.Name)
		require.NotEmpty(t, tool.Title, tool.Name)
	}
}

func TestAnnotateUnknownToolPanics(t *testing.T) {
	require.Panics(t, func() { annotate(&mcp.Tool{Name: "not_a_tool"}) })
}
