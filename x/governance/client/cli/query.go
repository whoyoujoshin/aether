package cli

import (
	"context"
	"strconv"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/spf13/cobra"

	"github.com/whoyoujoshin/aether/x/governance"
)

func GetQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        governance.ModuleName,
		Short:                      "Querying commands for the governance module",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}

	cmd.AddCommand(
		GetProposalCmd(),
		GetProposalsCmd(),
		GetParamsCmd(),
		GetVoteCmd(),
		GetVotesCmd(),
		GetTallyCmd(),
	)

	return cmd
}

func GetProposalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "proposal [proposal-id]",
		Short: "Query a single proposal by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			id, err := strconv.ParseUint(args[0], 10, 64)
			if err != nil {
				return err
			}
			queryClient := governance.NewQueryClient(clientCtx)
			res, err := queryClient.Proposal(context.Background(), &governance.QueryProposalRequest{ProposalId: id})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

func GetProposalsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "proposals",
		Short: "Query all proposals",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			queryClient := governance.NewQueryClient(clientCtx)
			res, err := queryClient.Proposals(context.Background(), &governance.QueryProposalsRequest{})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

func GetParamsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "params",
		Short: "Query governance parameters",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			queryClient := governance.NewQueryClient(clientCtx)
			res, err := queryClient.Params(context.Background(), &governance.QueryParamsRequest{})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetVoteCmd, GetVotesCmd, and GetTallyCmd close a real, previously-
// missing gap (Section 3 item 6): there was no way to query an
// individual vote or a tally breakdown directly -- verifying a
// proposal's outcome could only be inferred indirectly from its
// status.
func GetVoteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vote [proposal-id] [voter]",
		Short: "Query a single vote by proposal ID and voter address",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			id, err := strconv.ParseUint(args[0], 10, 64)
			if err != nil {
				return err
			}
			queryClient := governance.NewQueryClient(clientCtx)
			res, err := queryClient.Vote(context.Background(), &governance.QueryVoteRequest{ProposalId: id, Voter: args[1]})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

func GetVotesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "votes [proposal-id]",
		Short: "Query every vote cast on a proposal",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			id, err := strconv.ParseUint(args[0], 10, 64)
			if err != nil {
				return err
			}
			queryClient := governance.NewQueryClient(clientCtx)
			res, err := queryClient.Votes(context.Background(), &governance.QueryVotesRequest{ProposalId: id})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

func GetTallyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tally [proposal-id]",
		Short: "Query the current tally breakdown for a proposal, and the quorum threshold it's measured against",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			id, err := strconv.ParseUint(args[0], 10, 64)
			if err != nil {
				return err
			}
			queryClient := governance.NewQueryClient(clientCtx)
			res, err := queryClient.Tally(context.Background(), &governance.QueryTallyRequest{ProposalId: id})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}