package main

import (
	"context"
	"fmt"
	"strings"
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
	flagHelicaseRefreshAfter    = "helicase.refresh-after"
)

func addHelicaseFlags(startCmd *cobra.Command) {
	startCmd.Flags().String(flagHelicaseCounterpartyRPC, "", "Helicase: RPC of the chain to relay IBC packets in from (it must index transactions); comma-separated for several chains; empty disables it")
	startCmd.Flags().String(flagHelicaseClientID, "", "Helicase: Aether's light client of that chain, e.g. 07-tendermint-0; comma-separated, in the same order, for several chains")
	startCmd.Flags().String(flagHelicaseAetherRPC, "http://127.0.0.1:26657", "Helicase: this node's own RPC")
	startCmd.Flags().Duration(flagHelicaseInterval, 2*time.Second, "Helicase: how often to look for packets to relay")
	startCmd.Flags().Duration(flagHelicaseRefreshAfter, helicase.DefaultRefreshAfter, "Helicase: update Aether's client of the other chain at least this often, even with no packets, so transfers with relative timeouts don't time out at once")
}

// startHelicase runs a Helicase worker in this node for each configured
// chain, and has the node propose their relay transactions.
func startHelicase(a *app.App, appOpts servertypes.AppOptions, logger log.Logger) {
	paths, err := helicasePaths(cast.ToString(appOpts.Get(flagHelicaseCounterpartyRPC)), cast.ToString(appOpts.Get(flagHelicaseClientID)))
	if err != nil {
		logger.Error("helicase not started", "err", err)
		return
	}
	var workers helicase.Sources
	for _, p := range paths {
		cfg := helicase.Config{
			CounterpartyRPC: p.rpc,
			ClientID:        p.clientID,
			AetherRPC:       cast.ToString(appOpts.Get(flagHelicaseAetherRPC)),
			Interval:        cast.ToDuration(appOpts.Get(flagHelicaseInterval)),
			RefreshAfter:    cast.ToDuration(appOpts.Get(flagHelicaseRefreshAfter)),
			MaxMsgs:         helicase.Split(len(paths)),
		}
		w, err := helicase.New(cfg, a.AppCodec(), a.GetTxConfig(), app.EncodeHelicaseTx, logger)
		if err != nil {
			logger.Error("helicase not started", "client", p.clientID, "err", err)
			return
		}
		workers = append(workers, w)
	}
	if len(workers) == 0 {
		return
	}
	a.SetHelicaseSource(workers)
	for _, w := range workers {
		go w.Run(context.Background())
	}
}

type helicasePath struct{ rpc, clientID string }

// helicasePaths pairs the comma-separated counterparty RPCs with the
// client IDs given in the same order. Both empty: Helicase is off.
func helicasePaths(rpcs, clientIDs string) ([]helicasePath, error) {
	split := func(s string) []string {
		var out []string
		for _, v := range strings.Split(s, ",") {
			if v = strings.TrimSpace(v); v != "" {
				out = append(out, v)
			}
		}
		return out
	}
	r, c := split(rpcs), split(clientIDs)
	if len(r) != len(c) {
		return nil, fmt.Errorf("%s lists %d chains but %s lists %d clients; give one client per chain, in the same order",
			flagHelicaseCounterpartyRPC, len(r), flagHelicaseClientID, len(c))
	}
	seen := map[string]bool{}
	var paths []helicasePath
	for i := range r {
		if seen[c[i]] {
			return nil, fmt.Errorf("%s lists %s twice", flagHelicaseClientID, c[i])
		}
		seen[c[i]] = true
		paths = append(paths, helicasePath{rpc: r[i], clientID: c[i]})
	}
	return paths, nil
}
