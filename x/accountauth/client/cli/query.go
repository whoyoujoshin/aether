package cli

import (
	"context"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/spf13/cobra"

	"github.com/whoyoujoshin/aether/x/accountauth"
)

func GetQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        accountauth.ModuleName,
		Short:                      "Querying commands for the account-abstraction module",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}

	cmd.AddCommand(GetAuthenticatorsCmd())

	return cmd
}

func GetAuthenticatorsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "authenticators [account]",
		Short: "Query every authenticator an account has registered",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			queryClient := accountauth.NewQueryClient(clientCtx)
			res, err := queryClient.Authenticators(context.Background(), &accountauth.QueryAuthenticatorsRequest{Account: args[0]})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}
