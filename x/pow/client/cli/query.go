package cli

import (
	"context"
	"fmt"
	"strconv"

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
		GetParamsCmd(),
		GetValidatorInfoCmd(),
		GetMinerLeaderboardCmd(),
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

func GetParamsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "params",
		Short: "Query current x/pow parameters (only those with live keeper state -- see QueryParamsResponse's proto comment)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			queryClient := pow.NewQueryClient(clientCtx)
			res, err := queryClient.Params(context.Background(), &pow.QueryParamsRequest{})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

func GetValidatorInfoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validator-info",
		Short: "Query real tenure data for every active validator",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			queryClient := pow.NewQueryClient(clientCtx)
			res, err := queryClient.ValidatorInfo(context.Background(), &pow.QueryValidatorInfoRequest{})
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

func GetMinerLeaderboardCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "miner-leaderboard [epoch]",
		Short: "Query recorded mining work for an epoch, ranked highest-first (defaults to the current epoch)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			var epoch int64
			if len(args) == 1 {
				epoch, err = strconv.ParseInt(args[0], 10, 64)
				if err != nil {
					return fmt.Errorf("invalid epoch: %w", err)
				}
			}
			queryClient := pow.NewQueryClient(clientCtx)
			res, err := queryClient.MinerLeaderboard(context.Background(), &pow.QueryMinerLeaderboardRequest{Epoch: epoch})
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
