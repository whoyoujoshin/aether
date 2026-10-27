package app

import (
	"encoding/json"
	"math/big"
	"strconv"
	"strings"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	ibctransfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	channeltypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	host "github.com/cosmos/ibc-go/v8/modules/core/24-host"
	ibctesting "github.com/cosmos/ibc-go/v8/testing"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/ligase"
	"github.com/whoyoujoshin/aether/x/governance"
	"github.com/whoyoujoshin/aether/x/inflowcap"
)

func (f *ligaseFixture) supplyMap() map[string]sdkmath.Int {
	f.t.Helper()
	coins, _, err := f.appA.BankKeeper.GetPaginatedTotalSupply(f.a.GetContext(), &query.PageRequest{Limit: 1000})
	require.NoError(f.t, err)
	out := map[string]sdkmath.Int{}
	for _, c := range coins {
		out[c.Denom] = c.Amount
	}
	return out
}

func supplyGrew(before, after map[string]sdkmath.Int) map[string]sdkmath.Int {
	grew := map[string]sdkmath.Int{}
	seen := map[string]bool{}
	for d := range before {
		seen[d] = true
	}
	for d := range after {
		seen[d] = true
	}
	for d := range seen {
		b, a := before[d], after[d]
		if b.IsNil() {
			b = sdkmath.ZeroInt()
		}
		if a.IsNil() {
			a = sdkmath.ZeroInt()
		}
		if a.GT(b) {
			grew[d] = a.Sub(b)
		}
	}
	return grew
}

// deliverRaw commits an arbitrary ICS-20 packet on B (no escrow there: a
// malicious counterparty, or a forged header, can commit any bytes) and
// receives it on A. txErr is set when the receive transaction itself
// fails, which writes no acknowledgement.
func (f *ligaseFixture) deliverRaw(denom, amount, receiver, memo string) (ack channeltypes.Acknowledgement, txErr error) {
	f.t.Helper()
	data := ibctransfertypes.NewFungibleTokenPacketData(denom, amount, f.remote, receiver, memo)
	timeout := clienttypes.NewHeight(0, 1_000_000)
	bz := data.GetBytes()
	seq, err := f.path.EndpointB.SendPacket(timeout, 0, bz)
	require.NoError(f.t, err)
	packet := channeltypes.NewPacket(bz, seq,
		f.path.EndpointB.ChannelConfig.PortID, f.path.EndpointB.ChannelID,
		f.path.EndpointA.ChannelConfig.PortID, f.path.EndpointA.ChannelID,
		timeout, 0)
	res, err := f.path.EndpointA.RecvPacketWithResult(packet)
	if err != nil {
		return channeltypes.Acknowledgement{}, err
	}
	ackBz, err := ibctesting.ParseAckFromEvents(res.Events)
	require.NoError(f.t, err)
	require.NoError(f.t, ibctransfertypes.ModuleCdc.UnmarshalJSON(ackBz, &ack))
	return ack, nil
}

func (f *ligaseFixture) sendUnrelayed(amount int64, receiver string) channeltypes.Packet {
	f.t.Helper()
	msg := ibctransfertypes.NewMsgTransfer("transfer", f.path.EndpointB.ChannelID, sdk.NewInt64Coin("uaeth", amount),
		f.remote, receiver, clienttypes.NewHeight(0, 1_000_000), 0, "")
	res, err := f.b.SendMsgs(msg)
	require.NoError(f.t, err)
	packet, err := ibctesting.ParsePacketFromEvents(res.Events)
	require.NoError(f.t, err)
	return packet
}

func (f *ligaseFixture) recvPackets(packets ...channeltypes.Packet) (*abci.ExecTxResult, error) {
	f.t.Helper()
	require.NoError(f.t, f.path.EndpointA.UpdateClient())
	msgs := make([]sdk.Msg, 0, len(packets))
	signer := f.a.SenderAccount.GetAddress().String()
	for _, p := range packets {
		key := host.PacketCommitmentKey(p.GetSourcePort(), p.GetSourceChannel(), p.GetSequence())
		proof, proofHeight := f.path.EndpointA.Counterparty.Chain.QueryProof(key)
		msgs = append(msgs, channeltypes.NewMsgRecvPacket(p, proof, proofHeight, signer))
	}
	return f.a.SendMsgs(msgs...)
}

// burnRejected sends `amount` of the voucher back to B with a receiver B
// will not accept, so the burn sits in flight until the error
// acknowledgement is relayed. The sender is A's account, which must hold
// the vouchers.
func (f *ligaseFixture) burnRejected(amount int64) channeltypes.Packet {
	f.t.Helper()
	f.a.SendMsgsOverride = nil
	msg := ibctransfertypes.NewMsgTransfer("transfer", f.path.EndpointA.ChannelID, sdk.NewInt64Coin(f.voucher, amount),
		f.a.SenderAccount.GetAddress().String(), "not-a-bech32", clienttypes.NewHeight(0, 1_000_000), 0, "")
	res, err := f.a.SendMsgs(msg)
	require.NoError(f.t, err)
	packet, err := ibctesting.ParsePacketFromEvents(res.Events)
	require.NoError(f.t, err)
	return packet
}

