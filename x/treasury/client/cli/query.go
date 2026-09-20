package cli

import (
	"context"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/spf13/cobra"

	"github.com/whoyoujoshin/aether/x/treasury"
)

func GetQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        treasury.ModuleName,
		Short:                      "Querying commands for the treasury module",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}

	cmd.AddCommand(
		GetBalanceCmd(),
	)

	return cmd
}

// GetBalanceCmd closes a real, previously-missing gap (Section 3 item
// 8): treasury had no query service at all, so its own internally-
// tracked balance couldn't even be checked externally, let alone
// checked against the module account's real bank balance.
func GetBalanceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "balance",
		Short: "Query the treasury's tracked ledger balance alongside its real bank balance, and whether they match",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			queryClient := treasury.NewQueryClient(clientCtx)
			res, err := queryClient.Balance(context.Background(), &treasury.QueryBalanceRequest{})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}
