package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cast"
	"github.com/spf13/cobra"

	"cosmossdk.io/log"
	cmtcfg "github.com/cometbft/cometbft/config"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client/keys"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/server"
	svrcmd "github.com/cosmos/cosmos-sdk/server/cmd"
	serverconfig "github.com/cosmos/cosmos-sdk/server/config"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
	genutilcli "github.com/cosmos/cosmos-sdk/x/genutil/client/cli"
	authcmd "github.com/cosmos/cosmos-sdk/x/auth/client/cli"
	powcli "github.com/whoyoujoshin/aether/x/pow/client/cli"
	"github.com/whoyoujoshin/aether/app"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/cosmos-sdk/client/rpc"
)

var encodingConfig = app.MakeEncodingConfig()

var initClientCtx = client.Context{}.
	WithCodec(encodingConfig.Codec).
	WithInterfaceRegistry(encodingConfig.InterfaceRegistry).
	WithTxConfig(encodingConfig.TxConfig).
	WithLegacyAmino(encodingConfig.Amino).
	WithInput(os.Stdin).
	WithBroadcastMode(flags.BroadcastSync).
	WithHomeDir(app.DefaultNodeHome).
	WithViper("").
	WithAccountRetriever(authtypes.AccountRetriever{})
func main() {
	rootCmd := &cobra.Command{
		Use:   "aetherd",
		Short: "Aether Network daemon",
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if err := client.SetCmdClientContextHandler(initClientCtx, cmd); err != nil {
				return err
			}
			customAppTemplate, customAppConfig := initAppConfig()
			return server.InterceptConfigsPreRunHandler(cmd, customAppTemplate, customAppConfig, cmtcfg.DefaultConfig())
		},
	}

	rootCmd.AddCommand(
	genutilcli.InitCmd(app.ModuleBasics, app.DefaultNodeHome),
	keys.Commands(),
)
server.AddCommands(
		rootCmd,
		app.DefaultNodeHome,
		createApp,
		nil,
		func(startCmd *cobra.Command) {},
	)

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
		powcli.NewTxCmd(),
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
		rpc.ValidatorCommand(),
		authcmd.QueryTxCmd(),
		authcmd.QueryTxsByEventsCmd(),
	)
	app.ModuleBasics.AddQueryCommands(queryCmd)
	rootCmd.AddCommand(queryCmd)

	if err := svrcmd.Execute(rootCmd, "AETHERD", app.DefaultNodeHome); err != nil {
		fmt.Fprintln(rootCmd.OutOrStderr(), err)
		os.Exit(1)
	}
}

func initAppConfig() (string, interface{}) {
	srvCfg := serverconfig.DefaultConfig()
	srvCfg.MinGasPrices = "0.0001aeth"
	return serverconfig.DefaultConfigTemplate, srvCfg
}

func createApp(
	logger log.Logger,
	db dbm.DB,
	traceStore io.Writer,
	appOpts servertypes.AppOptions,
) servertypes.Application {
	skipUpgradeHeights := make(map[int64]bool)
	for _, h := range cast.ToIntSlice(appOpts.Get(server.FlagUnsafeSkipUpgrades)) {
		skipUpgradeHeights[int64(h)] = true
	}

	homePath := cast.ToString(appOpts.Get(flags.FlagHome))
	invCheckPeriod := cast.ToUint(appOpts.Get(server.FlagInvCheckPeriod))
	baseAppOptions := server.DefaultBaseappOptions(appOpts)

	return app.New(
		logger,
		db,
		traceStore,
		true,
		skipUpgradeHeights,
		homePath,
		invCheckPeriod,
		appOpts,
		baseAppOptions...,
	)
}