func (f *ligaseFixture) refundErrorAck(packet channeltypes.Packet) {
	f.t.Helper()
	require.NoError(f.t, f.path.EndpointB.UpdateClient())
	recv, err := f.path.EndpointB.RecvPacketWithResult(packet)
	require.NoError(f.t, err)
	ackBz, err := ibctesting.ParseAckFromEvents(recv.Events)
	require.NoError(f.t, err)
	var ack channeltypes.Acknowledgement
	require.NoError(f.t, ibctransfertypes.ModuleCdc.UnmarshalJSON(ackBz, &ack))
	require.False(f.t, ack.Success(), "B must reject the return so A refunds it: %s", ackBz)
	require.NoError(f.t, f.path.EndpointA.AcknowledgePacket(packet, ackBz))
}

// Every odd spelling of the listed denom either is that same voucher
// (and then the cap applies) or it is refused, and nothing else is
// minted: not the listed voucher by another name, not a native denom,
// not a second ibc/ hash the cap did not count.
func TestRedTeam_DenomSpellingsDoNotMintUncounted(t *testing.T) {
	f := newInflowCapFixture(t)
	const capAmount = int64(1000)
	f.capVoucher(capAmount)
	chA := f.path.EndpointA.ChannelID
	chB := f.path.EndpointB.ChannelID
	hash := strings.TrimPrefix(f.voucher, "ibc/")
	receiver := f.payee

	// Control: the real denom mints the listed voucher, one unit.
	ack, txErr := f.deliverRaw("uaeth", "1", receiver, "")
	require.NoError(t, txErr)
	require.True(t, ack.Success())
	require.Equal(t, int64(1), f.supplyA(f.voucher))

	variants := []string{
		"uaeth/",
		"uaeth//",
		"/uaeth",
		"//uaeth",
		"uaeth/uaeth",
		"transfer/" + chA + "/uaeth",
		"transfer/" + chB + "/uaeth",
		"transfer/" + chB + "/uaeth/",
		"transfer/" + chB + "//uaeth",
		"Transfer/" + chB + "/uaeth",
		"TRANSFER/" + chB + "/uaeth",
		"transfer/" + strings.ToUpper(chB) + "/uaeth",
		"transfer/" + chB + "/UAETH",
		"ibc/" + hash,
		"IBC/" + hash,
		"ibc/" + strings.ToLower(hash),
		"IBC/" + strings.ToLower(hash),
		"ibc/" + hash + "/",
		"uaeth/channel-0/pool",
		"foo/channel-0/uaeth",
		"transfer/channel-0/uaeth",
		"transfer/channel-0/transfer/channel-1/uaeth",
		"gamm/pool/1",
		"transfer/channel-01/uaeth",
		"port/channel-9/uaeth",
		"UAETH",
		" uaeth",
		"uaeth ",
		"uaeth\n",
		"%s",
		"uaeth\u200b",
		"transfer//" + chB + "/uaeth",
		"transfer/" + chB,
		"transfer/" + chB + "/" + f.voucher,
		"",
		"/",
		"ibc/",
		"IBC/",
	}
	for _, denom := range variants {
		before := f.supplyMap()
		ack, txErr := f.deliverRaw(denom, "1", receiver, "")
		after := f.supplyMap()
		grew := supplyGrew(before, after)
		if txErr != nil {
			require.Empty(t, grew, "denom %q: tx failed (%v) but supply grew", denom, txErr)
			continue
		}
		comingHome := ibctransfertypes.ReceiverChainIsSource(ibctransfertypes.PortID, chB, denom)
		capped := inflowcap.VoucherDenom(chA, denom)
		if comingHome {
			require.False(t, ack.Success(), "denom %q: nothing is escrowed, so unescrow must fail rather than mint", denom)
			require.Empty(t, grew, "denom %q minted on the coming-home path", denom)
			continue
		}
		if capped == f.voucher {
			// Same coin as the listing: the unit must have been counted.
			require.Equal(t, map[string]sdkmath.Int{f.voucher: sdkmath.NewInt(1)}, grew, "denom %q", denom)
			require.LessOrEqual(t, f.supplyA(f.voucher), capAmount)
			continue
		}
		require.False(t, ack.Success(), "unlisted denom %q (voucher %s) was accepted", denom, capped)
		require.Empty(t, grew, "unlisted denom %q minted %v", denom, grew)
	}
	require.LessOrEqual(t, f.supplyA(f.voucher), capAmount)
}

