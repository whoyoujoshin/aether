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