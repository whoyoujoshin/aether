package cli

import (
	"fmt"
	"os"
	"strconv"

	"cosmossdk.io/math"
	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/tx"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/x/governance"
)

func NewTxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        governance.ModuleName,
		Short:                      "Governance transaction subcommands",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	cmd.AddCommand(NewSubmitProposalCmd())
	cmd.AddCommand(NewSubmitParamChangeProposalCmd())
	cmd.AddCommand(NewDepositCmd())
	cmd.AddCommand(NewVoteCmd())
	return cmd
}

func NewSubmitProposalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "submit-proposal [recipient] [amount] [deposit]",
		Short: "Submit a treasury-spend proposal, with an initial deposit",
		Long: `Submit a treasury-spend proposal, with an initial deposit.

[amount] and [deposit] must both be a plain uaeth integer -- e.g. 5000000,
NOT a denom-suffixed coin string like 5000000uaeth. This has caused a real
submission mistake before: the amount was accepted as-is with no denom
suffix stripped, and would only have failed much later, at execution,
had the proposal passed.`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}

			if err := requirePlainUaethAmount("amount", args[1]); err != nil {
				return err
			}
			if err := requirePlainUaethAmount("deposit", args[2]); err != nil {
				return err
			}

			msg := &governance.MsgSubmitProposal{
				Proposer:  clientCtx.GetFromAddress().String(),
				Recipient: args[0],
				Amount:    args[1],
				Deposit:   args[2],
			}
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

// NewSubmitParamChangeProposalCmd is the generic path for proposing any
// authority-gated message on any module -- e.g. x/consensus's
// MsgUpdateParams, to change block.max_gas. See
// MsgSubmitParamChangeProposal's proto comment and
// ParamChangeGovernanceActivationHeight's doc comment for why this
// exists and why it is height-gated.
//
// [msg-json-file] must contain a single sdk.Msg in proto JSON form,
// including its "@type" field, e.g.:
//
//	{
//	  "@type": "/cosmos.consensus.v1.MsgUpdateParams",
//	  "authority": "<this chain's governance module account>",
//	  "block": {"max_bytes": "22020096", "max_gas": "10000000"},
//	  "evidence": {...},
//	  "validator": {"pub_key_types": ["ed25519"]}
//	}
//
// The message's authority/signer field is validated on-chain (not just
// here) to be exactly this chain's governance module account -- see
// SubmitParamChangeProposal's on-chain checks -- so this file cannot be
// used to grant anyone but governance itself the ability to execute it.
func NewSubmitParamChangeProposalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "submit-param-change-proposal [msg-json-file] [deposit]",
		Short: "Submit a proposal to execute an authority-gated message (e.g. x/consensus MsgUpdateParams) on any module, with an initial deposit",
		Long: `Submit a proposal to execute an authority-gated message on any module, with an initial deposit.

[msg-json-file] must be a JSON file containing a single Cosmos SDK message in
proto JSON form, including its "@type" field -- e.g. a
/cosmos.consensus.v1.MsgUpdateParams message to change block.max_gas. Its
authority/signer field will be checked on-chain: it must be exactly this
chain's governance module account, or the proposal will be rejected at
submission.

[deposit] must be a plain uaeth integer -- e.g. 5000000, NOT a
denom-suffixed coin string like 5000000uaeth.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}

			if err := requirePlainUaethAmount("deposit", args[1]); err != nil {
				return err
			}

			contents, err := os.ReadFile(args[0])
			if err != nil {
				return fmt.Errorf("could not read msg-json-file %q: %w", args[0], err)
			}

			var sdkMsg sdk.Msg
			if err := clientCtx.Codec.UnmarshalInterfaceJSON(contents, &sdkMsg); err != nil {
				return fmt.Errorf("could not parse msg-json-file as a registered sdk.Msg: %w", err)
			}

			executeMsg, err := codectypes.NewAnyWithValue(sdkMsg)
			if err != nil {
				return fmt.Errorf("could not pack execute_msg: %w", err)
			}

			msg := &governance.MsgSubmitParamChangeProposal{
				Proposer:   clientCtx.GetFromAddress().String(),
				Deposit:    args[1],
				ExecuteMsg: executeMsg,
			}
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

// requirePlainUaethAmount catches, client-side and before ever
// broadcasting, the exact real mistake that produced Proposal 1's
// malformed amount: passing a denom-suffixed coin string (e.g.
// "5000000uaeth") where a plain uaeth integer is expected. Both amount
// and deposit fields are parsed on-chain via math.NewIntFromString,
// which has no concept of a denom suffix -- this just surfaces that
// same rejection immediately and helpfully, instead of as an opaque
// on-chain error (or, before the on-chain validation fix, silently
// weeks later at execution).
func requirePlainUaethAmount(fieldName, value string) error {
	if _, ok := math.NewIntFromString(value); !ok {
		if coin, err := sdk.ParseCoinNormalized(value); err == nil {
			return fmt.Errorf("%s %q looks like a denom-suffixed coin string -- pass a plain uaeth integer instead, e.g. %s not %s", fieldName, value, coin.Amount.String(), value)
		}
		return fmt.Errorf("%s %q is not a valid integer", fieldName, value)
	}
	return nil
}

func NewDepositCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deposit [proposal-id] [amount]",
		Short: "Contribute additional deposit to an existing proposal",
		Long: `Contribute additional deposit to an existing proposal.

[amount] must be a plain uaeth integer -- e.g. 5000000, NOT a
denom-suffixed coin string like 5000000uaeth.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}

			proposalID, err := strconv.ParseUint(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid proposal-id: %w", err)
			}

			if err := requirePlainUaethAmount("amount", args[1]); err != nil {
				return err
			}

			msg := &governance.MsgDeposit{
				ProposalId: proposalID,
				Depositor:  clientCtx.GetFromAddress().String(),
				Amount:     args[1],
			}
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

func NewVoteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vote [proposal-id] [option]",
		Short: "Vote on a proposal in its voting period (option: yes|no|abstain|no_with_veto)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}

			proposalID, err := strconv.ParseUint(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid proposal-id: %w", err)
			}

			var option governance.VoteOption
			switch args[1] {
			case "yes":
				option = governance.VoteOption_VOTE_OPTION_YES
			case "no":
				option = governance.VoteOption_VOTE_OPTION_NO
			case "abstain":
				option = governance.VoteOption_VOTE_OPTION_ABSTAIN
			case "no_with_veto":
				option = governance.VoteOption_VOTE_OPTION_NO_WITH_VETO
			default:
				return fmt.Errorf("invalid option %q: must be one of yes|no|abstain|no_with_veto", args[1])
			}

			msg := &governance.MsgVote{
				ProposalId: proposalID,
				Voter:      clientCtx.GetFromAddress().String(),
				Option:     option,
			}
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}