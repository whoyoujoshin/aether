package main

import (
	"fmt"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/tx"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/spf13/cobra"

	"github.com/whoyoujoshin/aether/wallet"
)

// applySignsFor lets `aetherd tx ... --from` send from an account whose
// key was rotated (docs/PQ-KEY-ROTATION.md). The SDK takes the sender
// from the key's own address, which after a rotation is not the account
// the key signs for. The wallet records that pairing in signs-for.json in
// the keyring directory (`wallet sign-for`); when --from names such a key,
// or the account itself, the transaction is sent from the account and
// signed by the key. The chain checks the signature against the key
// stored on the account, so a wrong record only gets the transaction
// refused.
//
// It runs before the command, so the SDK's own flag handling keeps the
// sender it sets: readTxCommandFlags leaves From alone unless the flag
// reads as changed.
func applySignsFor(cmd *cobra.Command) error {
	f := cmd.Flags().Lookup(flags.FlagFrom)
	if f == nil || !f.Changed || f.Value.String() == "" {
		return nil
	}
	ctx := client.GetClientContextFromCmd(cmd)
	from := f.Value.String()
	name, account, err := signsForRecord(ctx.KeyringDir, from)
	if err != nil || account == "" {
		return err
	}
	addr, err := sdk.AccAddressFromBech32(account)
	if err != nil {
		return err
	}
	if ctx.Keyring == nil {
		return fmt.Errorf("--from %s: no keyring to sign with", from)
	}
	if _, err := ctx.Keyring.Key(name); err != nil {
		return fmt.Errorf("--from %s: %s signs for %s (%s), but the keyring has no key %s: %w", from, name, account, wallet.SignsForFile, name, err)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "aetherd: key %s signs for %s (%s); sending from it\n", name, account, wallet.SignsForFile)
	f.Changed = false
	return client.SetCmdClientContext(cmd, ctx.WithFrom(name).WithFromName(name).WithFromAddress(addr))
}

// signsForRecord is the key name and account for from (a key name or an
// account address) in keyringDir's signs-for.json, if any.
func signsForRecord(keyringDir, from string) (name, account string, err error) {
	records, err := wallet.ReadSignsFor(keyringDir)
	if err != nil {
		return "", "", err
	}
	if a, ok := records[from]; ok {
		return from, a, nil
	}
	for n, a := range records {
		if a == from {
			return n, a, nil
		}
	}
	return "", "", nil
}

// wrapPositionalSenders handles the SDK commands that take their sender as
// the first argument and set --from from it inside their RunE, after
// applySignsFor has run: bank send sends from the rotated account itself;
// the others refuse a rotated key rather than send from the key's own
// address.
func wrapPositionalSenders(root *cobra.Command) {
	for _, c := range root.Commands() {
		wrapPositionalSenders(c)
		if c.RunE == nil || !takesPositionalSender(c.Use) {
			continue
		}
		orig, isSend := c.RunE, c.Name() == "send" && c.Parent() != nil && c.Parent().Name() == "bank"
		c.RunE = func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return orig(cmd, args)
			}
			name, account, err := signsForRecord(client.GetClientContextFromCmd(cmd).KeyringDir, args[0])
			if err != nil {
				return err
			}
			if account == "" {
				return orig(cmd, args)
			}
			if !isSend {
				return fmt.Errorf("%s signs for %s (%s), and `%s` can't send from a rotated account: use the wallet CLI", args[0], account, wallet.SignsForFile, cmd.CommandPath())
			}
			return rotatedBankSend(cmd, name, account, args)
		}
	}
}

func takesPositionalSender(use string) bool {
	var first string
	if _, err := fmt.Sscanf(use, "%s %s", new(string), &first); err != nil {
		return false
	}
	return len(first) > len("_key_or_address]") && first[len(first)-len("_key_or_address]"):] == "_key_or_address]"
}

// rotatedBankSend is the SDK's bank send (x/bank/client/cli, v0.50.14)
// from account, signed by the key name.
func rotatedBankSend(cmd *cobra.Command, name, account string, args []string) error {
	if err := cmd.Flags().Set(flags.FlagFrom, name); err != nil {
		return err
	}
	ctx, err := client.GetClientTxContext(cmd)
	if err != nil {
		return err
	}
	from, err := sdk.AccAddressFromBech32(account)
	if err != nil {
		return err
	}
	ctx = ctx.WithFrom(name).WithFromName(name).WithFromAddress(from)
	to, err := sdk.AccAddressFromBech32(args[1])
	if err != nil {
		return err
	}
	coins, err := sdk.ParseCoinsNormalized(args[2])
	if err != nil {
		return err
	}
	if len(coins) == 0 {
		return fmt.Errorf("invalid coins")
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "aetherd: key %s signs for %s (%s); sending from it\n", name, account, wallet.SignsForFile)
	return tx.GenerateOrBroadcastTxCLI(ctx, cmd.Flags(), banktypes.NewMsgSend(from, to, coins))
}
