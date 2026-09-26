package main

import (
	"fmt"

	"cosmossdk.io/x/feegrant"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	"github.com/cosmos/cosmos-sdk/x/authz"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/spf13/cobra"
)

// Query commands for bank, authz and feegrant. In SDK v0.50 these
// modules ship their query CLIs only through autocli, which aetherd
// doesn't wire up, so without these there is no `aetherd query` for
// balances or grants. Names and arguments match the SDK's autocli ones.

func moduleQueryCmd(name string, subs ...*cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:                        name,
		Short:                      "Querying commands for the " + name + " module",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	cmd.AddCommand(subs...)
	return cmd
}

// addressQueryCmd is a query command whose first len(roles) arguments
// are account addresses (checked before anything is sent); paginated
// adds --limit, --page-key and friends and passes the page to run.
func addressQueryCmd(use, short string, roles []string, maxArgs int, paginated bool,
	run func(clientCtx client.Context, cmd *cobra.Command, args []string, page *query.PageRequest) error,
) *cobra.Command {
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.RangeArgs(len(roles), maxArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			for i, role := range roles {
				if _, err := sdk.AccAddressFromBech32(args[i]); err != nil {
					return fmt.Errorf("invalid %s address %q: %w", role, args[i], err)
				}
			}
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			var page *query.PageRequest
			if paginated {
				if page, err = client.ReadPageRequest(cmd.Flags()); err != nil {
					return err
				}
			}
			return run(clientCtx, cmd, args, page)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	if paginated {
		flags.AddPaginationFlagsToCmd(cmd, use)
	}
	return cmd
}

func bankQueryCmd() *cobra.Command {
	balances := addressQueryCmd("balances [address]", "Query all of an account's balances", []string{"account"}, 1, true,
		func(clientCtx client.Context, cmd *cobra.Command, args []string, page *query.PageRequest) error {
			res, err := banktypes.NewQueryClient(clientCtx).AllBalances(cmd.Context(), &banktypes.QueryAllBalancesRequest{Address: args[0], Pagination: page})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		})
	balance := addressQueryCmd("balance [address] [denom]", "Query an account's balance of one denom", []string{"account"}, 2, false,
		func(clientCtx client.Context, cmd *cobra.Command, args []string, _ *query.PageRequest) error {
			if len(args) < 2 {
				return fmt.Errorf("give a denom, e.g. uaeth")
			}
			res, err := banktypes.NewQueryClient(clientCtx).Balance(cmd.Context(), &banktypes.QueryBalanceRequest{Address: args[0], Denom: args[1]})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		})
	return moduleQueryCmd(banktypes.ModuleName, balances, balance)
}

func authzQueryCmd() *cobra.Command {
	grants := addressQueryCmd("grants [granter] [grantee] [msg-type-url]",
		"Query the grants from granter to grantee, optionally only those for one message type", []string{"granter", "grantee"}, 3, true,
		func(clientCtx client.Context, cmd *cobra.Command, args []string, page *query.PageRequest) error {
			req := &authz.QueryGrantsRequest{Granter: args[0], Grantee: args[1], Pagination: page}
			if len(args) == 3 {
				req.MsgTypeUrl = args[2]
			}
			res, err := authz.NewQueryClient(clientCtx).Grants(cmd.Context(), req)
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		})
	byGranter := addressQueryCmd("grants-by-granter [granter]", "Query the grants an account has given", []string{"granter"}, 1, true,
		func(clientCtx client.Context, cmd *cobra.Command, args []string, page *query.PageRequest) error {
			res, err := authz.NewQueryClient(clientCtx).GranterGrants(cmd.Context(), &authz.QueryGranterGrantsRequest{Granter: args[0], Pagination: page})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		})
	byGrantee := addressQueryCmd("grants-by-grantee [grantee]", "Query the grants an account has received", []string{"grantee"}, 1, true,
		func(clientCtx client.Context, cmd *cobra.Command, args []string, page *query.PageRequest) error {
			res, err := authz.NewQueryClient(clientCtx).GranteeGrants(cmd.Context(), &authz.QueryGranteeGrantsRequest{Grantee: args[0], Pagination: page})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		})
	return moduleQueryCmd(authz.ModuleName, grants, byGranter, byGrantee)
}

func feegrantQueryCmd() *cobra.Command {
	grant := addressQueryCmd("grant [granter] [grantee]", "Query the fee allowance granter gives grantee", []string{"granter", "grantee"}, 2, false,
		func(clientCtx client.Context, cmd *cobra.Command, args []string, _ *query.PageRequest) error {
			res, err := feegrant.NewQueryClient(clientCtx).Allowance(cmd.Context(), &feegrant.QueryAllowanceRequest{Granter: args[0], Grantee: args[1]})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		})
	byGrantee := addressQueryCmd("grants-by-grantee [grantee]", "Query the fee allowances an account has received", []string{"grantee"}, 1, true,
		func(clientCtx client.Context, cmd *cobra.Command, args []string, page *query.PageRequest) error {
			res, err := feegrant.NewQueryClient(clientCtx).Allowances(cmd.Context(), &feegrant.QueryAllowancesRequest{Grantee: args[0], Pagination: page})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		})
	byGranter := addressQueryCmd("grants-by-granter [granter]", "Query the fee allowances an account has given", []string{"granter"}, 1, true,
		func(clientCtx client.Context, cmd *cobra.Command, args []string, page *query.PageRequest) error {
			res, err := feegrant.NewQueryClient(clientCtx).AllowancesByGranter(cmd.Context(), &feegrant.QueryAllowancesByGranterRequest{Granter: args[0], Pagination: page})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		})
	return moduleQueryCmd(feegrant.ModuleName, grant, byGrantee, byGranter)
}
