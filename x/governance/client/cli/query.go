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