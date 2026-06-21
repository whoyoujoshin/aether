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

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/server"
	svrcmd "github.com/cosmos/cosmos-sdk/server/cmd"
	serverconfig "github.com/cosmos/cosmos-sdk/server/config"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
	genutilcli "github.com/cosmos/cosmos-sdk/x/genutil/client/cli"

	"github.com/whoyoujoshin/aether/app"
)

var initClientCtx = client.Context{}.
	WithHomeDir(app.DefaultNodeHome)

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

	// genesis/init commands
	rootCmd.AddCommand(
		genutilcli.InitCmd(app.ModuleBasics, app.DefaultNodeHome),
	)

	// start/export/comet/etc.
	server.AddCommands(
		rootCmd,
		app.DefaultNodeHome,
		createApp,
		nil,                               // appExport - not wired up yet
		func(startCmd *cobra.Command) {},   // addStartFlags - must not be nil
	)

	if err := svrcmd.Execute(rootCmd, "AETHERD", app.DefaultNodeHome); err != nil {
		fmt.Fprintln(rootCmd.OutOrStderr(), err)
		os.Exit(1)
	}
}

func initAppConfig() (string, interface{}) {
	srvCfg := serverconfig.DefaultConfig()
	srvCfg.MinGasPrices = "0.0001aeth" // <- swap for your chain's actual fee denom
	return serverconfig.DefaultConfigTemplate, srvCfg
}

// createApp matches servertypes.AppCreator exactly. It pulls the extra
// options app.New() wants out of appOpts instead of taking them as args.
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
		true, // loadLatest
		skipUpgradeHeights,
		homePath,
		invCheckPeriod,
		appOpts,
		baseAppOptions...,
	)
}