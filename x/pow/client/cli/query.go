package cli

import (
	"context"
	"fmt"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/spf13/cobra"

	"github.com/whoyoujoshin/aether/x/pow"
)

func GetQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        pow.ModuleName,
		Short:                      "Querying commands for the pow module",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}

	cmd.AddCommand(
		GetDifficultyCmd(),
		GetBlockRewardCmd(),
		GetEscrowCmd(),
		GetBanStatusCmd(),
		GetActiveValidatorsCmd(),
		GetCurrentEpochCmd(),
	)

	return cmd
}

func GetDifficultyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "difficulty",
		Short: "Query the current PoW difficulty",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			queryClient := pow.NewQueryClient(clientCtx)
			res, err := queryClient.Difficulty(context.Background(), &pow.QueryDifficultyRequest{})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

func GetBlockRewardCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "block-reward",
		Short: "Query the current block reward",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			queryClient := pow.NewQueryClient(clientCtx)
			res, err := queryClient.BlockReward(context.Background(), &pow.QueryBlockRewardRequest{})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

func GetEscrowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "escrow [miner-address]",
		Short: "Query a miner's pending escrow balance and unlock height",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			queryClient := pow.NewQueryClient(clientCtx)
			res, err := queryClient.Escrow(context.Background(), &pow.QueryEscrowRequest{Miner: args[0]})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

func GetBanStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ban-status [miner-address]",
		Short: "Query whether a miner address is permanently banned",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			queryClient := pow.NewQueryClient(clientCtx)
			res, err := queryClient.BanStatus(context.Background(), &pow.QueryBanStatusRequest{Miner: args[0]})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

func GetActiveValidatorsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "active-validators",
		Short: "Query the currently active validator set",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			queryClient := pow.NewQueryClient(clientCtx)
			res, err := queryClient.ActiveValidators(context.Background(), &pow.QueryActiveValidatorsRequest{})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

func GetCurrentEpochCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "current-epoch",
		Short: "Query the current epoch number",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			queryClient := pow.NewQueryClient(clientCtx)
			res, err := queryClient.CurrentEpoch(context.Background(), &pow.QueryCurrentEpochRequest{})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

var _ = fmt.Sprintf // keep fmt import if unused elsewhere; remove if truly unnecessary