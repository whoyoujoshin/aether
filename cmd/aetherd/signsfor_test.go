package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/wallet"
)

// A tx command run with --from <rotated key> (or the account) gets the
// account as its sender through the SDK's own GetClientTxContext; other
// keys are untouched.
func TestApplySignsFor(t *testing.T) {
	dir := t.TempDir()
	registry := codectypes.NewInterfaceRegistry()
	mldsa.RegisterInterfaces(registry)
	w, err := wallet.NewWallet("aetherd", "test", dir, codec.NewProtoCodec(registry))
	require.NoError(t, err)
	owner, _, err := w.CreateAccount("owner")
	require.NoError(t, err)
	next, _, err := w.CreateAccount("next")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, wallet.SignsForFile), []byte(fmt.Sprintf(`{"next":%q}`, owner.Address)), 0o600))

	run := func(from string) (client.Context, string) {
		var got client.Context
		var stderr bytes.Buffer
		cmd := &cobra.Command{Use: "x", RunE: func(cmd *cobra.Command, _ []string) error {
			var err error
			got, err = client.GetClientTxContext(cmd)
			return err
		}}
		flags.AddTxFlagsToCmd(cmd)
		cmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
			if err := client.SetCmdClientContextHandler(initClientCtx, cmd); err != nil {
				return err
			}
			return applySignsFor(cmd)
		}
		cmd.SetErr(&stderr)
		cmd.SetArgs([]string{"--from", from, "--keyring-backend", "test", "--keyring-dir", dir, "--chain-id", "aether-rc-x"})
		require.NoError(t, cmd.ExecuteContext(context.Background()))
		return got, stderr.String()
	}

	for _, from := range []string{"next", owner.Address} {
		ctx, stderr := run(from)
		require.Equal(t, owner.Address, ctx.GetFromAddress().String(), from)
		require.Equal(t, "next", ctx.GetFromName(), "signed by the key")
		require.Contains(t, stderr, "signs for "+owner.Address)
	}
	ctx, stderr := run("owner")
	require.Equal(t, owner.Address, ctx.GetFromAddress().String())
	require.Equal(t, "owner", ctx.GetFromName())
	require.Empty(t, stderr, "a key with no record is the SDK's own path")
	_ = next
}

func TestTakesPositionalSender(t *testing.T) {
	for use, want := range map[string]bool{
		"send [from_key_or_address] [to_address] [amount]":    true,
		"multi-send [from_key_or_address] [to_address_1] ...": true,
		"grant [granter_key_or_address] [grantee]":            true,
		"grant [grantee] [authorization_type]":                false,
		"submit":                                              false,
	} {
		require.Equal(t, want, takesPositionalSender(use), use)
	}
}