// A denom prefixed with the sending chain's port and channel is "coming
// home": the cap does not apply, and transfer unescrows Aether's own
// escrow instead of minting. With nothing escrowed the unescrow fails
// and the sender is refunded. With uaeth actually escrowed, a packet can
// take only that escrow, and still cannot mint the listed voucher.
func TestRedTeam_FakeComingHomeUnescrowsOrFails(t *testing.T) {
	f := newInflowCapFixture(t)
	f.capVoucher(1000)
	chB := f.path.EndpointB.ChannelID
	home := "transfer/" + chB + "/uaeth"
	receiver := f.payee

	recv := sdk.MustAccAddressFromBech32(receiver)
	beforeVoucher := f.supplyA(f.voucher)
	beforeAeth := f.supplyA("uaeth")
	bal := f.balanceA(recv, "uaeth")
	ack, txErr := f.deliverRaw(home, "1000", receiver, "")
	require.NoError(t, txErr)
	require.False(t, ack.Success(), "no escrow: unescrow fails, nothing is minted")
	require.Equal(t, beforeVoucher, f.supplyA(f.voucher))
	require.Equal(t, beforeAeth, f.supplyA("uaeth"))
	require.Equal(t, bal.Int64(), f.balanceA(recv, "uaeth").Int64(), "the failed unescrow moved no uaeth")

	// A real send locks 100 uaeth in A's escrow for this channel.
	setCaps(t, f.b, inflowcap.Entry{ChannelId: chB, Denom: "uaeth", Cap: sdkmath.NewInt(1_000_000)})
	f.a.SendMsgsOverride = nil
	out := ibctransfertypes.NewMsgTransfer("transfer", f.path.EndpointA.ChannelID, sdk.NewInt64Coin("uaeth", 100),
		f.a.SenderAccount.GetAddress().String(), f.remote, clienttypes.NewHeight(0, 1_000_000), 0, "")
	res, err := f.a.SendMsgs(out)
	require.NoError(t, err)
	packet, err := ibctesting.ParsePacketFromEvents(res.Events)
	require.NoError(t, err)
	require.NoError(t, f.path.RelayPacket(packet))

	aethBefore := f.supplyA("uaeth")
	voucherBefore := f.supplyA(f.voucher)
	recvBefore := f.balanceA(recv, "uaeth")
	ack, txErr = f.deliverRaw(home, "100", receiver, "")
	require.NoError(t, txErr)
	require.True(t, ack.Success(), "the 100 escrowed uaeth unlock")
	require.Equal(t, aethBefore, f.supplyA("uaeth"), "unescrow does not mint uaeth")
	require.Equal(t, voucherBefore, f.supplyA(f.voucher), "unescrow does not mint the voucher")
	require.Equal(t, recvBefore.AddRaw(100).Int64(), f.balanceA(recv, "uaeth").Int64())

	ack, txErr = f.deliverRaw(home, "1", receiver, "")
	require.NoError(t, txErr)
	require.False(t, ack.Success(), "one more than the escrow fails, and still mints nothing")
	require.Equal(t, aethBefore, f.supplyA("uaeth"))
	require.Equal(t, voucherBefore, f.supplyA(f.voucher))

	// The same prefix in front of the listed voucher tries to unescrow
	// that voucher (it was minted, not escrowed) and does not mint more.
	ack, txErr = f.deliverRaw("transfer/"+chB+"/"+f.voucher, "1000", receiver, "")
	require.NoError(t, txErr)
	require.False(t, ack.Success())
	require.Equal(t, voucherBefore, f.supplyA(f.voucher))
}

func maxUint256String() string {
	n := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	return n.String()
}

// Amount strings are parsed once, with math.NewIntFromString, and that
// integer is what both the cap and the mint use. A string that is not a
// positive integer, or whose integer does not fit under the cap, mints
// nothing. (Leading zero is octal, so "010" is 8, not 10.)
func TestRedTeam_AmountStringsMintWhatTheCapChecked(t *testing.T) {
	f := newInflowCapFixture(t)
	const capAmount = int64(100)
	f.capVoucher(capAmount)
	receiver := f.payee

	under := []struct {
		amount string
		want   int64
	}{
		{"1", 1},
		{"+10", 10},
		{"010", 8},
		{"0x10", 16},
		{"0b10", 2},
		{"1_0", 10},
		{"50", 50},
	}
	var sum int64
	for _, c := range under {
		sum += c.want
		before := f.supplyA(f.voucher)
		ack, txErr := f.deliverRaw("uaeth", c.amount, receiver, "")
		require.NoError(t, txErr, c.amount)
		require.True(t, ack.Success(), "%s should mint %d", c.amount, c.want)
		require.Equal(t, before+c.want, f.supplyA(f.voucher), "amount %q", c.amount)
	}
	require.LessOrEqual(t, sum, capAmount)
	require.Equal(t, sum, f.supplyA(f.voucher))

	// Whatever these decode as, the supply must not pass the cap. A
	// duplicate JSON amount is the case where a decoder could see either
	// number; minting the larger one would pass the cap.
	for _, raw := range []struct {
		name string
		data []byte
	}{
		{"zero", mustPacket("uaeth", "0", receiver)},
		{"negative", mustPacket("uaeth", "-5", receiver)},
		{"plus zero", mustPacket("uaeth", "+0", receiver)},
		{"blank", mustPacket("uaeth", "", receiver)},
		{"decimal", mustPacket("uaeth", "1.5", receiver)},
		{"exponent", mustPacket("uaeth", "1e2", receiver)},
		{"space", mustPacket("uaeth", " 10", receiver)},
		{"trailing space", mustPacket("uaeth", "10 ", receiver)},
		{"bad octal", mustPacket("uaeth", "08", receiver)},
		{"one over", mustPacket("uaeth", "100", receiver)}, // sum is 97, plus 100 is over
		{"far over", mustPacket("uaeth", "999999", receiver)},
		{"max uint256", mustPacket("uaeth", maxUint256String(), receiver)},
		{"past uint256", mustPacket("uaeth", new(big.Int).Lsh(big.NewInt(1), 256).String(), receiver)},
		{"json number", []byte(`{"denom":"uaeth","amount":999999,"sender":"` + f.remote + `","receiver":"` + receiver + `","memo":""}`)},
		{"duplicate amount", []byte(`{"denom":"uaeth","amount":"1","sender":"` + f.remote + `","receiver":"` + receiver + `","memo":"","amount":"999999"}`)},
		{"not json", []byte("not json")},
		{"protobuf", []byte{0x0a, 0x05, 0x75, 0x61, 0x65, 0x74, 0x68}},
	} {
		before := f.supplyA(f.voucher)
		ack, txErr := f.deliverRawBytes(raw.data)
		// A duplicate key or a JSON number can decode to a small integer.
		// That may mint, but only that integer, and only inside the cap.
		// Minting the other reading (999999) would pass the cap.
		require.LessOrEqual(t, f.supplyA(f.voucher), capAmount, "%s (txErr %v)", raw.name, txErr)
		if raw.name == "duplicate amount" || raw.name == "json number" {
			continue
		}
		require.Equal(t, before, f.supplyA(f.voucher), "%s minted (txErr %v)", raw.name, txErr)
		if txErr == nil {
			require.False(t, ack.Success(), "%s was accepted", raw.name)
		}
	}
}

