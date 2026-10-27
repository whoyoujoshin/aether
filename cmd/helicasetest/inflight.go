package main

import (
	"fmt"
	"log"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	channeltypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"

	"github.com/whoyoujoshin/aether/relayer"
	"github.com/whoyoujoshin/aether/x/inflowcap"
)

// inFlightStep checks that a voucher sent back still holds its room
// under the cap until its packet settles (x/inflowcap's ICS4Wrapper),
// with the cap full when it starts. cmd/outbound is paused for it, so a
// send back stays in flight for as long as the step needs; Helicase
// still carries everything onto Aether.
//
//   - a send back that will time out: while it's in flight a transfer in
//     is refused over_cap; the timeout's refund brings the supply back to
//     the cap exactly;
//   - a send back the counterparty will refuse (a receiver it can't
//     decode): the same, with an error acknowledgement's refund once
//     cmd/outbound resumes;
//   - a send back that succeeds frees its room only on its
//     acknowledgement.
func inFlightStep(aether, cparty *relayer.Chain, chanA, chanC, denom, helicaseAddr string, outboundPID int, wait time.Duration) {
	voucher := transfertypes.ParseDenomTrace(transfertypes.GetPrefixedDenom(transfertypes.PortID, chanA, denom)).IBCDenom()
	st := capStatus(aether, voucher)
	if !st.Room.IsZero() || !st.InFlight.IsZero() {
		log.Fatalf("in flight: start with the cap full and nothing in flight, got supply %s, in flight %s, room %s", st.Supply, st.InFlight, st.Room)
	}
	capAmount := st.Entry.Cap
	back := math.NewInt(1_000)
	fmt.Printf("\n6. in flight: cap %s full (supply %s); cmd/outbound paused\n", capAmount, st.Supply)
	pause(outboundPID)

	// A send back that times out.
	timingOut, err := relayer.TransferWithTimeout(aether, chanA, sdk.NewCoin(voucher, back), cparty.FromAddrStr, 20*time.Second)
	must(err, "sending vouchers back with a 20s timeout")
	expectHeld(aether, voucher, capAmount.Sub(back), back)
	fmt.Printf("   aether user sent %s back (packet %d, 20s timeout): supply %s, in flight %s, room 0\n", back, timingOut.Sequence, capAmount.Sub(back), back)
	refill := refusedOnAether(aether, cparty, chanA, chanC, sdk.NewCoin(denom, back), wait)
	fmt.Printf("   %s%s sent in meanwhile (packet %d) refused over_cap on aether\n", back, denom, refill.Sequence)
	waitFor(wait, "aether to time out packet "+fmt.Sprint(timingOut.Sequence), func() (bool, error) {
		return relayer.CommitmentCleared(aether, timingOut)
	})
	checkHelicaseTx(aether, channeltypes.EventTypeTimeoutPacket, channeltypes.AttributeKeySrcChannel, chanA, timingOut.Sequence, helicaseAddr)
	expectHeld(aether, voucher, capAmount, math.ZeroInt())
	fmt.Printf("   timed out on aether and refunded: supply %s (the cap), in flight 0\n", capAmount)

	// A send back the counterparty refuses.
	failing, err := relayer.Transfer(aether, chanA, sdk.NewCoin(voucher, back), "not-a-bech32")
	must(err, "sending vouchers back to a receiver the counterparty refuses")
	expectHeld(aether, voucher, capAmount.Sub(back), back)
	fmt.Printf("   aether user sent %s back to a receiver the counterparty can't decode (packet %d): in flight %s\n", back, failing.Sequence, back)
	refill2 := refusedOnAether(aether, cparty, chanA, chanC, sdk.NewCoin(denom, math.NewInt(1)), wait)
	fmt.Printf("   1%s sent in meanwhile (packet %d) refused over_cap on aether\n", denom, refill2.Sequence)

	resume(outboundPID)
	fmt.Println("   cmd/outbound resumed")
	waitFor(wait, "aether to process the error acknowledgement of packet "+fmt.Sprint(failing.Sequence), func() (bool, error) {
		return relayer.CommitmentCleared(aether, failing)
	})
	checkHelicaseTx(aether, channeltypes.EventTypeAcknowledgePacket, channeltypes.AttributeKeySrcChannel, chanA, failing.Sequence, helicaseAddr)
	expectHeld(aether, voucher, capAmount, math.ZeroInt())
	fmt.Printf("   counterparty refused it, aether refunded it: supply %s (the cap), in flight 0\n", capAmount)
	for _, p := range []channeltypes.Packet{refill, refill2} {
		waitFor(wait, "the counterparty to process aether's acknowledgement of packet "+fmt.Sprint(p.Sequence), func() (bool, error) {
			return relayer.CommitmentCleared(cparty, p)
		})
	}
	fmt.Println("   the counterparty refunded both refused transfers")

	// A send back that succeeds: its room frees on the acknowledgement.
	ok, err := relayer.Transfer(aether, chanA, sdk.NewCoin(voucher, back), cparty.FromAddrStr)
	must(err, "sending vouchers back")
	waitFor(wait, "aether to process the acknowledgement of packet "+fmt.Sprint(ok.Sequence), func() (bool, error) {
		return relayer.CommitmentCleared(aether, ok)
	})
	expectHeld(aether, voucher, capAmount.Sub(back), math.ZeroInt())
	if room := capStatus(aether, voucher).Room; !room.Equal(back) {
		log.Fatalf("room after a send back was acknowledged is %s, want %s", room, back)
	}
	fmt.Printf("   a send back acknowledged (packet %d): in flight 0, room %s\n", ok.Sequence, back)
}

// expectHeld checks voucher's supply and its sends in flight on Aether.
func expectHeld(aether *relayer.Chain, voucher string, supply, inFlight math.Int) {
	st := capStatus(aether, voucher)
	if !st.Supply.Equal(supply) || !st.InFlight.Equal(inFlight) {
		log.Fatalf("supply %s, in flight %s; want %s, %s", st.Supply, st.InFlight, supply, inFlight)
	}
	if held := supply.Add(inFlight); held.GTE(st.Entry.Cap) && !st.Room.IsZero() {
		log.Fatalf("room %s with %s held under a cap of %s", st.Room, held, st.Entry.Cap)
	}
}

// refusedOnAether sends coin from the counterparty to the Aether user and
// checks Aether wrote an error acknowledgement with reason over_cap. It
// doesn't wait for the counterparty's refund: cmd/outbound may be paused.
func refusedOnAether(aether, cparty *relayer.Chain, chanA, chanC string, coin sdk.Coin, wait time.Duration) channeltypes.Packet {
	packet, err := relayer.Transfer(cparty, chanC, coin, aether.FromAddrStr)
	must(err, "counterparty transfer to aether")
	var ackBz []byte
	waitFor(wait, "aether to acknowledge packet "+fmt.Sprint(packet.Sequence), func() (bool, error) {
		_, ackBz, err = relayer.FindAcknowledgement(aether, transfertypes.PortID, chanA, packet.Sequence)
		return err == nil, err
	})
	var ack channeltypes.Acknowledgement
	must(transfertypes.ModuleCdc.UnmarshalJSON(ackBz, &ack), "decoding aether's acknowledgement")
	if ack.Success() {
		log.Fatalf("aether accepted packet %d (%s) while a send back held the room", packet.Sequence, coin)
	}
	if got := refusalReason(aether, chanA, packet.Sequence); got != inflowcap.ReasonOverCap {
		log.Fatalf("packet %d refused with reason %q, want %q", packet.Sequence, got, inflowcap.ReasonOverCap)
	}
	return packet
}
