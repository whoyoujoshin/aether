// cmd/counterpartyd is a standard, entirely ordinary Cosmos SDK chain
// daemon -- real x/staking, standard secp256k1 keys -- whose only
// purpose is to be a genuinely separate, independently-running IBC
// counterparty for testing Aether's IBC wiring with a real relayer
// (see counterparty/app.go and docs/IBC.md).
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"cosmossdk.io/log"
	cmtcfg "github.com/cometbft/cometbft/config"
	addresscodec "github.com/cosmos/cosmos-sdk/codec/address"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/keys"
	authcmd "github.com/cosmos/cosmos-sdk/x/auth/client/cli"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	bankcli "github.com/cosmos/cosmos-sdk/x/bank/client/cli"
	genutilcli "github.com/cosmos/cosmos-sdk/x/genutil/client/cli"
	stakingcli "github.com/cosmos/cosmos-sdk/x/staking/client/cli"

	ibccli "github.com/cosmos/ibc-go/v8/modules/core/client/cli"
	ibctransfercli "github.com/cosmos/ibc-go/v8/modules/apps/transfer/client/cli"

	"github.com/cosmos/cosmos-sdk/server"
	svrcmd "github.com/cosmos/cosmos-sdk/server/cmd"
	serverconfig "github.com/cosmos/cosmos-sdk/server/config"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"

	"github.com/whoyoujoshin/aether/counterparty"
)

var encodingConfig = counterparty.MakeEncodingConfig()

var initClientCtx = client.Context{}.
	WithCodec(encodingConfig.Codec).
	WithInterfaceRegistry(encodingConfig.InterfaceRegistry).
	WithTxConfig(encodingConfig.TxConfig).
	WithLegacyAmino(encodingConfig.Amino).
	WithInput(os.Stdin).
	WithBroadcastMode(flags.BroadcastSync).
	WithHomeDir(counterparty.DefaultNodeHome).
	WithViper("").
	WithAccountRetriever(authtypes.AccountRetriever{})

func init() {
	counterparty.SetAddressPrefixes()
}

func main() {
	rootCmd := &cobra.Command{
		Use:   "counterpartyd",
		Short: "Counterparty chain daemon (standard secp256k1, for real IBC relayer testing against Aether)",
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if err := client.SetCmdClientContextHandler(initClientCtx, cmd); err != nil {
				return err
			}
			srvCfg := serverconfig.DefaultConfig()
			return server.InterceptConfigsPreRunHandler(cmd, serverconfig.DefaultConfigTemplate, srvCfg, cmtcfg.DefaultConfig())
		},
	}

	rootCmd.AddCommand(
		genutilcli.InitCmd(counterparty.ModuleBasics, counterparty.DefaultNodeHome),
		genutilcli.Commands(encodingConfig.TxConfig, counterparty.ModuleBasics, counterparty.DefaultNodeHome),
		keys.Commands(),
	)
	server.AddCommands(rootCmd, counterparty.DefaultNodeHome, createApp, nil, func(startCmd *cobra.Command) {})

	txCmd := &cobra.Command{
		Use:                        "tx",
		Short:                      "Transactions subcommands",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	txCmd.AddCommand(
		authcmd.GetSignCommand(),
		authcmd.GetBroadcastCommand(),
		bankcli.NewTxCmd(addresscodec.NewBech32Codec(counterparty.Bech32Prefix)),
		stakingcli.NewTxCmd(addresscodec.NewBech32Codec(counterparty.Bech32Prefix+"valoper"), addresscodec.NewBech32Codec(counterparty.Bech32Prefix)),
		ibccli.GetTxCmd(),
		ibctransfercli.NewTxCmd(),
	)
	rootCmd.AddCommand(txCmd)

	queryCmd := &cobra.Command{
		Use:                        "query",
		Aliases:                    []string{"q"},
		Short:                      "Querying subcommands",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	queryCmd.AddCommand(
		authcmd.QueryTxCmd(),
		authcmd.QueryTxsByEventsCmd(),
	)
	counterparty.ModuleBasics.AddQueryCommands(queryCmd)
	rootCmd.AddCommand(queryCmd)

	if err := svrcmd.Execute(rootCmd, "COUNTERPARTYD", counterparty.DefaultNodeHome); err != nil {
		fmt.Fprintln(rootCmd.OutOrStderr(), err)
		os.Exit(1)
	}
}

func createApp(logger log.Logger, db dbm.DB, traceStore io.Writer, appOpts servertypes.AppOptions) servertypes.Application {
	baseAppOptions := server.DefaultBaseappOptions(appOpts)
	return counterparty.New(logger, db, traceStore, true, appOpts, baseAppOptions...)
}
