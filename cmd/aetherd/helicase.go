package main

import (
	"context"
	"time"

	"cosmossdk.io/log"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
	"github.com/spf13/cast"
	"github.com/spf13/cobra"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/helicase"
)

// Helicase settings: flags on `aetherd start`, or the same keys under
// [helicase] in app.toml. A node with no counterparty configured
// proposes no relay transactions but still validates other proposers'.
const (
	flagHelicaseCounterpartyRPC = "helicase.counterparty-rpc"
	flagHelicaseClientID        = "helicase.client-id"
	flagHelicaseAetherRPC       = "helicase.aether-rpc"
	flagHelicaseInterval        = "helicase.interval"
)

func addHelicaseFlags(startCmd *cobra.Command) {
	startCmd.Flags().String(flagHelicaseCounterpartyRPC, "", "Helicase: RPC of the chain to relay IBC packets in from (it must index transactions); empty disables it")
	startCmd.Flags().String(flagHelicaseClientID, "", "Helicase: Aether's light client of that chain, e.g. 07-tendermint-0")
	startCmd.Flags().String(flagHelicaseAetherRPC, "http://127.0.0.1:26657", "Helicase: this node's own RPC")
	startCmd.Flags().Duration(flagHelicaseInterval, 2*time.Second, "Helicase: how often to look for packets to relay")
}

// startHelicase runs the Helicase worker in this node if one is
// configured, and has the node propose its relay transactions.
func startHelicase(a *app.App, appOpts servertypes.AppOptions, logger log.Logger) {
	rpc := cast.ToString(appOpts.Get(flagHelicaseCounterpartyRPC))
	if rpc == "" {
		return
	}
	cfg := helicase.Config{
		CounterpartyRPC: rpc,
		ClientID:        cast.ToString(appOpts.Get(flagHelicaseClientID)),
		AetherRPC:       cast.ToString(appOpts.Get(flagHelicaseAetherRPC)),
		Interval:        cast.ToDuration(appOpts.Get(flagHelicaseInterval)),
	}
	w, err := helicase.New(cfg, a.AppCodec(), a.GetTxConfig(), app.EncodeHelicaseTx, logger)
	if err != nil {
		logger.Error("helicase not started", "err", err)
		return
	}
	a.SetHelicaseSource(w)
	go w.Run(context.Background())
}
