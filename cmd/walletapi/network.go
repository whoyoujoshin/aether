package main

import (
	"flag"
	"fmt"
	"net/http"

	"github.com/whoyoujoshin/aether/wallet"
)

// --network testnet|mainnet (wallet.Network): the chain this wallet uses,
// with its public node, explorer and USDC unless the flags give them. The
// desktop app passes it from its Settings. Mainnet has no public node yet,
// so it needs --grpc and --rpc; it holds real funds, so the keyring must be
// the encrypted one.

// network is the network in use: "testnet", "mainnet", or "" when the
// flags name a chain directly.
var network string

// testnetExplorer is the testnet's explorer (no wallet.Network field: only
// the wallet links to it).
const testnetExplorer = "https://explorer.157-245-252-221.sslip.io"

func applyNetwork(fs *flag.FlagSet, name string, usdc *wallet.USDCSetting) error {
	if name == "" {
		if n, ok := wallet.NetworkForChain(chainID); ok {
			network = n.Name
		}
		return nil
	}
	n, err := wallet.NetworkByName(name)
	if err != nil {
		return fmt.Errorf("--network: %w", err)
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if set["chain-id"] && chainID != n.ChainID {
		return fmt.Errorf("--network %s is chain %s, but --chain-id is %s", n.Name, n.ChainID, chainID)
	}
	chainID, network = n.ChainID, n.Name
	for _, e := range []struct {
		flag, public string
		into         *string
	}{{"grpc", n.GRPC, &grpcEndpoint}, {"rpc", n.RPC, &rpcEndpoint}} {
		if set[e.flag] {
			continue
		}
		if e.public == "" {
			return fmt.Errorf("%s has no public node yet: give --grpc and --rpc for a node you trust", n.Name)
		}
		*e.into = e.public
	}
	if !set["explorer"] {
		explorerURL = ""
		if n.Name == wallet.TestnetNetwork.Name {
			explorerURL = testnetExplorer
		}
	}
	if usdc != nil {
		*usdc = n.USDCWith(*usdc)
	}
	if n.Real && keyringBackend == "test" {
		return fmt.Errorf("%s holds real funds: the wallet's keys must be in the encrypted keyring (--keyring-backend file)", n.Name)
	}
	return nil
}

// GET /api/network -> the network this wallet is on, for the window: its
// name, chain, whether funds are real, its explorer and faucet ("" where
// there is none).
func handleNetwork(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"name": network, "chainId": chainID, "real": false, "explorer": explorerURL, "faucet": ""}
	if n, ok := wallet.NetworkForChain(chainID); ok {
		out["real"], out["faucet"] = n.Real, n.Faucet
	}
	writeJSON(w, http.StatusOK, out)
}
