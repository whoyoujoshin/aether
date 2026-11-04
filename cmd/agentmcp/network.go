package main

import (
	"flag"
	"fmt"

	"github.com/whoyoujoshin/aether/wallet"
)

// --network testnet|mainnet: everything agentmcp needs to know about a
// public network without being told each part (wallet.Network,
// docs/CONNECT.md). It sets --chain-id, the public endpoints where some are
// published, and the USDC, unless the flag is given; a flag given wins.
// Without --network nothing changes: --chain-id and the other flags'
// defaults are what they always were.

// applyNetwork fills in, from the network called name, what the flags in
// fs left unset. usdc is the USDC flags' setting.
func applyNetwork(fs *flag.FlagSet, name string, usdc *wallet.USDCSetting) error {
	if name == "" {
		return nil
	}
	n, err := wallet.NetworkByName(name)
	if err != nil {
		return fmt.Errorf("--network: %w", err)
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if !set["chain-id"] {
		chainID = n.ChainID
	} else if chainID != n.ChainID {
		return fmt.Errorf("--network %s is chain %s, but --chain-id is %s", n.Name, n.ChainID, chainID)
	}
	if !set["grpc"] && n.GRPC != "" {
		grpcEndpoint = n.GRPC
	}
	if !set["rpc"] && n.RPC != "" {
		rpcEndpoint = n.RPC
	}
	if usdc != nil {
		*usdc = n.USDCWith(*usdc)
	}
	if n.Real && !set["keyring-backend"] {
		keyringBackend = "file"
	}
	return nil
}

// checkRealFundsKeyring refuses to sign on a network with real funds from
// the unencrypted test keyring (docs/MAINNET.md §3: keys off
// keyring-backend test), whatever named the chain. Testnets, devnets and
// rehearsals are left alone.
func checkRealFundsKeyring() error {
	n, ok := wallet.NetworkForChain(chainID)
	if !ok || !n.Real || keyringBackend != "test" {
		return nil
	}
	return fmt.Errorf("%s holds real funds: the agent's key must be in an encrypted keyring (--keyring-backend file, its passphrase in the file $AETHER_KEYRING_PASSPHRASE_FILE names; docs/SERVICE-KEYS.md), not the unencrypted test keyring", chainID)
}
