package cli

import (
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/spf13/cobra"

	"github.com/whoyoujoshin/aether/x/inflowcap"
)

func GetQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        inflowcap.ModuleName,
		Short:                      "Querying commands for the IBC inflow cap",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	cmd.AddCommand(GetCapsCmd(), GetParamsCmd())
	return cmd
}

func GetCapsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "caps",
		Short: "Each listed token with its voucher denom, supply on Aether and the room left under its cap",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			res, err := inflowcap.NewQueryClient(clientCtx).Caps(cmd.Context(), &inflowcap.QueryCapsRequest{})
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
		Short: "The allowlist as set: tokens from other chains not on it are refused",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			res, err := inflowcap.NewQueryClient(clientCtx).Params(cmd.Context(), &inflowcap.QueryParamsRequest{})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}
