package cli

import (
	"context"
	"encoding/hex"
	"fmt"
	"strconv"
	"encoding/json"
	"os"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/tx"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/whoyoujoshin/aether/x/governance"
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
	cmd.AddCommand(NewDraftUpdateParamsCmd())

	return cmd
}

// NewDraftUpdateParamsCmd fetches x/pow's current live parameters and
// writes a ready-to-submit MsgUpdateParams JSON file, overriding only
// the fields the caller explicitly flags -- so proposing a change to
// one parameter never risks silently reverting the other four to some
// stale or guessed value (MsgUpdateParams is a full-replace message;
// see its own proto comment). The output feeds directly into
// x/governance's existing generic
// `tx governance submit-param-change-proposal` command -- no new
// governance-side CLI or code was needed for this module to gain a
// governance-adjustable parameter path.
func NewDraftUpdateParamsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "draft-update-params [output-file]",
		Short: "Fetch current x/pow parameters and write a MsgUpdateParams JSON file, applying any flag overrides",
		Long: `Fetch current x/pow parameters and write a MsgUpdateParams JSON file ready
to hand to "tx governance submit-param-change-proposal".

By default every field is carried over unchanged from the current live
value (queried from the chain, not guessed or hardcoded). Pass one or
more override flags to change specific fields -- e.g.:

  aetherd tx pow draft-update-params params.json --bond-cooldown 4320

then:

  aetherd tx governance submit-param-change-proposal params.json <deposit> --from <key> ...`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}

			queryClient := pow.NewQueryClient(clientCtx)
			current, err := queryClient.Params(context.Background(), &pow.QueryParamsRequest{})
			if err != nil {
				return fmt.Errorf("could not fetch current live x/pow params: %w", err)
			}

			epochLength := current.EpochLength
			if cmd.Flags().Changed("epoch-length") {
				epochLength, _ = cmd.Flags().GetInt64("epoch-length")
			}
			topKSize := current.TopKSize
			if cmd.Flags().Changed("top-k-size") {
				topKSize, _ = cmd.Flags().GetInt64("top-k-size")
			}
			bondCooldown := current.BondCooldown
			if cmd.Flags().Changed("bond-cooldown") {
				bondCooldown, _ = cmd.Flags().GetInt64("bond-cooldown")
			}
			recencyWindowK := current.RecencyWindowK
			if cmd.Flags().Changed("recency-window-k") {
				recencyWindowK, _ = cmd.Flags().GetInt64("recency-window-k")
			}
			beaconRoundsPerBlock := current.BeaconRoundsPerBlock
			if cmd.Flags().Changed("beacon-rounds-per-block") {
				beaconRoundsPerBlock, _ = cmd.Flags().GetInt64("beacon-rounds-per-block")
			}

			authority := authtypes.NewModuleAddress(governance.ModuleName).String()

			// int64 fields are written as JSON strings, matching the
			// proto3 canonical JSON mapping that
			// UnmarshalInterfaceJSON expects (the same convention used
			// for the x/consensus MsgUpdateParams example this
			// mechanism was originally built against).
			doc := map[string]string{
				"@type":                   "/aether.pow.v1.MsgUpdateParams",
				"authority":               authority,
				"epoch_length":            strconv.FormatInt(epochLength, 10),
				"top_k_size":              strconv.FormatInt(topKSize, 10),
				"bond_cooldown":           strconv.FormatInt(bondCooldown, 10),
				"recency_window_k":        strconv.FormatInt(recencyWindowK, 10),
				"beacon_rounds_per_block": strconv.FormatInt(beaconRoundsPerBlock, 10),
			}

			bz, err := json.MarshalIndent(doc, "", "  ")
			if err != nil {
				return err
			}
			if err := os.WriteFile(args[0], bz, 0o644); err != nil {
				return fmt.Errorf("could not write %s: %w", args[0], err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s. Submit it with:\n  aetherd tx governance submit-param-change-proposal %s <deposit> --from <key> --chain-id <chain-id>\n", args[0], args[0])
			return nil
		},
	}
	cmd.Flags().Int64("epoch-length", 0, "override EpochLength (default: current live value)")
	cmd.Flags().Int64("top-k-size", 0, "override TopKSize (default: current live value)")
	cmd.Flags().Int64("bond-cooldown", 0, "override BondCooldown (default: current live value)")
	cmd.Flags().Int64("recency-window-k", 0, "override RecencyWindowK (default: current live value)")
	cmd.Flags().Int64("beacon-rounds-per-block", 0, "override BeaconRoundsPerBlock (default: current live value)")
	flags.AddQueryFlagsToCmd(cmd)
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