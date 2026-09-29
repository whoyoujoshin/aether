package cli

import (
	"strconv"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/spf13/cobra"

	"github.com/whoyoujoshin/aether/x/escrow"
)

func GetQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        escrow.ModuleName,
		Short:                      "Querying commands for escrows",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	cmd.AddCommand(GetEscrowCmd(), GetEscrowsCmd())
	return cmd
}

func GetEscrowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show [id]",
		Short: "Show an open escrow (settled ones are gone: see their escrow_released / escrow_refunded events)",
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
			res, err := escrow.NewQueryClient(clientCtx).Escrow(cmd.Context(), &escrow.QueryEscrowRequest{Id: id})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

func GetEscrowsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list [address]",
		Short: "List open escrows an address is payer, payee or arbiter of",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			after, _ := cmd.Flags().GetUint64("after-id")
			limit, _ := cmd.Flags().GetUint32("limit")
			res, err := escrow.NewQueryClient(clientCtx).EscrowsByAddress(cmd.Context(), &escrow.QueryEscrowsByAddressRequest{
				Address: args[0], AfterId: after, Limit: limit,
			})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		},
	}
	cmd.Flags().Uint64("after-id", 0, "continue after this id (next_after_id from the previous page)")
	cmd.Flags().Uint32("limit", 100, "at most this many (max 100)")
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}
