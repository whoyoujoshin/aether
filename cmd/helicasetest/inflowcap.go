package main

import (
	"context"
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

// inflowCapStep checks x/inflowcap (docs/IBC-INFLOW-CAP.md) with
// Helicase and cmd/outbound carrying every packet. Aether's genesis lists
// the counterparty's denom over chanA with a cap, and not unlisted:
//
//   - a transfer one over the room left is refused, and the counterparty
//     refunds the sender once cmd/outbound relays the error ack;
//   - a transfer of exactly the room fills the cap;
//   - an Aether user sending vouchers back frees that much room, and the
//     same amount comes in again;
//   - a token that isn't listed is refused.
func inflowCapStep(aether, cparty *relayer.Chain, chanA, chanC, denom, unlisted string, wait time.Duration) {
	voucher := transfertypes.ParseDenomTrace(transfertypes.GetPrefixedDenom(transfertypes.PortID, chanA, denom)).IBCDenom()
	st := capStatus(aether, voucher)
	fmt.Printf("\n5. inflow cap: %s over %s capped at %s, supply %s, room %s\n", denom, chanA, st.Entry.Cap, st.Supply, st.Room)
	if st.Room.IsZero() {
		log.Fatalf("no room left under the cap to test with")
	}

	over := st.Room.AddRaw(1)
	refused(aether, cparty, chanA, chanC, sdk.NewCoin(denom, over), inflowcap.ReasonOverCap, wait)
	if got := capStatus(aether, voucher).Supply; !got.Equal(st.Supply) {
		log.Fatalf("supply moved from %s to %s on a refused transfer", st.Supply, got)
	}
	fmt.Printf("   %s%s (one over the room) refused on aether and refunded on the counterparty\n", over, denom)

	accepted(aether, cparty, chanC, voucher, sdk.NewCoin(denom, st.Room), wait)
	if st2 := capStatus(aether, voucher); !st2.Supply.Equal(st.Entry.Cap) || !st2.Room.IsZero() {
		log.Fatalf("after filling the cap: supply %s, room %s", st2.Supply, st2.Room)
	}
	fmt.Printf("   %s%s (exactly the room) accepted: supply at the cap, room 0\n", st.Room, denom)

	back := sdk.NewCoin(voucher, math.NewInt(1_000))
	packet, err := relayer.Transfer(aether, chanA, back, cparty.FromAddrStr)
	must(err, "aether user sending vouchers back")
	waitFor(wait, "the send back to be acknowledged on aether", func() (bool, error) {
		return relayer.CommitmentCleared(aether, packet)
	})
	if room := capStatus(aether, voucher).Room; !room.Equal(back.Amount) {
		log.Fatalf("room after sending %s back is %s", back, room)
	}
	fmt.Printf("   aether user sent %s back: room %s\n", back, back.Amount)
	accepted(aether, cparty, chanC, voucher, sdk.NewCoin(denom, back.Amount), wait)
	fmt.Printf("   %s%s came in again\n", back.Amount, denom)

	if unlisted != "" {
		refused(aether, cparty, chanA, chanC, sdk.NewCoin(unlisted, math.NewInt(100)), inflowcap.ReasonNotListed, wait)
		fmt.Printf("   100%s (not listed) refused on aether and refunded on the counterparty\n", unlisted)
	}
}

func capStatus(aether *relayer.Chain, voucher string) inflowcap.CapStatus {
	res, err := inflowcap.NewQueryClient(aether.ClientCtx).Caps(context.Background(), &inflowcap.QueryCapsRequest{})
	must(err, "querying aether's inflow caps")
	for _, c := range res.Caps {
		if c.VoucherDenom == voucher {
			return c
		}
	}
	log.Fatalf("%s isn't listed in aether's inflow caps (%d entries): the devnet genesis must list it", voucher, len(res.Caps))
	return inflowcap.CapStatus{}
}

// refused sends coin from the counterparty to the Aether user, and checks
// Aether refused it for reason and the counterparty refunded it.
func refused(aether, cparty *relayer.Chain, chanA, chanC string, coin sdk.Coin, reason string, wait time.Duration) {
	before, err := relayer.Balance(cparty, cparty.FromAddrStr, coin.Denom)
	must(err, "counterparty balance")
	packet, err := relayer.Transfer(cparty, chanC, coin, aether.FromAddrStr)
	must(err, "counterparty transfer to aether")
	waitFor(wait, "the counterparty to process aether's acknowledgement of packet "+fmt.Sprint(packet.Sequence), func() (bool, error) {
		return relayer.CommitmentCleared(cparty, packet)
	})
	_, ackBz, err := relayer.FindAcknowledgement(aether, transfertypes.PortID, chanA, packet.Sequence)
	must(err, "finding aether's acknowledgement")
	var ack channeltypes.Acknowledgement
	must(transfertypes.ModuleCdc.UnmarshalJSON(ackBz, &ack), "decoding aether's acknowledgement")
	if ack.Success() {
		log.Fatalf("aether accepted packet %d (%s), which the inflow cap should refuse", packet.Sequence, coin)
	}
	if got := refusalReason(aether, chanA, packet.Sequence); got != reason {
		log.Fatalf("packet %d refused with reason %q, want %q", packet.Sequence, got, reason)
	}
	after, err := relayer.Balance(cparty, cparty.FromAddrStr, coin.Denom)
	must(err, "counterparty balance")
	// Gas, if it's paid in this denom, is the only difference.
	if lost := before.Amount.Sub(after.Amount); lost.GTE(coin.Amount) {
		log.Fatalf("counterparty sender lost %s: the error ack didn't refund %s", lost, coin)
	}
}

// accepted sends coin from the counterparty to the Aether user and waits
// for it to arrive.
func accepted(aether, cparty *relayer.Chain, chanC, voucher string, coin sdk.Coin, wait time.Duration) {
	before, err := relayer.Balance(aether, aether.FromAddrStr, voucher)
	must(err, "aether user balance")
	packet, err := relayer.Transfer(cparty, chanC, coin, aether.FromAddrStr)
	must(err, "counterparty transfer to aether")
	waitFor(wait, "aether to receive packet "+fmt.Sprint(packet.Sequence), func() (bool, error) {
		bal, err := relayer.Balance(aether, aether.FromAddrStr, voucher)
		return err == nil && bal.Amount.Sub(before.Amount).Equal(coin.Amount), err
	})
}

// refusalReason is the reason in the inflow_cap_refused event of the
// Aether transaction that received packet seq over chanA.
func refusalReason(aether *relayer.Chain, chanA string, seq uint64) string {
	node, err := aether.ClientCtx.GetNode()
	must(err, "aether node")
	query := fmt.Sprintf("%s.%s='%s' AND %s.%s='%d'", channeltypes.EventTypeRecvPacket, channeltypes.AttributeKeyDstChannel, chanA,
		channeltypes.EventTypeRecvPacket, channeltypes.AttributeKeySequence, seq)
	page, perPage := 1, 10
	res, err := node.TxSearch(context.Background(), query, false, &page, &perPage, "asc")
	must(err, "searching aether for "+query)
	for _, r := range res.Txs {
		if reason, err := relayer.EventAttr(r.TxResult.Events, inflowcap.RefusedEvent, inflowcap.RefusedKey(inflowcap.AttributeReason)); err == nil {
			return reason
		}
	}
	return ""
}
