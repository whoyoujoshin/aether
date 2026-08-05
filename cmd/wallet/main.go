// cmd/wallet/main.go
//
// A thin CLI layer over the wallet library (wallet/). Provides the
// commands a real user needs day-to-day: create an account, import
// one from a mnemonic, list accounts, check a balance, and send
// funds -- all backed by the same, already-tested account/Client/tx
// logic, not reimplemented here.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"cosmossdk.io/math"
	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/wallet"
)

var (
	keyringDir     string
	keyringBackend string
	grpcEndpoint   string
	chainID        string
)

func defaultHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".aether"
	}
	// Matches aetherd's own default home directory, so accounts
	// created via `aetherd keys add` and this tool share the same
	// keyring and are visible to both.
	return filepath.Join(home, ".aether")
}

func newWallet() (*wallet.Wallet, error) {
	registry := codectypes.NewInterfaceRegistry()
	mldsa.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)

	return wallet.NewWallet("aetherd", keyringBackend, keyringDir, cdc)
}

func main() {
	rootCmd := &cobra.Command{
		Use:   "wallet",
		Short: "Aether wallet CLI -- create accounts, check balances, and send funds",
	}

	rootCmd.PersistentFlags().StringVar(&keyringDir, "keyring-dir", defaultHomeDir(), "keyring storage directory")
	rootCmd.PersistentFlags().StringVar(&keyringBackend, "keyring-backend", "test", "keyring backend (test|file|os)")
	rootCmd.PersistentFlags().StringVar(&grpcEndpoint, "grpc", "localhost:9090", "node gRPC endpoint")
	rootCmd.PersistentFlags().StringVar(&chainID, "chain-id", "aether-testnet-1", "chain ID")

	rootCmd.AddCommand(
		newCreateCmd(),
		newImportCmd(),
		newListCmd(),
		newBalanceCmd(),
		newSendCmd(),
	)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func newCreateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new independent ML-DSA-44 account",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w, err := newWallet()
			if err != nil {
				return err
			}

			account, mnemonic, err := w.CreateAccount(args[0])
			if err != nil {
				return err
			}

			fmt.Printf("Account %q created.\n", account.Name)
			fmt.Printf("Address: %s\n", account.Address)
			fmt.Println("Note: Aether uses independent post-quantum (ML-DSA) keys.")
			fmt.Println("This key is not derived from a hierarchical path.")
			fmt.Println()
			fmt.Println("**IMPORTANT** write this mnemonic phrase in a safe place.")
			fmt.Println("It restores ONLY this key -- it does not support hierarchical")
			fmt.Println("multi-account derivation (unlike Bitcoin/Ethereum HD wallets).")
			fmt.Println()
			fmt.Println(mnemonic)
			return nil
		},
	}
}

func newImportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "import <name>",
		Short: "Recover the single account a mnemonic corresponds to",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w, err := newWallet()
			if err != nil {
				return err
			}

			fmt.Print("Enter your mnemonic: ")
			var mnemonic string
			if _, err := fmt.Scanln(&mnemonic); err != nil {
				return err
			}

			account, err := w.ImportAccount(args[0], mnemonic)
			if err != nil {
				return err
			}

			fmt.Printf("Account %q recovered.\n", account.Name)
			fmt.Printf("Address: %s\n", account.Address)
			return nil
		},
	}
}

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all accounts in the keyring",
		RunE: func(cmd *cobra.Command, args []string) error {
			w, err := newWallet()
			if err != nil {
				return err
			}

			accounts, err := w.ListAccounts()
			if err != nil {
				return err
			}

			if len(accounts) == 0 {
				fmt.Println("No accounts found.")
				return nil
			}

			fmt.Printf("%-20s %s\n", "NAME", "ADDRESS")
			for _, a := range accounts {
				fmt.Printf("%-20s %s\n", a.Name, a.Address)
			}
			return nil
		},
	}
}

func newBalanceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "balance <name-or-address>",
		Short: "Check an account's balance",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			address, err := resolveAddress(args[0])
			if err != nil {
				return err
			}

			client, err := wallet.NewClient(grpcEndpoint)
			if err != nil {
				return err
			}
			defer client.Close()

			balance, err := client.GetBalance(address)
			if err != nil {
				return err
			}

			if balance.IsZero() {
				fmt.Printf("%s has no balance.\n", address)
				return nil
			}

			fmt.Printf("Balance for %s:\n", address)
			for _, coin := range balance {
				fmt.Printf("  %s\n", coin.String())
			}
			return nil
		},
	}
}

func newSendCmd() *cobra.Command {
	var gasLimit uint64
	cmd := &cobra.Command{
		Use:   "send <from-name> <to-address> <amount>",
		Short: "Send funds (e.g. amount: 1000000uaeth)",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			fromName, toAddr, amountStr := args[0], args[1], args[2]

			w, err := newWallet()
			if err != nil {
				return err
			}
			fromAccount, err := w.GetAccount(fromName)
			if err != nil {
				return fmt.Errorf("failed to look up sender account %q: %w", fromName, err)
			}

			amount, err := sdk.ParseCoinsNormalized(amountStr)
			if err != nil {
				return fmt.Errorf("invalid amount %q: %w", amountStr, err)
			}

			client, err := wallet.NewClient(grpcEndpoint)
			if err != nil {
				return err
			}
			defer client.Close()

			accountNumber, sequence, err := client.GetAccountInfo(fromAccount.Address)
			if err != nil {
				return fmt.Errorf("failed to fetch sender account info (does the account exist on-chain yet?): %w", err)
			}

			signed, err := w.BuildAndSignSendTx(fromName, fromAccount.Address, toAddr, amount, wallet.TxParams{
				ChainID:       chainID,
				AccountNumber: accountNumber,
				Sequence:      sequence,
				GasLimit:      gasLimit,
				Fees:          sdk.NewCoins(sdk.NewCoin("uaeth", math.NewInt(0))),
			})
			if err != nil {
				return fmt.Errorf("failed to build/sign transaction: %w", err)
			}

			result, err := client.BroadcastTx(signed)
			if err != nil {
				return fmt.Errorf("failed to broadcast transaction: %w", err)
			}

			if result.Code != 0 {
				fmt.Printf("Transaction rejected on-chain (code %d): %s\n", result.Code, result.RawLog)
				return nil
			}

			fmt.Printf("Sent %s to %s.\n", amount.String(), toAddr)
			fmt.Printf("Tx hash: %s\n", result.TxHash)
			return nil
		},
	}
	cmd.Flags().Uint64Var(&gasLimit, "gas", 400_000, "gas limit -- ML-DSA-44 signatures need more than the SDK's typical default")
	return cmd
}

// resolveAddress accepts either a real bech32 address or a keyring
// account name, so `wallet balance mywallet` and
// `wallet balance cosmos1...` both work naturally.
func resolveAddress(nameOrAddress string) (string, error) {
	if _, err := sdk.AccAddressFromBech32(nameOrAddress); err == nil {
		return nameOrAddress, nil
	}

	w, err := newWallet()
	if err != nil {
		return "", err
	}
	account, err := w.GetAccount(nameOrAddress)
	if err != nil {
		return "", fmt.Errorf("%q is not a valid address and no account with that name was found: %w", nameOrAddress, err)
	}
	return account.Address, nil
}