func mustPacket(denom, amount, receiver string) []byte {
	return ibctransfertypes.NewFungibleTokenPacketData(denom, amount, "inj1sender", receiver, "").GetBytes()
}

func (f *ligaseFixture) deliverRawBytes(data []byte) (ack channeltypes.Acknowledgement, txErr error) {
	f.t.Helper()
	timeout := clienttypes.NewHeight(0, 1_000_000)
	seq, err := f.path.EndpointB.SendPacket(timeout, 0, data)
	require.NoError(f.t, err)
	packet := channeltypes.NewPacket(data, seq,
		f.path.EndpointB.ChannelConfig.PortID, f.path.EndpointB.ChannelID,
		f.path.EndpointA.ChannelConfig.PortID, f.path.EndpointA.ChannelID,
		timeout, 0)
	res, err := f.path.EndpointA.RecvPacketWithResult(packet)
	if err != nil {
		return channeltypes.Acknowledgement{}, err
	}
	ackBz, err := ibctesting.ParseAckFromEvents(res.Events)
	if err != nil {
		return channeltypes.Acknowledgement{}, err
	}
	require.NoError(f.t, ibctransfertypes.ModuleCdc.UnmarshalJSON(ackBz, &ack))
	return ack, nil
}

// Once the voucher exists, amount 2^256-1 makes supply.Add panic. The
// receive transaction fails, so ibc-go never writes the error
// acknowledgement the sender's chain needs to refund before timeout.
func TestRedTeam_OverflowingRecvWritesNoErrorAck(t *testing.T) {
	f := newInflowCapFixture(t)
	f.capVoucher(1000)
	receiver := f.payee
	ack, txErr := f.deliverRaw("uaeth", "1", receiver, "")
	require.NoError(t, txErr)
	require.True(t, ack.Success())
	require.Equal(t, int64(1), f.supplyA(f.voucher))

	before := f.supplyA(f.voucher)
	_, txErr = f.deliverRaw("uaeth", maxUint256String(), receiver, "")
	require.Equal(t, before, f.supplyA(f.voucher), "the panic must not mint")
	require.NoError(t, txErr, "amount 2^256-1 with supply 1 must be an error acknowledgement (Int.SafeAdd, then refuse over_cap), not a failed transaction whose sender cannot be refunded until timeout")
}

// Three packets in one transaction, each under the cap, together over it.
// The mint of an earlier message is visible to the next, so only what
// fits is minted.
func TestRedTeam_PacketsInOneTxCannotExceedCapTogether(t *testing.T) {
	f := newInflowCapFixture(t)
	const capAmount = int64(1000)
	f.capVoucher(capAmount)
	receiver := f.payee
	var packets []channeltypes.Packet
	for i := 0; i < 3; i++ {
		packets = append(packets, f.sendUnrelayed(400, receiver))
	}
	_, err := f.recvPackets(packets...)
	require.NoError(t, err)
	require.Equal(t, int64(800), f.supplyA(f.voucher), "400+400 fit, the third 400 must be refused in the same tx")
	require.LessOrEqual(t, f.supplyA(f.voucher), capAmount)

	// The room left is 200. Two more packets of 200 and 1, same tx.
	more := []channeltypes.Packet{f.sendUnrelayed(200, receiver), f.sendUnrelayed(1, receiver)}
	_, err = f.recvPackets(more...)
	require.NoError(t, err)
	require.Equal(t, capAmount, f.supplyA(f.voucher))
}

