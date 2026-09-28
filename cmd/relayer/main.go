// cmd/relayer drives the minimal, purpose-built relayer in package
// relayer against two real, independently-running processes: Aether
// and a counterparty. The "counterparty" encoding config (package
// counterparty) registers nothing but standard Cosmos SDK + ibc-go
// modules, so -cparty-* flags can point it at either the standalone
// test chain (cmd/counterpartyd, the default) or any real external
// standard Cosmos SDK chain (e.g. Osmosis: -cparty-bech32-prefix osmo
// plus its real rpc/grpc/chain-id). It is still not a general-purpose
// relayer beyond that -- see relayer/chain.go's package doc.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"

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
		cpartyBech32   = flag.String("cparty-bech32-prefix", counterparty.Bech32Prefix, "Counterparty chain's bech32 address prefix (e.g. \"osmo\" for Osmosis) -- counterparty's own encoding config is entirely standard Cosmos SDK + ibc-go otherwise, so any standard external chain works by just changing this and the endpoints/chain-id above")

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

	cpartyEnc := counterparty.MakeEncodingConfig(*cpartyBech32)
	cpartyKr, err := keyring.New("counterpartyd", *keyringBackend, *cpartyHome, os.Stdin, cpartyEnc.Codec)
	if err != nil {
		log.Fatalf("opening counterparty keyring: %v", err)
	}

	aether, err := relayer.NewChain("aether", *aetherRPC, *aetherGRPC, *aetherChainID, app.Bech32MainPrefix,
		aetherEnc.Codec, aetherEnc.TxConfig, aetherKr, *aetherKey, *aetherGasPrice)
	if err != nil {
		log.Fatalf("connecting to aether: %v", err)
	}
	cparty, err := relayer.NewChain("counterparty", *cpartyRPC, *cpartyGRPC, *cpartyChainID, *cpartyBech32,
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
	fmt.Printf("aether unbonding period (bond cooldown x measured block time): %s\n", aetherUnbonding)

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

	connAether, connCparty, err := relayer.OpenConnection(aether, cparty, clientOnAether, clientOnCparty)
	if err != nil {
		log.Fatalf("connection handshake: %v", err)
	}
	for _, side := range []struct {
		chain *relayer.Chain
		conn  string
	}{{aether, connAether}, {cparty, connCparty}} {
		state, err := relayer.ConnectionState(side.chain, side.conn)
		if err != nil {
			log.Fatalf("querying %s connection %s: %v", side.chain.Name, side.conn, err)
		}
		fmt.Printf("connection %s on %s: %s\n", side.conn, side.chain.Name, state)
	}

	chanAether, chanCparty, err := relayer.OpenTransferChannel(aether, cparty, clientOnAether, clientOnCparty, connAether, connCparty)
	if err != nil {
		log.Fatalf("channel handshake: %v", err)
	}
	for _, side := range []struct {
		chain *relayer.Chain
		ch    string
	}{{aether, chanAether}, {cparty, chanCparty}} {
		state, err := relayer.ChannelState(side.chain, transfertypes.PortID, side.ch)
		if err != nil {
			log.Fatalf("querying %s channel %s: %v", side.chain.Name, side.ch, err)
		}
		fmt.Printf("channel %s/%s on %s: %s\n", transfertypes.PortID, side.ch, side.chain.Name, state)
	}

	// Round trip: native uaeth out to the counterparty (escrowed on
	// Aether, minted as an IBC voucher there), then the voucher back
	// (burned there, unescrowed on Aether).
	amount := math.NewInt(12_345)
	voucher := transfertypes.ParseDenomTrace(transfertypes.GetPrefixedDenom(transfertypes.PortID, chanCparty, "uaeth")).IBCDenom()
	escrow, err := address.NewBech32Codec(app.Bech32MainPrefix).BytesToString(transfertypes.GetEscrowAddress(transfertypes.PortID, chanAether))
	if err != nil {
		log.Fatal(err)
	}

	packet, err := relayer.Transfer(aether, chanAether, sdk.NewCoin("uaeth", amount), cparty.FromAddrStr)
	if err != nil {
		log.Fatalf("transfer aether -> counterparty: %v", err)
	}
	fmt.Printf("sent packet %d: %s uaeth aether -> counterparty\n", packet.Sequence, amount)
	if err := relayer.RelayPacket(aether, cparty, clientOnAether, clientOnCparty, packet); err != nil {
		log.Fatalf("relaying packet %d: %v", packet.Sequence, err)
	}
	expect(aether, escrow, "uaeth", amount, "aether transfer escrow")
	expect(cparty, cparty.FromAddrStr, voucher, amount, "counterparty voucher ("+voucher+")")

	packet, err = relayer.Transfer(cparty, chanCparty, sdk.NewCoin(voucher, amount), aether.FromAddrStr)
	if err != nil {
		log.Fatalf("transfer counterparty -> aether: %v", err)
	}
	fmt.Printf("sent packet %d: %s voucher counterparty -> aether\n", packet.Sequence, amount)
	if err := relayer.RelayPacket(cparty, aether, clientOnCparty, clientOnAether, packet); err != nil {
		log.Fatalf("relaying packet %d: %v", packet.Sequence, err)
	}
	expect(aether, escrow, "uaeth", math.ZeroInt(), "aether transfer escrow")
	expect(cparty, cparty.FromAddrStr, voucher, math.ZeroInt(), "counterparty voucher")
	fmt.Println("round trip complete")
}

func expect(c *relayer.Chain, addr, denom string, want math.Int, what string) {
	got, err := relayer.Balance(c, addr, denom)
	if err != nil {
		log.Fatal(err)
	}
	if !got.Amount.Equal(want) {
		log.Fatalf("%s: got %s, want %s%s", what, got, want, denom)
	}
	fmt.Printf("  %s on %s: %s  ok\n", what, c.Name, got)
}
