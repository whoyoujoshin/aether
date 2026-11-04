package wallet

import (
	"fmt"
	"strings"
)

// A Network is what a tool needs to know about one of Aether's public
// networks to start on it without being told each part: its chain ID,
// its public endpoints, its faucet, and the USDC it accepts. Tools pick
// one by name (--network testnet|mainnet in agentmcp, `network` in the
// TypeScript and Python clients; clients/testdata/networks.json keeps
// them all the same). docs/MAINNET.md §6.
type Network struct {
	Name    string
	ChainID string
	// Public endpoints, "" where none is published yet: then a tool needs
	// them given, a node of one's own (localhost) by default.
	RPC  string
	GRPC string
	// Faucet is "" on a network with none (mainnet has none, §6).
	Faucet string
	// JobBoard is the job board host's /jobs URL, "" where none runs.
	JobBoard string
	// USDC is the USDC its tools accept. On mainnet the base denom and
	// issuer are known (Circle's USDC on Injective) but the path isn't:
	// the channel exists only once the path is opened and listed after
	// launch, so Path stays "" until then and tools know only AETH unless
	// given --usdc-path.
	USDC USDCSetting
	// Real is true where funds have real value: tools keep keys in an
	// encrypted keyring there, and nothing hands out free funds.
	Real bool
}

// The public networks.
var (
	TestnetNetwork = Network{
		Name:     "testnet",
		ChainID:  "aether-testnet-1",
		RPC:      "https://rpc.157-245-252-221.sslip.io",
		GRPC:     "grpc.157-245-252-221.sslip.io:443",
		Faucet:   "https://faucet.157-245-252-221.sslip.io/request",
		JobBoard: "https://explorer.157-245-252-221.sslip.io/jobs",
		USDC:     TestnetUSDC,
	}
	MainnetNetwork = Network{
		Name:    "mainnet",
		ChainID: "aether-1",
		USDC:    USDCSetting{BaseDenom: InjectiveMainnetUSDCBaseDenom, Issuer: "Injective"},
		Real:    true,
	}
)

// Networks lists the public networks, testnet first.
func Networks() []Network { return []Network{TestnetNetwork, MainnetNetwork} }

// NetworkByName is the network called name ("testnet", "mainnet") or
// with that chain ID.
func NetworkByName(name string) (Network, error) {
	for _, n := range Networks() {
		if strings.EqualFold(name, n.Name) || name == n.ChainID {
			return n, nil
		}
	}
	return Network{}, fmt.Errorf("unknown network %q: want testnet or mainnet", name)
}

// NetworkForChain is the public network with chainID, and false for any
// other chain (a devnet, a rehearsal): those get no public defaults.
func NetworkForChain(chainID string) (Network, bool) {
	for _, n := range Networks() {
		if n.ChainID == chainID {
			return n, true
		}
	}
	return Network{}, false
}

// USDCWith fills in what s leaves out from the network's USDC: given only
// a path on mainnet, it is Circle's USDC on Injective over that path, not
// Noble's uusdc, which is what a bare path otherwise means. A setting
// naming its own base denom or channel is left as it is.
func (n Network) USDCWith(s USDCSetting) USDCSetting {
	if s == (USDCSetting{}) {
		if n.USDC.Path == "" && n.USDC.Channel == "" {
			return USDCSetting{} // the network has no USDC path yet
		}
		return n.USDC
	}
	if s.Path != "" && s.BaseDenom == "" && s.Channel == "" && n.USDC.BaseDenom != "" {
		s.BaseDenom = n.USDC.BaseDenom
		if s.Issuer == "" {
			s.Issuer = n.USDC.Issuer
		}
	}
	return s
}
