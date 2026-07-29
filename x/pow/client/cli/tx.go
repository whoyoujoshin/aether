package cli

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"encoding/json"
	"os"

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
	cmd.AddCommand(NewSubmitAuxPowCmd())

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
	Miner: clientCtx.GetFromAddress().String(),
	Submission: &pow.MsgSubmitPoW_Native{
		Native: &pow.NativeSubmission{
			Height:     height,
			Timestamp:  timestamp,
			PrevHash:   prevHash,
			MerkleRoot: merkleRoot,
			Nonce:      nonce,
			Difficulty: difficulty,
		},
	},
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

func NewSubmitAuxPowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "submit-auxpow [json-file]",
		Short: "Submit a merged-mining (AuxPoW) proof from a JSON file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}

			raw, err := os.ReadFile(args[0])
			if err != nil {
				return fmt.Errorf("failed to read json file: %w", err)
			}

			var input auxPowJSONInput
			if err := json.Unmarshal(raw, &input); err != nil {
				return fmt.Errorf("failed to parse json: %w", err)
			}

			auxPowData, err := input.toAuxPowData()
			if err != nil {
				return fmt.Errorf("invalid auxpow data: %w", err)
			}

			msg := &pow.MsgSubmitPoW{
				Miner: clientCtx.GetFromAddress().String(),
				Submission: &pow.MsgSubmitPoW_AuxPow{
					AuxPow: auxPowData,
				},
			}
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

// auxPowJSONInput mirrors AuxPowData but with hex-encoded strings for
// binary fields, since JSON has no native byte-array type.
type auxPowJSONInput struct {
	ParentHeader   string           `json:"parent_header"`
	CoinbaseTx     string           `json:"coinbase_tx"`
	CoinbaseBranch merkleBranchJSON `json:"coinbase_branch"`
	ChainBranch    merkleBranchJSON `json:"chain_branch"`
	ChainNonce     uint32           `json:"chain_nonce"`
	AuxBlockHash   string           `json:"aux_block_hash"`
}

type merkleBranchJSON struct {
	Hashes []string `json:"hashes"`
	Index  uint32   `json:"index"`
}

func (m merkleBranchJSON) toMerkleBranch() (*pow.MerkleBranch, error) {
	hashes := make([][]byte, len(m.Hashes))
	for i, h := range m.Hashes {
		b, err := hex.DecodeString(h)
		if err != nil {
			return nil, fmt.Errorf("invalid hash hex at index %d: %w", i, err)
		}
		hashes[i] = b
	}
	return &pow.MerkleBranch{Hashes: hashes, Index: m.Index}, nil
}

func (input auxPowJSONInput) toAuxPowData() (*pow.AuxPowData, error) {
	parentHeader, err := hex.DecodeString(input.ParentHeader)
	if err != nil {
		return nil, fmt.Errorf("invalid parent_header hex: %w", err)
	}
	coinbaseTx, err := hex.DecodeString(input.CoinbaseTx)
	if err != nil {
		return nil, fmt.Errorf("invalid coinbase_tx hex: %w", err)
	}
	auxBlockHash, err := hex.DecodeString(input.AuxBlockHash)
	if err != nil {
		return nil, fmt.Errorf("invalid aux_block_hash hex: %w", err)
	}
	coinbaseBranch, err := input.CoinbaseBranch.toMerkleBranch()
	if err != nil {
		return nil, fmt.Errorf("invalid coinbase_branch: %w", err)
	}
	chainBranch, err := input.ChainBranch.toMerkleBranch()
	if err != nil {
		return nil, fmt.Errorf("invalid chain_branch: %w", err)
	}

	return &pow.AuxPowData{
		ParentHeader:   parentHeader,
		CoinbaseTx:     coinbaseTx,
		CoinbaseBranch: coinbaseBranch,
		ChainBranch:    chainBranch,
		ChainNonce:     input.ChainNonce,
		AuxBlockHash:   auxBlockHash,
	}, nil
}