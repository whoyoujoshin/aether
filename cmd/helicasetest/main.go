// cmd/helicasetest proves Helicase (app/helicase.go, package helicase)
// end to end against two real, separately running chains: an Aether
// devnet node started with --helicase.counterparty-rpc pointing at a
// counterparty devnet (cmd/counterpartyd), and that counterparty.
//
// It opens the path once with cmd/relayer's handshake (the one step on
// Aether that still takes a signed, ML-DSA-44 relayer transaction), then
// checks that after it nothing relays onto Aether except the proposer:
//
//  1. in: the counterparty sends to Aether. Helicase delivers the packet;
//     the counterparty gets Aether's acknowledgement from a relay signed
//     only with a counterparty (secp256k1) key.
//  2. out: an Aether user sends to the counterparty. The counterparty
//     receives it from that same classical-key relay; Helicase brings the
//     acknowledgement back to Aether.
//  3. timeout: an Aether user sends with a short timeout and nobody
//     relays it. Helicase proves the counterparty never received it, and
//     Aether refunds the sender.
//
// Each Aether transaction that did this is checked to carry no signature,
// the memo "helicase" and the Helicase address as its sender, and the
// Aether relayer key's sequence is checked not to have moved after the
// handshake.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/auth/signing"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	channeltypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/counterparty"
	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/relayer"
)

