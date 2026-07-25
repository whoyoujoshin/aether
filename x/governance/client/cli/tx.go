package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/tx"

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
	cmd.AddCommand(NewDepositCmd())
	cmd.AddCommand(NewVoteCmd())
	return cmd
}

func NewSubmitProposalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "submit-proposal [recipient] [amount] [deposit]",
		Short: "Submit a treasury-spend proposal, with an initial deposit",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
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

func NewDepositCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deposit [proposal-id] [amount]",
		Short: "Contribute additional deposit to an existing proposal",
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