// A send back that will fail still holds its room while it is in flight:
// a fresh fill is refused, and the error acknowledgement's refund brings
// the supply back to the cap, never over it. Before the fix two cycles
// reached 3000 on a cap of 1000.
func TestRedTeam_InFlightErrorAckRefundHeldUnderCap(t *testing.T) {
	f := newInflowCapFixture(t)
	const capAmount = int64(1000)
	f.capVoucher(capAmount)
	f.send(capAmount, f.payee, "")
	require.True(t, f.lastAckSuccess)
	require.Equal(t, capAmount, f.supplyA(f.voucher))

	for cycle := 1; cycle <= 2; cycle++ {
		held := f.supplyA(f.voucher)
		packet := f.burnRejected(held)
		require.Equal(t, int64(0), f.supplyA(f.voucher), "cycle %d: burned while in flight", cycle)
		require.Equal(t, capAmount, f.appA.InflowCapKeeper.InFlight(f.a.GetContext(), f.voucher).Int64(), "cycle %d", cycle)
		f.send(capAmount, f.payee, "")
		require.False(t, f.lastAckSuccess, "cycle %d: the burn in flight still holds the room", cycle)
		require.Equal(t, inflowcap.ReasonOverCap, refusal(t, f.lastRecv))
		f.send(1, f.payee, "")
		require.False(t, f.lastAckSuccess, "cycle %d: not even one unit", cycle)
		f.refundErrorAck(packet)
		require.Equal(t, capAmount, f.supplyA(f.voucher), "cycle %d: the refund restores the burn", cycle)
		require.True(t, f.appA.InflowCapKeeper.InFlight(f.a.GetContext(), f.voucher).IsZero(), "cycle %d: released with the refund", cycle)
	}
	f.send(1, f.payee, "")
	require.False(t, f.lastAckSuccess, "at the cap after the refunds")
}

// The same with a timeout: the fill while the burn is in flight is
// refused, and the timeout's refund restores the cap exactly. Before the
// fix one cycle reached 2000.
func TestRedTeam_InFlightTimeoutRefundHeldUnderCap(t *testing.T) {
	f := newInflowCapFixture(t)
	const capAmount = int64(1000)
	f.capVoucher(capAmount)
	f.send(capAmount, f.payee, "")
	require.True(t, f.lastAckSuccess)

	timeout := clienttypes.NewHeight(0, uint64(f.b.GetContext().BlockHeight())+20)
	packet := f.sendBack(capAmount, timeout)
	require.Equal(t, int64(0), f.supplyA(f.voucher))

	f.send(capAmount, f.payee, "")
	require.False(t, f.lastAckSuccess, "the burn in flight still holds the room")
	require.Equal(t, inflowcap.ReasonOverCap, refusal(t, f.lastRecv))
	require.Equal(t, int64(0), f.supplyA(f.voucher))

	f.coord.CommitNBlocks(f.b, 25)
	require.NoError(t, f.path.EndpointA.UpdateClient())
	require.NoError(t, f.path.EndpointA.TimeoutPacket(packet), "the refund itself is never refused")
	require.Equal(t, capAmount, f.supplyA(f.voucher))
	require.True(t, f.appA.InflowCapKeeper.InFlight(f.a.GetContext(), f.voucher).IsZero())
}

// A send back that succeeds frees its room, but only once its
// acknowledgement comes back, and the Caps query shows it in flight
// until then.
func TestRedTeam_SuccessfulSendBackFreesRoomOnAck(t *testing.T) {
	f := newInflowCapFixture(t)
	const capAmount = int64(1000)
	f.capVoucher(capAmount)
	f.send(capAmount, f.payee, "")
	require.True(t, f.lastAckSuccess)

	packet := f.sendBack(400, clienttypes.NewHeight(0, 1_000_000))
	caps, err := inflowcap.NewQueryServerImpl(f.appA.InflowCapKeeper).Caps(f.a.GetContext(), &inflowcap.QueryCapsRequest{})
	require.NoError(t, err)
	require.Equal(t, int64(600), caps.Caps[0].Supply.Int64())
	require.Equal(t, int64(400), caps.Caps[0].InFlight.Int64())
	require.True(t, caps.Caps[0].Room.IsZero())
	f.send(1, f.payee, "")
	require.False(t, f.lastAckSuccess, "not free until acknowledged")

	require.NoError(t, f.path.RelayPacket(packet))
	require.True(t, f.appA.InflowCapKeeper.InFlight(f.a.GetContext(), f.voucher).IsZero())
	f.send(400, f.payee, "")
	require.True(t, f.lastAckSuccess, "the acknowledged burn is final, so its room is free")
	require.Equal(t, capAmount, f.supplyA(f.voucher))
}

// Lowering the cap under the supply does not claw back what is already
// on Aether, and a refund of a send that left before the lowering
// restores that send. That restored supply can sit above the new cap.
// It must not sit above the supply the old cap had already allowed, and
// no further inflow is accepted.
func TestRedTeam_LoweredCapRefundRestoresButBlocksNewInflow(t *testing.T) {
	f := newInflowCapFixture(t)
	f.capVoucher(1000)
	f.send(1000, f.payee, "")
	require.True(t, f.lastAckSuccess)

	timeout := clienttypes.NewHeight(0, uint64(f.b.GetContext().BlockHeight())+20)
	f.a.SendMsgsOverride = nil
	msg := ibctransfertypes.NewMsgTransfer("transfer", f.path.EndpointA.ChannelID, sdk.NewInt64Coin(f.voucher, 1000),
		f.a.SenderAccount.GetAddress().String(), f.remote, timeout, 0, "")
	res, err := f.a.SendMsgs(msg)
	require.NoError(t, err)
	packet, err := ibctesting.ParsePacketFromEvents(res.Events)
	require.NoError(t, err)
	require.Equal(t, int64(0), f.supplyA(f.voucher))

	f.capVoucher(100) // governance lowers the cap while the send is in flight
	f.coord.CommitNBlocks(f.b, 25)
	require.NoError(t, f.path.EndpointA.UpdateClient())
	require.NoError(t, f.path.EndpointA.TimeoutPacket(packet))
	require.Equal(t, int64(1000), f.supplyA(f.voucher), "the refund restores the burn, which the old cap had allowed")

	f.send(1, f.payee, "")
	require.False(t, f.lastAckSuccess, "supply is over the new cap: no new inflow")
	require.Equal(t, inflowcap.ReasonOverCap, refusal(t, f.lastRecv))
	require.Equal(t, int64(1000), f.supplyA(f.voucher))
}