func main() {
	var (
		aetherRPC     = flag.String("aether-rpc", "http://127.0.0.1:26657", "Aether RPC address")
		aetherGRPC    = flag.String("aether-grpc", "127.0.0.1:9090", "Aether gRPC address")
		aetherChainID = flag.String("aether-chain-id", "aether", "Aether chain ID")
		aetherHome    = flag.String("aether-home", os.ExpandEnv("$HOME/.aether"), "Aether keyring root dir")
		aetherRelayer = flag.String("aether-relayer-key", "relayer", "Aether key that signs the one-time handshake")
		aetherUser    = flag.String("aether-user-key", "user", "Aether key that sends transfers, as any user would")
		aetherGas     = flag.String("aether-gas-prices", "0.0001uaeth", "Aether gas prices")

		cpartyRPC     = flag.String("cparty-rpc", "http://127.0.0.1:26557", "Counterparty RPC address")
		cpartyGRPC    = flag.String("cparty-grpc", "127.0.0.1:9080", "Counterparty gRPC address")
		cpartyChainID = flag.String("cparty-chain-id", "counterparty", "Counterparty chain ID")
		cpartyHome    = flag.String("cparty-home", os.ExpandEnv("$HOME/.counterparty"), "Counterparty keyring root dir")
		cpartyKey     = flag.String("cparty-key", "relayer", "Counterparty key: sends transfers and relays onto the counterparty")
		cpartyGas     = flag.String("cparty-gas-prices", "0.0001stake", "Counterparty gas prices")
		cpartyDenom   = flag.String("cparty-denom", "stake", "Counterparty native denom to send to Aether")

		keyringBackend = flag.String("keyring-backend", "test", "keyring backend for both chains")
		wait           = flag.Duration("wait", 90*time.Second, "how long to wait for Helicase at each step")
		outboundKey    = flag.String("outbound-key", "", "if set, cmd/outbound is running with this counterparty key: relay nothing by hand, wait for it, and check it signed the counterparty's side")
	)
	flag.Parse()

	aetherEnc := app.MakeEncodingConfig()
	aetherKr, err := keyring.New("aetherd", *keyringBackend, *aetherHome, os.Stdin, aetherEnc.Codec, func(o *keyring.Options) {
		o.SupportedAlgos = append(o.SupportedAlgos, mldsa.Algo)
	})
	must(err, "opening aether keyring")
	cpartyEnc := counterparty.MakeEncodingConfig(counterparty.Bech32Prefix)
	cpartyKr, err := keyring.New("counterpartyd", *keyringBackend, *cpartyHome, os.Stdin, cpartyEnc.Codec)
	must(err, "opening counterparty keyring")

	aetherHandshake, err := relayer.NewChain("aether", *aetherRPC, *aetherGRPC, *aetherChainID, app.Bech32MainPrefix,
		aetherEnc.Codec, aetherEnc.TxConfig, aetherKr, *aetherRelayer, *aetherGas)
	must(err, "connecting to aether")
	aetherUserChain, err := relayer.NewChain("aether", *aetherRPC, *aetherGRPC, *aetherChainID, app.Bech32MainPrefix,
		aetherEnc.Codec, aetherEnc.TxConfig, aetherKr, *aetherUser, *aetherGas)
	must(err, "connecting to aether as the user")
	cparty, err := relayer.NewChain("counterparty", *cpartyRPC, *cpartyGRPC, *cpartyChainID, counterparty.Bech32Prefix,
		cpartyEnc.Codec, cpartyEnc.TxConfig, cpartyKr, *cpartyKey, *cpartyGas)
	must(err, "connecting to counterparty")

	// --- The one-time handshake: signed on both chains. ---
	aetherUnbonding, err := relayer.AetherUnbondingPeriod(aetherHandshake)
	must(err, "aether unbonding period")
	cpartyUnbonding, err := relayer.StakingUnbondingPeriod(cparty)
	must(err, "counterparty unbonding period")
	clientOnCparty, err := relayer.CreateClient(aetherHandshake, cparty, aetherUnbonding)
	must(err, "client of aether on counterparty")
	clientOnAether, err := relayer.CreateClient(cparty, aetherHandshake, cpartyUnbonding)
	must(err, "client of counterparty on aether")
	connA, connC, err := relayer.OpenConnection(aetherHandshake, cparty, clientOnAether, clientOnCparty)
	must(err, "connection handshake")
	chanA, chanC, err := relayer.OpenTransferChannel(aetherHandshake, cparty, clientOnAether, clientOnCparty, connA, connC)
	must(err, "channel handshake")
	fmt.Printf("handshake done (signed): aether %s/%s/%s <-> counterparty %s/%s/%s\n", clientOnAether, connA, chanA, clientOnCparty, connC, chanC)

	relayerSeq := sequence(aetherHandshake, aetherHandshake.FromAddr)
	fmt.Printf("aether relayer key %s at sequence %d; from here on nothing may sign a relay on aether\n\n", aetherHandshake.FromAddrStr, relayerSeq)
	// relayer.Chain switches the process-wide bech32 prefix between the two
	// chains, so encode Aether's addresses explicitly.
	helicaseAddr, err := address.NewBech32Codec(app.Bech32MainPrefix).BytesToString(app.HelicaseAddress)
	must(err, "encoding the helicase address")

	var outbound string // the outbound relayer's counterparty address
	if *outboundKey != "" {
		rec, err := cpartyKr.Key(*outboundKey)
		must(err, "outbound key")
		addr, err := rec.GetAddress()
		must(err, "outbound key address")
		outbound, err = address.NewBech32Codec(counterparty.Bech32Prefix).BytesToString(addr)
		must(err, "encoding outbound address")
	}

	// --- 1. In: counterparty -> Aether. ---
	amount := math.NewInt(7_000)
	packet, err := relayer.Transfer(cparty, chanC, sdk.NewCoin(*cpartyDenom, amount), aetherUserChain.FromAddrStr)
	must(err, "counterparty transfer to aether")
	fmt.Printf("1. counterparty sent packet %d: %s%s -> %s\n", packet.Sequence, amount, *cpartyDenom, aetherUserChain.FromAddrStr)

	voucher := transfertypes.ParseDenomTrace(transfertypes.GetPrefixedDenom(transfertypes.PortID, chanA, *cpartyDenom)).IBCDenom()
	waitFor(*wait, "aether to receive packet "+fmt.Sprint(packet.Sequence), func() (bool, error) {
		bal, err := relayer.Balance(aetherUserChain, aetherUserChain.FromAddrStr, voucher)
		return err == nil && bal.Amount.Equal(amount), err
	})
	checkHelicaseTx(aetherUserChain, channeltypes.EventTypeRecvPacket, channeltypes.AttributeKeyDstChannel, chanA, packet.Sequence, helicaseAddr)
	fmt.Printf("   aether received it: %s%s, delivered by the proposer, unsigned\n", amount, voucher)

	if outbound != "" {
		waitFor(*wait, "the counterparty to process aether's acknowledgement", func() (bool, error) {
			return relayer.CommitmentCleared(cparty, packet)
		})
		checkSignedBy(cparty, channeltypes.EventTypeAcknowledgePacket, channeltypes.AttributeKeySrcChannel, chanC, packet.Sequence, outbound)
		fmt.Println("   counterparty got aether's acknowledgement from cmd/outbound (its own secp256k1 key)")
	} else {
		_, ack, err := relayer.FindAcknowledgement(aetherUserChain, transfertypes.PortID, chanA, packet.Sequence)
		must(err, "finding aether's acknowledgement")
		must(relayer.DeliverAcknowledgement(aetherUserChain, cparty, clientOnCparty, packet, ack), "acknowledging on the counterparty")
		fmt.Println("   counterparty got aether's acknowledgement (relayed with a secp256k1 key, onto the counterparty only)")
	}

	// --- 2. Out: Aether -> counterparty. ---
	out := sdk.NewCoin("uaeth", math.NewInt(12_345))
	packet, err = relayer.Transfer(aetherUserChain, chanA, out, cparty.FromAddrStr)
	must(err, "aether transfer to counterparty")
	fmt.Printf("\n2. aether user sent packet %d: %s (an ordinary ML-DSA-signed transfer)\n", packet.Sequence, out)
	if outbound != "" {
		aethVoucher := transfertypes.ParseDenomTrace(transfertypes.GetPrefixedDenom(transfertypes.PortID, chanC, "uaeth")).IBCDenom()
		before, err := relayer.Balance(cparty, cparty.FromAddrStr, aethVoucher)
		must(err, "counterparty voucher balance")
		waitFor(*wait, "the counterparty to receive packet "+fmt.Sprint(packet.Sequence), func() (bool, error) {
			bal, err := relayer.Balance(cparty, cparty.FromAddrStr, aethVoucher)
			return err == nil && bal.Amount.Sub(before.Amount).Equal(out.Amount), err
		})
		checkSignedBy(cparty, channeltypes.EventTypeRecvPacket, channeltypes.AttributeKeyDstChannel, chanC, packet.Sequence, outbound)
		fmt.Println("   counterparty received it from cmd/outbound")
	} else {
		_, err = relayer.DeliverPacket(aetherUserChain, cparty, clientOnCparty, packet)
		must(err, "delivering to the counterparty")
		fmt.Println("   counterparty received it (relayed with a secp256k1 key)")
	}
	waitFor(*wait, "aether to process the acknowledgement of packet "+fmt.Sprint(packet.Sequence), func() (bool, error) {
		return relayer.CommitmentCleared(aetherUserChain, packet)
	})
	checkHelicaseTx(aetherUserChain, channeltypes.EventTypeAcknowledgePacket, channeltypes.AttributeKeySrcChannel, chanA, packet.Sequence, helicaseAddr)
	fmt.Println("   aether processed the acknowledgement, delivered by the proposer, unsigned")

	if outbound != "" {
		// With cmd/outbound running, nothing stays unrelayed long enough
		// to time out; step 3 runs without it.
		finish(aetherHandshake, relayerSeq)
		fmt.Println("both directions ran unattended: helicase onto aether, cmd/outbound onto the counterparty")
		return
	}

	// --- 3. Timeout: Aether -> counterparty, never relayed. ---
	before, err := relayer.Balance(aetherUserChain, aetherUserChain.FromAddrStr, "uaeth")
	must(err, "aether user balance")
	packet, err = relayer.TransferWithTimeout(aetherUserChain, chanA, out, cparty.FromAddrStr, 15*time.Second)
	must(err, "aether transfer that will time out")
	fmt.Printf("\n3. aether user sent packet %d with a 15s timeout; nobody relays it\n", packet.Sequence)
	waitFor(*wait, "aether to time out packet "+fmt.Sprint(packet.Sequence), func() (bool, error) {
		return relayer.CommitmentCleared(aetherUserChain, packet)
	})
	checkHelicaseTx(aetherUserChain, channeltypes.EventTypeTimeoutPacket, channeltypes.AttributeKeySrcChannel, chanA, packet.Sequence, helicaseAddr)
	after, err := relayer.Balance(aetherUserChain, aetherUserChain.FromAddrStr, "uaeth")
	must(err, "aether user balance")
	// The transfer's own fee is the only difference.
	if lost := before.Amount.Sub(after.Amount); lost.GTE(out.Amount) {
		log.Fatalf("aether user lost %s: the timeout didn't refund %s", lost, out)
	}
	fmt.Printf("   aether timed it out and refunded %s, delivered by the proposer, unsigned\n", out)

	finish(aetherHandshake, relayerSeq)
	fmt.Println("helicase proven: packets, acknowledgements and timeouts reached aether with no relayer signature")
}

