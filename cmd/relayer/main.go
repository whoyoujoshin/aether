// cmd/relayer drives the minimal, purpose-built relayer in package
// relayer against two real, independently-running processes: Aether
// and the standalone counterparty chain (cmd/counterpartyd). It is not
// a general-purpose relayer -- see relayer/chain.go's package doc.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/cosmos/cosmos-sdk/crypto/keyring"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/counterparty"
	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/relayer"
)

func main() {
	var (
		aetherRPC      = flag.String("aether-rpc", "http://127.0.0.1:26657", "Aether RPC address")
		aetherGRPC     = flag.String("aether-grpc", "127.0.0.1:9090", "Aether gRPC address")
		aetherChainID  = flag.String("aether-chain-id", "aether", "Aether chain ID")
		aetherHome     = flag.String("aether-home", os.ExpandEnv("$HOME/.aether"), "Aether keyring root dir")
		aetherKey      = flag.String("aether-key", "relayer", "Name of the relayer's key in Aether's keyring")
		aetherGasPrice = flag.String("aether-gas-prices", "", "Aether gas prices (e.g. 0.0001uaeth), empty if none required")

		cpartyRPC      = flag.String("cparty-rpc", "http://127.0.0.1:26557", "Counterparty RPC address")
		cpartyGRPC     = flag.String("cparty-grpc", "127.0.0.1:9080", "Counterparty gRPC address")
		cpartyChainID  = flag.String("cparty-chain-id", "counterparty", "Counterparty chain ID")
		cpartyHome     = flag.String("cparty-home", os.ExpandEnv("$HOME/.counterparty"), "Counterparty keyring root dir")
		cpartyKey      = flag.String("cparty-key", "relayer", "Name of the relayer's key in the counterparty's keyring")
		cpartyGasPrice = flag.String("cparty-gas-prices", "", "Counterparty gas prices, empty if none required")

		keyringBackend = flag.String("keyring-backend", "test", "keyring backend for both chains (test/file/os)")
	)
	flag.Parse()

	aetherEnc := app.MakeEncodingConfig()
	aetherKr, err := keyring.New("aetherd", *keyringBackend, *aetherHome, os.Stdin, aetherEnc.Codec, func(o *keyring.Options) {
		o.SupportedAlgos = append(o.SupportedAlgos, mldsa.Algo)
	})
	if err != nil {
		log.Fatalf("opening aether keyring: %v", err)
	}

	cpartyEnc := counterparty.MakeEncodingConfig()
	cpartyKr, err := keyring.New("counterpartyd", *keyringBackend, *cpartyHome, os.Stdin, cpartyEnc.Codec)
	if err != nil {
		log.Fatalf("opening counterparty keyring: %v", err)
	}

	aether, err := relayer.NewChain("aether", *aetherRPC, *aetherGRPC, *aetherChainID, app.Bech32MainPrefix,
		aetherEnc.Codec, aetherEnc.TxConfig, aetherKr, *aetherKey, *aetherGasPrice)
	if err != nil {
		log.Fatalf("connecting to aether: %v", err)
	}
	cparty, err := relayer.NewChain("counterparty", *cpartyRPC, *cpartyGRPC, *cpartyChainID, counterparty.Bech32Prefix,
		cpartyEnc.Codec, cpartyEnc.TxConfig, cpartyKr, *cpartyKey, *cpartyGasPrice)
	if err != nil {
		log.Fatalf("connecting to counterparty: %v", err)
	}

	aetherHeight, err := aether.LatestHeight()
	if err != nil {
		log.Fatalf("aether: %v", err)
	}
	cpartyHeight, err := cparty.LatestHeight()
	if err != nil {
		log.Fatalf("counterparty: %v", err)
	}
	fmt.Printf("aether tip: %d\ncounterparty tip: %d\n", aetherHeight, cpartyHeight)

	aetherUnbonding, err := relayer.AetherUnbondingPeriod(aether)
	if err != nil {
		log.Fatalf("querying aether's unbonding-period analog: %v", err)
	}
	fmt.Printf("aether bond-cooldown period: %s\n", aetherUnbonding)

	cpartyUnbonding, err := relayer.StakingUnbondingPeriod(cparty)
	if err != nil {
		log.Fatalf("querying counterparty's staking unbonding period: %v", err)
	}
	fmt.Printf("counterparty unbonding period: %s\n", cpartyUnbonding)

	// A 07-tendermint client on the counterparty tracking Aether's
	// consensus state must trust Aether within Aether's own real
	// bond-cooldown window.
	clientOnCparty, err := relayer.CreateClient(aether, cparty, aetherUnbonding)
	if err != nil {
		log.Fatalf("creating client for aether on counterparty: %v", err)
	}
	fmt.Printf("created client %s on counterparty, tracking aether\n", clientOnCparty)

	// And symmetrically, a client on Aether tracking the counterparty
	// must trust it within the counterparty's real staking unbonding
	// period.
	clientOnAether, err := relayer.CreateClient(cparty, aether, cpartyUnbonding)
	if err != nil {
		log.Fatalf("creating client for counterparty on aether: %v", err)
	}
	fmt.Printf("created client %s on aether, tracking counterparty\n", clientOnAether)
}