// Escrow and release move the voucher but do not change its supply, so
// they neither free room nor mint past the cap. A transfer over the cap
// is refused before Ligase locks it.
func TestRedTeam_EscrowAndReleaseStayInsideTheCap(t *testing.T) {
	f := newInflowCapFixture(t)
	const capAmount = int64(1000)
	f.capVoucher(capAmount)
	ligaseAddr := ligase.Address.String()

	f.send(600, ligaseAddr, escrowMemo(f.payee))
	require.True(t, f.lastAckSuccess)
	require.Equal(t, "escrow", f.result().Action)
	require.Equal(t, int64(600), f.supplyA(f.voucher), "escrow does not mint a second time")
	first, err := strconv.ParseUint(f.result().EscrowID, 10, 64)
	require.NoError(t, err)

	f.send(500, ligaseAddr, escrowMemo(f.payee))
	require.False(t, f.lastAckSuccess, "escrowed coins still count as supply")
	require.Equal(t, inflowcap.ReasonOverCap, refusal(t, f.lastRecv))
	require.Equal(t, int64(600), f.supplyA(f.voucher))

	// Release pays the 600 to the payee and the triggering unit is minted
	// too. Supply grows by that unit only, not by another 600.
	f.send(1, ligaseAddr, releaseMemo(first))
	require.True(t, f.lastAckSuccess)
	require.Equal(t, "release", f.result().Action)
	require.Equal(t, int64(601), f.supplyA(f.voucher), "release is not a mint of the escrowed coins")
	_, ok := f.appA.EscrowKeeper.GetEscrow(f.a.GetContext(), first)
	require.False(t, ok, "released")

	f.send(399, ligaseAddr, escrowMemo(f.payee))
	require.True(t, f.lastAckSuccess)
	require.Equal(t, capAmount, f.supplyA(f.voucher))
	full, err := strconv.ParseUint(f.result().EscrowID, 10, 64)
	require.NoError(t, err)

	f.send(1, ligaseAddr, releaseMemo(full))
	require.False(t, f.lastAckSuccess, "the triggering unit is over the cap, so the release does not run")
	require.Equal(t, inflowcap.ReasonOverCap, refusal(t, f.lastRecv))
	_, ok = f.appA.EscrowKeeper.GetEscrow(f.a.GetContext(), full)
	require.True(t, ok, "the escrow stays put")
	require.Equal(t, capAmount, f.supplyA(f.voucher))
}

func releaseMemo(id uint64) string {
	return `{"aether":{"release":{"id":"` + strconv.FormatUint(id, 10) + `"}}}`
}

// Ligase's withdraw sends through the same path: while its burn is in
// flight a fresh fill is refused, and its timeout restores the cap
// exactly. Before the fix it reached 2000.
func TestRedTeam_WithdrawTimeoutHeldUnderCap(t *testing.T) {
	f := newInflowCapFixture(t)
	const capAmount = int64(1000)
	f.capVoucher(capAmount)

	f.send(capAmount-1, f.mailbox.String(), "")
	require.True(t, f.lastAckSuccess)
	require.Equal(t, capAmount-1, f.supplyA(f.voucher))

	f.send(1, ligase.Address.String(), `{"aether":{"withdraw":{}}}`)
	require.True(t, f.lastAckSuccess, "the triggering unit fits, then the withdraw burns the mailbox")
	require.Equal(t, "withdraw", f.result().Action)
	require.Equal(t, int64(0), f.supplyA(f.voucher), "withdraw burned the vouchers into an in-flight packet")
	require.Equal(t, capAmount, f.appA.InflowCapKeeper.InFlight(f.a.GetContext(), f.voucher).Int64())
	packet, err := ibctesting.ParsePacketFromEvents(f.lastRecv)
	require.NoError(t, err)

	f.send(capAmount, f.payee, "")
	require.False(t, f.lastAckSuccess, "the withdraw in flight still holds the room")
	require.Equal(t, inflowcap.ReasonOverCap, refusal(t, f.lastRecv))

	// The withdraw packet times out an hour after it was sent.
	f.coord.IncrementTimeBy(2 * time.Hour)
	f.coord.CommitBlock(f.b)
	require.NoError(t, f.path.EndpointA.UpdateClient())
	require.NoError(t, f.path.EndpointA.TimeoutPacket(packet))
	require.Equal(t, capAmount, f.supplyA(f.voucher))
	require.True(t, f.appA.InflowCapKeeper.InFlight(f.a.GetContext(), f.voucher).IsZero())
}

