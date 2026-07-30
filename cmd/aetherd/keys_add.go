package main

import (
	"bufio"
	"errors"

	"github.com/cosmos/go-bip39"
	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/input"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
)

const mnemonicEntropySize = 256

// NewAddMLDSAKeyCmd overrides the stock `keys add` command. The real
// stock command (client/keys/add.go) always constructs a concrete
// BIP-44 HD path internally before calling kb.NewAccount -- there is
// no flag or code path that skips this, for any algo. Since ML-DSA-44
// is the only key type Aether ever accepts (see pq-signatures-
// decision.md: mandatory from genesis, no classical fallback), and
// genuine HD derivation is not possible for ML-DSA at all (see
// crypto/mldsa/algo.go's own documentation), every real Aether user
// would otherwise need to discover and always pass a special flag the
// stock command was never designed to expose cleanly. This is a
// smaller, purpose-built replacement handling the core case (generate
// or recover a single independent key) -- multisig, Ledger, and
// pubkey-import are deliberately out of scope for this command, as
// they are secondary conveniences, not part of the core "create my
// key" flow.
func NewAddMLDSAKeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a new independent ML-DSA-44 key",
		Long: `Generate (or recover) a single, independent ML-DSA-44 keypair.

Aether uses independent post-quantum keys: each account is its own
separate keypair, not part of a hierarchical derivation tree. A
mnemonic backs up exactly ONE key -- it does not derive multiple
accounts the way Bitcoin/Ethereum-style HD wallets do.`,
		Args: cobra.ExactArgs(1),
		RunE: runAddMLDSAKey,
	}

	f := cmd.Flags()
	f.Bool("recover", false, "Provide a mnemonic to recover an existing key instead of creating a new one")
	f.Bool("no-backup", false, "Don't print the mnemonic (if others are watching the terminal)")
	f.Bool(flags.FlagDryRun, false, "Perform the action, but don't add the key to the local keystore")

	return cmd
}

func runAddMLDSAKey(cmd *cobra.Command, args []string) error {
	clientCtx, err := client.GetClientQueryContext(cmd)
	if err != nil {
		return err
	}

	name := args[0]
	kb := clientCtx.Keyring

	recoverFlag, _ := cmd.Flags().GetBool("recover")
	noBackup, _ := cmd.Flags().GetBool("no-backup")
	dryRun, _ := cmd.Flags().GetBool(flags.FlagDryRun)
	showMnemonic := !noBackup

	if dryRun {
		kb = keyring.NewInMemory(clientCtx.Codec)
	}

	inBuf := bufio.NewReader(clientCtx.Input)

	var mnemonic string
	if recoverFlag {
		mnemonic, err = input.GetString("Enter your bip39 mnemonic", inBuf)
		if err != nil {
			return err
		}
		if !bip39.IsMnemonicValid(mnemonic) {
			return errors.New("invalid mnemonic")
		}
	} else {
		entropySeed, err := bip39.NewEntropy(mnemonicEntropySize)
		if err != nil {
			return err
		}
		mnemonic, err = bip39.NewMnemonic(entropySeed)
		if err != nil {
			return err
		}
	}

	// Always an empty HD path -- ML-DSA-44 has no real derivation tree
	// to walk. mldsa.Algo.Derive() would reject anything else anyway;
	// passing "" directly here is what actually lets it succeed.
	record, err := kb.NewAccount(name, mnemonic, "", "", mldsa.Algo)
	if err != nil {
		return err
	}

	addr, err := record.GetAddress()
	if err != nil {
		return err
	}

	cmd.Println()
	cmd.Printf("Key %q created.\n", name)
	cmd.Printf("Address: %s\n", addr.String())
	cmd.Println("Note: Aether uses independent post-quantum (ML-DSA) keys.")
	cmd.Println("This key is not derived from a hierarchical path.")

	if recoverFlag {
		return nil
	}

	if showMnemonic {
		cmd.Println()
		cmd.Println("**IMPORTANT** write this mnemonic phrase in a safe place.")
		cmd.Println("It restores ONLY this key -- it does not support hierarchical")
		cmd.Println("multi-account derivation (unlike Bitcoin/Ethereum HD wallets).")
		cmd.Println()
		cmd.Println(mnemonic)
	}

	return nil
}