package cli

import (
	"encoding/hex"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/tx"

	"github.com/whoyoujoshin/aether/x/pow"
)

func NewTxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        pow.ModuleName,
		Short:                      "PoW transaction subcommands",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}

	cmd.AddCommand(NewSubmitPoWCmd())
	cmd.AddCommand(NewRegisterValidatorPubkeyCmd())

	return cmd
}

func NewSubmitPoWCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "submit [height] [timestamp] [prev-hash-hex] [merkle-root-hex] [nonce] [difficulty]",
		Short: "Submit a proof-of-work solution for a block",
		Args:  cobra.ExactArgs(6),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}

			height, err := strconv.ParseUint(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid height: %w", err)
			}
			timestamp, err := strconv.ParseInt(args[1], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid timestamp: %w", err)
			}
			prevHash, err := hex.DecodeString(args[2])
			if err != nil {
				return fmt.Errorf("invalid prev-hash hex: %w", err)
			}
			merkleRoot, err := hex.DecodeString(args[3])
			if err != nil {
				return fmt.Errorf("invalid merkle-root hex: %w", err)
			}
			nonce, err := strconv.ParseUint(args[4], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid nonce: %w", err)
			}
			difficulty, err := strconv.ParseUint(args[5], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid difficulty: %w", err)
			}

			msg := &pow.MsgSubmitPoW{
				Miner:      clientCtx.GetFromAddress().String(),
				Height:     height,
				Timestamp:  timestamp,
				PrevHash:   prevHash,
				MerkleRoot: merkleRoot,
				Nonce:      nonce,
				Difficulty: difficulty,
			}

			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}

	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

func NewRegisterValidatorPubkeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "register-validator-pubkey [consensus-pubkey-hex] [signature-hex]",
		Short: "Register the ed25519 consensus pubkey you control, proven via signature",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}

			consensusPubkey, err := hex.DecodeString(args[0])
			if err != nil {
				return fmt.Errorf("invalid consensus-pubkey hex: %w", err)
			}
			signature, err := hex.DecodeString(args[1])
			if err != nil {
				return fmt.Errorf("invalid signature hex: %w", err)
			}

			msg := &pow.MsgRegisterValidatorPubkey{
				Miner:           clientCtx.GetFromAddress().String(),
				ConsensusPubkey: consensusPubkey,
				Signature:       signature,
			}

			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}

	flags.AddTxFlagsToCmd(cmd)
	return cmd
}