// A hundred entries is the maximum. The update fits a normal gas budget,
// the caps query returns every entry, and the last entry is enforced
// (the list is not truncated). 101 is rejected. Spellings that are
// different coins can sit together; a coin listed twice cannot.
func TestRedTeam_HundredEntriesQueryAndLastEntryIsCapped(t *testing.T) {
	f := newInflowCapFixture(t)
	ch := f.path.EndpointA.ChannelID
	entries := make([]inflowcap.Entry, inflowcap.MaxEntries)
	for i := 0; i < inflowcap.MaxEntries-1; i++ {
		entries[i] = inflowcap.Entry{ChannelId: ch, Denom: "d" + sdkmath.NewInt(int64(i)).String(), Cap: sdkmath.NewInt(1)}
	}
	entries[inflowcap.MaxEntries-1] = inflowcap.Entry{ChannelId: ch, Denom: "uaeth", Cap: sdkmath.NewInt(1000)}

	gov := authtypes.NewModuleAddress(governance.ModuleName).String()
	msg := &inflowcap.MsgUpdateParams{Authority: gov, Params: inflowcap.Params{Entries: entries}}
	ctx := f.a.GetContext().WithGasMeter(storetypes.NewGasMeter(10_000_000))
	_, err := f.appA.MsgServiceRouter().Handler(msg)(ctx, msg)
	require.NoError(t, err)
	require.Less(t, ctx.GasMeter().GasConsumed(), uint64(5_000_000), "100 entries took %d gas", ctx.GasMeter().GasConsumed())

	caps, err := inflowcap.NewQueryServerImpl(f.appA.InflowCapKeeper).Caps(f.a.GetContext(), &inflowcap.QueryCapsRequest{})
	require.NoError(t, err)
	require.Len(t, caps.Caps, inflowcap.MaxEntries)
	bz, err := caps.Marshal()
	require.NoError(t, err)
	require.Less(t, len(bz), 256*1024, "caps query is %d bytes", len(bz))
	require.Equal(t, f.voucher, caps.Caps[inflowcap.MaxEntries-1].VoucherDenom)

	f.send(1000, f.payee, "")
	require.True(t, f.lastAckSuccess, "the last entry is enforced, not dropped")
	f.send(1, f.payee, "")
	require.False(t, f.lastAckSuccess)
	require.Equal(t, inflowcap.ReasonOverCap, refusal(t, f.lastRecv))
	require.Equal(t, int64(1000), f.supplyA(f.voucher))

	ack, txErr := f.deliverRaw("zzz", "1", f.payee, "")
	require.NoError(t, txErr)
	require.False(t, ack.Success(), "unlisted beside a full list")
	require.Equal(t, int64(1000), f.supplyA(f.voucher))

	tooMany := append([]inflowcap.Entry{}, entries...)
	tooMany = append(tooMany, inflowcap.Entry{ChannelId: ch, Denom: "one-more", Cap: sdkmath.NewInt(1)})
	bad := &inflowcap.MsgUpdateParams{Authority: gov, Params: inflowcap.Params{Entries: tooMany}}
	_, err = f.appA.MsgServiceRouter().Handler(bad)(f.a.GetContext(), bad)
	require.Error(t, err, "101 entries")

	dup := append([]inflowcap.Entry{}, entries[:2]...)
	dup[1] = dup[0]
	dup[1].Cap = sdkmath.NewInt(2)
	_, err = f.appA.MsgServiceRouter().Handler(&inflowcap.MsgUpdateParams{
		Authority: gov, Params: inflowcap.Params{Entries: dup},
	})(f.a.GetContext(), &inflowcap.MsgUpdateParams{Authority: gov, Params: inflowcap.Params{Entries: dup}})
	require.Error(t, err, "one coin listed twice")
}

// A cap lowered under the current supply blocks new inflow and does not
// mint or burn what is already there. Room is zero.
func TestRedTeam_CapLoweredBelowSupplyBlocksNewInflow(t *testing.T) {
	f := newInflowCapFixture(t)
	f.capVoucher(1000)
	f.send(1000, f.payee, "")
	require.True(t, f.lastAckSuccess)
	f.capVoucher(100)
	f.send(1, f.payee, "")
	require.False(t, f.lastAckSuccess)
	require.Equal(t, inflowcap.ReasonOverCap, refusal(t, f.lastRecv))
	require.Equal(t, int64(1000), f.supplyA(f.voucher), "lowering the cap does not burn the stock")
	caps, err := inflowcap.NewQueryServerImpl(f.appA.InflowCapKeeper).Caps(f.a.GetContext(), &inflowcap.QueryCapsRequest{})
	require.NoError(t, err)
	require.True(t, caps.Caps[0].Room.IsZero())
	require.Equal(t, int64(100), caps.Caps[0].Entry.Cap.Int64())
	require.Equal(t, int64(1000), caps.Caps[0].Supply.Int64())
}

func inflowGenesis(entries ...inflowcap.Entry) json.RawMessage {
	if entries == nil {
		entries = []inflowcap.Entry{}
	}
	gs := inflowcap.GenesisState{Params: inflowcap.Params{Entries: entries}}
	return MakeEncodingConfig().Codec.MustMarshalJSON(&gs)
}