// finish checks nothing signed on Aether with the relayer key after the
// handshake.
func finish(aether *relayer.Chain, relayerSeq uint64) {
	if got := sequence(aether, aether.FromAddr); got != relayerSeq {
		log.Fatalf("aether relayer key moved from sequence %d to %d: something signed a relay on aether", relayerSeq, got)
	}
	fmt.Printf("\naether relayer key still at sequence %d\n", relayerSeq)
}

// checkSignedBy finds the counterparty transaction whose eventType event
// names channel and seq, and checks signer sent it.
func checkSignedBy(c *relayer.Chain, eventType, channelKey, channel string, seq uint64, signer string) {
	node, err := c.ClientCtx.GetNode()
	must(err, "counterparty node")
	query := fmt.Sprintf("%s.%s='%s' AND %s.%s='%d'", eventType, channelKey, channel, eventType, channeltypes.AttributeKeySequence, seq)
	page, perPage := 1, 10
	res, err := node.TxSearch(context.Background(), query, false, &page, &perPage, "asc")
	must(err, "searching the counterparty for "+query)
	for _, r := range res.Txs {
		sender, _ := relayer.EventAttr(r.TxResult.Events, sdk.EventTypeMessage, sdk.AttributeKeySender)
		if r.TxResult.Code == 0 && sender == signer {
			fmt.Printf("   counterparty tx %X at height %d, signed by %s\n", r.Hash, r.Height, sender)
			return
		}
	}
	log.Fatalf("no successful counterparty tx with %s sent by %s", query, signer)
}

