package app

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	ibctransfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	channeltypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	ibctesting "github.com/cosmos/ibc-go/v8/testing"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/ligase"
	"github.com/whoyoujoshin/aether/x/escrow"
)

// ligaseFixture is Aether (chain A, Ligase and x/escrow live) and another
// chain (B) with a transfer channel between them. B's sender stands in for
// someone on Noble: B's native uaeth arrives on A as a voucher, like USDC
// would.
type ligaseFixture struct {
	t              *testing.T
	coord          *ibctesting.Coordinator
	a, b           *ibctesting.TestChain
	appA           *App
	path           *ibctesting.Path
	voucher        string // B's uaeth as it exists on A
	remote, payee  string // B's sender; an Aether account
	mailbox        sdk.AccAddress
	lastAck        []byte
	lastAckSuccess bool
	lastRecv       []abci.Event // A's events receiving the last packet
}

func newLigaseFixture(t *testing.T, ligaseAt int64) *ligaseFixture {
	defer func(orig int64) { escrowActivationHeight = orig }(escrowActivationHeight)
	escrowActivationHeight = 1 // wired from the start; block 1 creates its module account
	orig := ligaseActivationHeight
	ligaseActivationHeight = ligaseAt
	t.Cleanup(func() { ligaseActivationHeight = orig })

	coord := &ibctesting.Coordinator{T: t, CurrentTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	a := newIBCTestChain(t, coord, "aether-test-a")
	b := newIBCTestChain(t, coord, "aether-test-b")
	coord.Chains = map[string]*ibctesting.TestChain{a.ChainID: a, b.ChainID: b}
	path := ibctesting.NewTransferPath(a, b)
	coord.Setup(path)

	appA := a.App.(*App)
	require.True(t, appA.escrowWired)
	f := &ligaseFixture{
		t: t, coord: coord, a: a, b: b, appA: appA, path: path,
		voucher: ibctransfertypes.ParseDenomTrace(ibctransfertypes.GetPrefixedDenom("transfer", path.EndpointA.ChannelID, "uaeth")).IBCDenom(),
		remote:  b.SenderAccount.GetAddress().String(),
		payee:   a.SenderAccount.GetAddress().String(),
	}
	f.mailbox = ligase.Mailbox(path.EndpointA.ChannelID, f.remote)
	return f
}

// send transfers amount of B's uaeth from B to receiver on A with memo,
// relays it, and records A's acknowledgement.
func (f *ligaseFixture) send(amount int64, receiver, memo string) {
	f.t.Helper()
	msg := ibctransfertypes.NewMsgTransfer("transfer", f.path.EndpointB.ChannelID, sdk.NewInt64Coin("uaeth", amount),
		f.remote, receiver, clienttypes.NewHeight(0, 1_000_000), 0, memo)
	res, err := f.b.SendMsgs(msg)
	require.NoError(f.t, err)
	packet, err := ibctesting.ParsePacketFromEvents(res.Events)
	require.NoError(f.t, err)
	require.NoError(f.t, f.path.EndpointA.UpdateClient())
	recv, err := f.path.EndpointA.RecvPacketWithResult(packet)
	require.NoError(f.t, err)
	f.lastRecv = recv.Events
	ackBz, err := ibctesting.ParseAckFromEvents(recv.Events)
	require.NoError(f.t, err)
	require.NoError(f.t, f.path.EndpointB.AcknowledgePacket(packet, ackBz))
	var ack channeltypes.Acknowledgement
	require.NoError(f.t, ibctransfertypes.ModuleCdc.UnmarshalJSON(ackBz, &ack))
	f.lastAck, f.lastAckSuccess = ack.GetResult(), ack.Success()
}

func (f *ligaseFixture) result() ligase.Result {
	f.t.Helper()
	require.True(f.t, f.lastAckSuccess, "acknowledgement was an error")
	var r map[string]ligase.Result
	require.NoError(f.t, json.Unmarshal(f.lastAck, &r))
	return r[ligase.ModuleName]
}

func (f *ligaseFixture) balanceA(addr sdk.AccAddress, denom string) sdkmath.Int {
	return f.appA.BankKeeper.GetBalance(f.a.GetContext(), addr, denom).Amount
}

func (f *ligaseFixture) balanceB(denom string) sdkmath.Int {
	return f.b.App.(*App).BankKeeper.GetBalance(f.b.GetContext(), f.b.SenderAccount.GetAddress(), denom).Amount
}

func escrowMemo(payee string) string {
	return fmt.Sprintf(`{"aether":{"escrow":{"payee":%q,"expires_in":"72h","on_expiry":"refund","terms":"job 1"}}}`, payee)
}

// TestLigaseEscrowFromAnotherChain: someone on another chain funds an
// escrow on Aether, releases it, and gets a refund back home -- each with
// one transfer and no Aether key.
func TestLigaseEscrowFromAnotherChain(t *testing.T) {
	f := newLigaseFixture(t, 0)
	startB := f.balanceB("uaeth")
	ligaseAddr := ligase.Address.String()

	// Fund: 1000 lands in an escrow for the payee, paid by the mailbox.
	f.send(1000, ligaseAddr, escrowMemo(f.payee))
	r := f.result()
	require.Equal(t, "escrow", r.Action)
	require.Equal(t, f.mailbox.String(), r.Mailbox)
	var id uint64
	fmt.Sscan(r.EscrowID, &id)
	e, ok := f.appA.EscrowKeeper.GetEscrow(f.a.GetContext(), id)
	require.True(t, ok)
	require.Equal(t, f.mailbox.String(), e.Payer)
	require.Equal(t, f.payee, e.Payee)
	require.Equal(t, sdk.NewCoins(sdk.NewInt64Coin(f.voucher, 1000)), e.Amount)
	require.True(t, f.balanceA(f.mailbox, f.voucher).IsZero(), "all of it is in the escrow")

	// Release: a 1-unit transfer from the same sender pays the payee.
	f.send(1, ligaseAddr, fmt.Sprintf(`{"aether":{"release":{"id":"%d"}}}`, id))
	require.Equal(t, "release", f.result().Action)
	require.Equal(t, int64(1000), f.balanceA(f.a.SenderAccount.GetAddress(), f.voucher).Int64())
	_, ok = f.appA.EscrowKeeper.GetEscrow(f.a.GetContext(), id)
	require.False(t, ok, "released")

	// Refund: a second escrow, which the payee declines on Aether (a
	// signed MsgRefundEscrow); the money waits in the mailbox until the
	// sender withdraws it home.
	f.send(500, ligaseAddr, escrowMemo(f.payee))
	fmt.Sscan(f.result().EscrowID, &id)
	f.a.SendMsgsOverride = nil
	_, err := f.a.SendMsgs(&escrow.MsgRefundEscrow{Sender: f.payee, Id: id})
	require.NoError(t, err)
	require.Equal(t, int64(1+500), f.balanceA(f.mailbox, f.voucher).Int64(), "the release's unit and the refund")

	beforeWithdraw := f.balanceB("uaeth")
	f.send(2, ligaseAddr, `{"aether":{"withdraw":{}}}`)
	r = f.result()
	require.Equal(t, "withdraw", r.Action)
	require.Equal(t, "503"+f.voucher, r.Returned)
	require.True(t, f.balanceA(f.mailbox, f.voucher).IsZero())

	// Receiving the withdraw instruction sent the mailbox's tokens back
	// to B; relay that packet.
	packet, err := ibctesting.ParsePacketFromEvents(f.lastRecv)
	require.NoError(t, err)
	require.Equal(t, f.remote, packetReceiver(t, packet))
	require.NoError(t, f.path.RelayPacket(packet))
	require.Equal(t, beforeWithdraw.SubRaw(2).AddRaw(503).Int64(), f.balanceB("uaeth").Int64(), "the withdrawal reached B")
	require.Equal(t, startB.SubRaw(1000).Int64(), f.balanceB("uaeth").Int64(), "B is out exactly what the payee was paid")
}

func packetReceiver(t *testing.T, packet channeltypes.Packet) string {
	var data ibctransfertypes.FungibleTokenPacketData
	require.NoError(t, ibctransfertypes.ModuleCdc.UnmarshalJSON(packet.GetData(), &data))
	return data.Receiver
}

// TestLigaseTwoStrands: an instruction from another chain can't move AETH.
func TestLigaseTwoStrands(t *testing.T) {
	f := newLigaseFixture(t, 0)
	ligaseAddr := ligase.Address.String()

	// AETH that left Aether and comes back can't fund an escrow: the
	// packet fails and B keeps its voucher.
	out := ibctransfertypes.NewMsgTransfer("transfer", f.path.EndpointA.ChannelID, sdk.NewInt64Coin("uaeth", 700),
		f.payee, f.remote, clienttypes.NewHeight(0, 1_000_000), 0, "")
	res, err := f.a.SendMsgs(out)
	require.NoError(t, err)
	packet, err := ibctesting.ParsePacketFromEvents(res.Events)
	require.NoError(t, err)
	require.NoError(t, f.path.RelayPacket(packet))
	aethOnB := ibctransfertypes.ParseDenomTrace(ibctransfertypes.GetPrefixedDenom("transfer", f.path.EndpointB.ChannelID, "uaeth")).IBCDenom()
	require.Equal(t, int64(700), f.balanceB(aethOnB).Int64())

	msg := ibctransfertypes.NewMsgTransfer("transfer", f.path.EndpointB.ChannelID, sdk.NewInt64Coin(aethOnB, 700),
		f.remote, ligaseAddr, clienttypes.NewHeight(0, 1_000_000), 0, escrowMemo(f.payee))
	res, err = f.b.SendMsgs(msg)
	require.NoError(t, err)
	packet, err = ibctesting.ParsePacketFromEvents(res.Events)
	require.NoError(t, err)
	require.NoError(t, f.path.RelayPacket(packet))
	require.Equal(t, int64(700), f.balanceB(aethOnB).Int64(), "refused and refunded on B")
	require.True(t, f.balanceA(f.mailbox, "uaeth").IsZero())

	// Named arbiter of someone's AETH escrow, the mailbox still can't
	// release it.
	f.a.SendMsgsOverride = nil
	res, err = f.a.SendMsgs(&escrow.MsgCreateEscrow{
		Payer: f.payee, Payee: sdk.AccAddress([]byte("some-other-aether-account-000000")).String(), Arbiter: f.mailbox.String(),
		Amount: sdk.NewCoins(sdk.NewInt64Coin("uaeth", 900)), ExpiresAt: f.a.CurrentHeader.Time.Add(time.Hour).Unix(),
		OnExpiry: escrow.ON_EXPIRY_REFUND,
	})
	require.NoError(t, err)
	id := escrowIDFromEvents(t, res.Events)
	f.send(1, ligaseAddr, fmt.Sprintf(`{"aether":{"release":{"id":"%d"}}}`, id))
	require.False(t, f.lastAckSuccess)
	_, ok := f.appA.EscrowKeeper.GetEscrow(f.a.GetContext(), id)
	require.True(t, ok, "the AETH escrow is untouched")

	// And AETH sent to the mailbox stays put on withdraw.
	f.a.SendMsgsOverride = nil
	_, err = f.a.SendMsgs(banktypes.NewMsgSend(f.a.SenderAccount.GetAddress(), f.mailbox, sdk.NewCoins(sdk.NewInt64Coin("uaeth", 50))))
	require.NoError(t, err)
	f.send(3, ligaseAddr, `{"aether":{"withdraw":{}}}`)
	require.Equal(t, "3"+f.voucher, f.result().Returned)
	require.Equal(t, int64(50), f.balanceA(f.mailbox, "uaeth").Int64())
}

// TestLigaseRefusesMalformed: an instruction that can't be carried out
// sends the tokens back, and a transfer that isn't for Ligase is ordinary.
func TestLigaseRefusesMalformed(t *testing.T) {
	f := newLigaseFixture(t, 0)
	ligaseAddr := ligase.Address.String()
	for name, c := range map[string]struct{ receiver, memo string }{
		"no instruction":         {ligaseAddr, `hello`},
		"instruction elsewhere":  {f.payee, escrowMemo(f.payee)},
		"two actions":            {ligaseAddr, `{"aether":{"withdraw":{},"release":{"id":"1"}}}`},
		"unknown field":          {ligaseAddr, `{"aether":{"escrow":{"payee":"x","bogus":1}}}`},
		"bad payee":              {ligaseAddr, escrowMemo("not-an-address")},
		"no deadline":            {ligaseAddr, fmt.Sprintf(`{"aether":{"escrow":{"payee":%q}}}`, f.payee)},
		"deadline too far":       {ligaseAddr, fmt.Sprintf(`{"aether":{"escrow":{"payee":%q,"expires_in":"9000h"}}}`, f.payee)},
		"release missing escrow": {ligaseAddr, `{"aether":{"release":{"id":"99"}}}`},
	} {
		before := f.balanceB("uaeth")
		f.send(10, c.receiver, c.memo)
		require.False(t, f.lastAckSuccess, name)
		require.Equal(t, before, f.balanceB("uaeth"), "%s: refunded on B", name)
	}

	// An ordinary transfer with an ordinary memo is untouched.
	f.send(10, f.payee, `invoice 42`)
	require.True(t, f.lastAckSuccess)
	require.Equal(t, int64(10), f.balanceA(f.a.SenderAccount.GetAddress(), f.voucher).Int64())
}

// TestLigaseInactive: before activation a transfer to the Ligase address
// is an ordinary transfer.
func TestLigaseInactive(t *testing.T) {
	f := newLigaseFixture(t, 1_000_000)
	f.send(10, ligase.Address.String(), escrowMemo(f.payee))
	require.True(t, f.lastAckSuccess)
	require.Equal(t, int64(10), f.balanceA(ligase.Address, f.voucher).Int64())
}

func escrowIDFromEvents(t *testing.T, events []abci.Event) uint64 {
	for _, ev := range events {
		if ev.Type != escrow.EventTypeCreated {
			continue
		}
		for _, a := range ev.Attributes {
			if a.Key == escrow.AttributeID {
				var id uint64
				fmt.Sscan(a.Value, &id)
				return id
			}
		}
	}
	t.Fatal("no escrow_created event")
	return 0
}