func initInflowApp(t *testing.T, db dbm.DB, inflow json.RawMessage) *App {
	t.Helper()
	app := newTestApp(t, db)
	genesis := ModuleBasics.DefaultGenesis(MakeEncodingConfig().Codec)
	if inflow != nil {
		genesis[inflowcap.ModuleName] = inflow
	} else {
		delete(genesis, inflowcap.ModuleName)
	}
	bz, err := json.Marshal(genesis)
	require.NoError(t, err)
	_, err = app.InitChain(&abci.RequestInitChain{
		ChainId:         testChainID,
		Time:            testGenesisTime,
		InitialHeight:   1,
		AppStateBytes:   bz,
		ConsensusParams: simtestutil.DefaultConsensusParams,
	})
	require.NoError(t, err)
	return app
}

func readInflow(t *testing.T, app *App, height int64) inflowcap.Params {
	t.Helper()
	ctx := app.NewUncachedContext(false, cmtproto.Header{
		ChainID: testChainID,
		Height:  height,
		Time:    testGenesisTime.Add(time.Duration(height) * time.Minute),
	})
	return app.InflowCapKeeper.GetParams(ctx)
}

// A list written into genesis before the module is wired does not survive.
// The store is created empty at activation, HasParams is false, and the
// testnet seed is what applies — not a high cap smuggled in the old file.
func TestRedTeam_ActivationIgnoresPreWireGenesisList(t *testing.T) {
	const activation = 3
	defer func(orig int64) { inflowCapActivationHeight = orig }(inflowCapActivationHeight)
	inflowCapActivationHeight = activation

	high := inflowcap.Entry{ChannelId: "channel-2", Denom: "erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d", Cap: sdkmath.NewInt(9_000_000_000_000)}
	db := dbm.NewMemDB()
	before := initInflowApp(t, db, inflowGenesis(high))
	require.False(t, before.inflowCapWired)
	finalizeAndCommit(t, before, 1)
	finalizeAndCommit(t, before, 2)
	require.ErrorContains(t, finalizeBlock(before, activation), "restart this node")

	at := newTestApp(t, db)
	require.True(t, at.inflowCapWired)
	finalizeAndCommit(t, at, activation)
	got := readInflow(t, at, activation+1)
	require.Equal(t, testnetInflowEntries(), got.Entries, "the pre-wire genesis list must not raise the caps")
	require.NotEqual(t, high.Cap.String(), got.Entries[0].Cap.String())
}

// Once genesis has run the module, activation does not replace that list
// with the testnet seed — including when the list is empty, which stays
// closed rather than opening the testnet paths.
func TestRedTeam_ActivationKeepsGenesisListWhenAlreadySet(t *testing.T) {
	defer func(orig int64) { inflowCapActivationHeight = orig }(inflowCapActivationHeight)
	inflowCapActivationHeight = 1

	custom := inflowcap.Entry{ChannelId: "channel-7", Denom: "ufoo", Cap: sdkmath.NewInt(42)}
	db := dbm.NewMemDB()
	app := initInflowApp(t, db, inflowGenesis(custom))
	require.True(t, app.inflowCapWired)
	finalizeAndCommit(t, app, 1) // the activation block
	got := readInflow(t, app, 2)
	require.Equal(t, []inflowcap.Entry{custom}, got.Entries, "activation must not overwrite a list genesis already set")

	closed := dbm.NewMemDB()
	emptyApp := initInflowApp(t, closed, inflowGenesis())
	finalizeAndCommit(t, emptyApp, 1)
	empty := readInflow(t, emptyApp, 2)
	require.Empty(t, empty.Entries, "an explicit empty list stays empty; the testnet seed must not open it")
	ctx := emptyApp.NewUncachedContext(false, cmtproto.Header{ChainID: testChainID, Height: 2, Time: testGenesisTime})
	require.True(t, emptyApp.InflowCapKeeper.HasParams(ctx), "empty is a set list, not an absent one")
}

// Export and import keep the list, and the imported list is the one the
// cap will enforce. A missing section on a fresh import is the default
// (nothing listed), not an open path.
func TestRedTeam_GenesisExportImportRoundTrip(t *testing.T) {
	defer func(orig int64) { inflowCapActivationHeight = orig }(inflowCapActivationHeight)
	inflowCapActivationHeight = 1

	custom := []inflowcap.Entry{
		{ChannelId: "channel-2", Denom: "erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d", Cap: sdkmath.NewInt(5_000_000_000)},
		{ChannelId: "channel-3", Denom: "uosmo", Cap: sdkmath.NewInt(0)},
	}
	db := dbm.NewMemDB()
	app := initInflowApp(t, db, inflowGenesis(custom...))
	finalizeAndCommit(t, app, 1)
	ctx := app.NewUncachedContext(false, cmtproto.Header{ChainID: testChainID, Height: 2, Time: testGenesisTime})
	exported, err := app.GetModuleManager().ExportGenesisForModules(ctx, app.AppCodec(), []string{inflowcap.ModuleName})
	require.NoError(t, err)
	require.NotEmpty(t, exported[inflowcap.ModuleName])

	restored := initInflowApp(t, dbm.NewMemDB(), exported[inflowcap.ModuleName])
	finalizeAndCommit(t, restored, 1)
	got := readInflow(t, restored, 2)
	require.Equal(t, custom, got.Entries)
	require.NoError(t, got.Validate())
	voucher := custom[0].VoucherDenom()
	_, ok := got.Find(custom[0].ChannelId, voucher)
	require.True(t, ok)
	require.True(t, inflowcap.Room(got.Entries[1].Cap, sdkmath.NewInt(1)).IsZero(), "a restored cap of 0 still leaves no room")
}