// checkHelicaseTx finds the Aether transaction whose eventType event names
// channel (under channelKey) and seq, and checks it's a relay
// transaction: no signatures, memo "helicase", sender the Helicase
// address.
func checkHelicaseTx(c *relayer.Chain, eventType, channelKey, channel string, seq uint64, helicaseAddr string) {
	node, err := c.ClientCtx.GetNode()
	must(err, "aether node")
	query := fmt.Sprintf("%s.%s='%s' AND %s.%s='%d'", eventType, channelKey, channel, eventType, channeltypes.AttributeKeySequence, seq)
	page, perPage := 1, 10
	res, err := node.TxSearch(context.Background(), query, false, &page, &perPage, "asc")
	must(err, "searching aether for "+query)
	for _, r := range res.Txs {
		if r.TxResult.Code != 0 {
			continue
		}
		tx, err := c.ClientCtx.TxConfig.TxDecoder()(r.Tx)
		must(err, "decoding aether tx")
		sigs, err := tx.(signing.SigVerifiableTx).GetSignaturesV2()
		must(err, "reading signatures")
		memo := tx.(sdk.TxWithMemo).GetMemo()
		sender, _ := relayer.EventAttr(r.TxResult.Events, sdk.EventTypeMessage, sdk.AttributeKeySender)
		if len(sigs) != 0 || memo != app.HelicaseMemo || sender != helicaseAddr {
			log.Fatalf("aether tx %X (%s) is not a relay transaction: %d signatures, memo %q, sender %s", r.Hash, query, len(sigs), memo, sender)
		}
		fmt.Printf("   tx %X at height %d: 0 signatures, memo %q, sender %s\n", r.Hash, r.Height, memo, sender)
		return
	}
	log.Fatalf("no successful aether tx with %s", query)
}

func sequence(c *relayer.Chain, addr sdk.AccAddress) uint64 {
	addrStr, err := address.NewBech32Codec(app.Bech32MainPrefix).BytesToString(addr)
	must(err, "encoding address")
	res, err := authtypes.NewQueryClient(c.ClientCtx).Account(context.Background(), &authtypes.QueryAccountRequest{Address: addrStr})
	must(err, "querying account "+addrStr)
	var acc sdk.AccountI
	must(c.ClientCtx.InterfaceRegistry.UnpackAny(res.Account, &acc), "unpacking account")
	return acc.GetSequence()
}

func waitFor(d time.Duration, what string, done func() (bool, error)) {
	deadline := time.Now().Add(d)
	var lastErr error
	for time.Now().Before(deadline) {
		ok, err := done()
		if ok {
			return
		}
		lastErr = err
		time.Sleep(time.Second)
	}
	log.Fatalf("timed out after %s waiting for %s (last error: %v)", d, what, lastErr)
}

func must(err error, what string) {
	if err != nil {
		log.Fatalf("%s: %v", what, err)
	}
}
