package inflowcap_test

import (
	"context"
	"errors"
	"testing"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	capabilitytypes "github.com/cosmos/ibc-go/modules/capability/types"
	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	channeltypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	porttypes "github.com/cosmos/ibc-go/v8/modules/core/05-port/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/x/inflowcap"
)

type supplyBank map[string]sdkmath.Int

func (b supplyBank) GetSupply(_ context.Context, denom string) sdk.Coin {
	if s, ok := b[denom]; ok {
		return sdk.NewCoin(denom, s)
	}
	return sdk.NewCoin(denom, sdkmath.ZeroInt())
}

func newKeeper(t *testing.T, bank supplyBank) (inflowcap.Keeper, sdk.Context) {
	t.Helper()
	key := storetypes.NewKVStoreKey(inflowcap.StoreKey)
	ctx := testutil.DefaultContext(key, storetypes.NewTransientStoreKey("t_"+inflowcap.StoreKey))
	cdc := codec.NewProtoCodec(codectypes.NewInterfaceRegistry())
	return inflowcap.NewKeeper(cdc, key, bank, "gov"), ctx
}

func send(channel string, seq uint64, voucher string, amount int64) inflowcap.InFlightSend {
	return inflowcap.InFlightSend{PortId: "transfer", ChannelId: channel, Sequence: seq, VoucherDenom: voucher, Amount: sdkmath.NewInt(amount)}
}

func TestInFlight_KeeperTotalsAndGenesis(t *testing.T) {
	voucher := inflowcap.VoucherDenom("channel-2", usdc)
	other := inflowcap.VoucherDenom("channel-3", "uosmo")
	k, ctx := newKeeper(t, supplyBank{voucher: sdkmath.NewInt(600)})
	k.SetParams(ctx, inflowcap.Params{Entries: []inflowcap.Entry{entry("channel-2", usdc, 1000)}})

	k.AddInFlight(ctx, send("channel-2", 1, voucher, 300))
	k.AddInFlight(ctx, send("channel-2", 2, voucher, 50))
	k.AddInFlight(ctx, send("channel-3", 1, other, 7))
	require.Equal(t, int64(350), k.InFlight(ctx, voucher).Int64())
	require.Equal(t, int64(7), k.InFlight(ctx, other).Int64())

	caps := k.Caps(ctx)
	require.Len(t, caps, 1)
	require.Equal(t, int64(600), caps[0].Supply.Int64())
	require.Equal(t, int64(350), caps[0].InFlight.Int64())
	require.Equal(t, int64(50), caps[0].Room.Int64(), "cap - supply - in flight")

	g := k.ExportGenesis(ctx)
	require.NoError(t, g.Validate())
	require.Len(t, g.InFlight, 3)
	k2, ctx2 := newKeeper(t, supplyBank{})
	k2.InitGenesis(ctx2, g)
	require.Equal(t, int64(350), k2.InFlight(ctx2, voucher).Int64(), "totals rebuilt from the sends")
	require.Equal(t, g, k2.ExportGenesis(ctx2))

	k.ReleaseInFlight(ctx, "transfer", "channel-2", 1)
	k.ReleaseInFlight(ctx, "transfer", "channel-2", 1) // twice: no-op
	k.ReleaseInFlight(ctx, "transfer", "channel-9", 1) // never recorded: no-op
	require.Equal(t, int64(50), k.InFlight(ctx, voucher).Int64())
	k.ReleaseInFlight(ctx, "transfer", "channel-2", 2)
	require.True(t, k.InFlight(ctx, voucher).IsZero())
	require.Len(t, k.InFlightSends(ctx), 1)
}

func TestInFlight_GenesisValidate(t *testing.T) {
	voucher := inflowcap.VoucherDenom("channel-2", usdc)
	ok := inflowcap.DefaultGenesisState()
	ok.InFlight = []inflowcap.InFlightSend{send("channel-2", 1, voucher, 1)}
	require.NoError(t, ok.Validate())
	for name, s := range map[string]inflowcap.InFlightSend{
		"zero amount": send("channel-2", 1, voucher, 0),
		"sequence 0":  send("channel-2", 0, voucher, 1),
		"not ibc":     send("channel-2", 1, "uaeth", 1),
		"bad channel": send("chan/2", 1, voucher, 1),
		"nil amount":  {PortId: "transfer", ChannelId: "channel-2", Sequence: 1, VoucherDenom: voucher},
	} {
		g := inflowcap.DefaultGenesisState()
		g.InFlight = []inflowcap.InFlightSend{s}
		require.Error(t, g.Validate(), name)
	}
	dup := inflowcap.DefaultGenesisState()
	dup.InFlight = []inflowcap.InFlightSend{send("channel-2", 1, voucher, 1), send("channel-2", 1, voucher, 2)}
	require.Error(t, dup.Validate())
}

// keeperHost is the app's host over a real keeper.
type keeperHost struct {
	inflowcap.Keeper
	active bool
}

func (h keeperHost) Active(sdk.Context) bool                 { return h.active }
func (h keeperHost) Params(ctx sdk.Context) inflowcap.Params { return h.GetParams(ctx) }

type channel struct {
	porttypes.ICS4Wrapper
	seq uint64
	err error
}

func (c *channel) SendPacket(sdk.Context, *capabilitytypes.Capability, string, string, clienttypes.Height, uint64, []byte) (uint64, error) {
	c.seq++
	return c.seq, c.err
}

func TestInFlight_ICS4WrapperRecordsBurnsOnly(t *testing.T) {
	voucher := inflowcap.VoucherDenom("channel-2", usdc)
	k, ctx := newKeeper(t, supplyBank{})
	h := keeperHost{Keeper: k, active: true}
	ch := &channel{}
	w := inflowcap.NewICS4Wrapper(ch, h)
	sendData := func(denom, amount string) []byte {
		return transfertypes.NewFungibleTokenPacketData(denom, amount, "aether1sender", "inj1receiver", "").GetBytes()
	}

	// USDC going home over channel-2: burned, so in flight, under the
	// same voucher the cap counts.
	seq, err := w.SendPacket(ctx, nil, "transfer", "channel-2", clienttypes.ZeroHeight(), 1, sendData("transfer/channel-2/"+usdc, "250"))
	require.NoError(t, err)
	require.Equal(t, uint64(1), seq)
	require.Equal(t, []inflowcap.InFlightSend{send("channel-2", 1, voucher, 250)}, k.InFlightSends(ctx))

	// AETH out, and USDC forwarded over another channel: escrowed, the
	// supply doesn't move.
	_, err = w.SendPacket(ctx, nil, "transfer", "channel-2", clienttypes.ZeroHeight(), 1, sendData("uaeth", "5"))
	require.NoError(t, err)
	_, err = w.SendPacket(ctx, nil, "transfer", "channel-3", clienttypes.ZeroHeight(), 1, sendData("transfer/channel-2/"+usdc, "5"))
	require.NoError(t, err)
	// Another port, and undecodable data.
	_, err = w.SendPacket(ctx, nil, "icacontroller-x", "channel-2", clienttypes.ZeroHeight(), 1, sendData("transfer/channel-2/"+usdc, "5"))
	require.NoError(t, err)
	_, err = w.SendPacket(ctx, nil, "transfer", "channel-2", clienttypes.ZeroHeight(), 1, []byte("{"))
	require.NoError(t, err)
	require.Len(t, k.InFlightSends(ctx), 1)

	// A failed send, and the cap not active: nothing recorded.
	ch.err = errors.New("closed")
	_, err = w.SendPacket(ctx, nil, "transfer", "channel-2", clienttypes.ZeroHeight(), 1, sendData("transfer/channel-2/"+usdc, "5"))
	require.Error(t, err)
	ch.err = nil
	_, err = inflowcap.NewICS4Wrapper(ch, keeperHost{Keeper: k}).SendPacket(ctx, nil, "transfer", "channel-2", clienttypes.ZeroHeight(), 1, sendData("transfer/channel-2/"+usdc, "5"))
	require.NoError(t, err)
	require.Equal(t, int64(250), k.InFlight(ctx, voucher).Int64())

	// Settling the packet, by acknowledgement or timeout, releases it.
	m := inflowcap.NewIBCModule(&next{}, h)
	sent := channeltypes.Packet{Sequence: 1, SourcePort: "transfer", SourceChannel: "channel-2"}
	require.NoError(t, m.OnAcknowledgementPacket(ctx, sent, nil, nil))
	require.True(t, k.InFlight(ctx, voucher).IsZero())

	_, err = w.SendPacket(ctx, nil, "transfer", "channel-2", clienttypes.ZeroHeight(), 1, sendData("transfer/channel-2/"+usdc, "9"))
	require.NoError(t, err)
	timedOut := channeltypes.Packet{Sequence: ch.seq, SourcePort: "transfer", SourceChannel: "channel-2"}
	require.NoError(t, m.OnTimeoutPacket(ctx, timedOut, nil))
	require.True(t, k.InFlight(ctx, voucher).IsZero())
}

// When transfer fails to settle (the refund errs), the record stays: the
// whole message fails, so nothing was refunded either.
func TestInFlight_FailedSettleKeepsRecord(t *testing.T) {
	voucher := inflowcap.VoucherDenom("channel-2", usdc)
	k, ctx := newKeeper(t, supplyBank{})
	k.AddInFlight(ctx, send("channel-2", 4, voucher, 10))
	m := inflowcap.NewIBCModule(&next{settleErr: errors.New("refund failed")}, keeperHost{Keeper: k, active: true})
	sent := channeltypes.Packet{Sequence: 4, SourcePort: "transfer", SourceChannel: "channel-2"}
	require.Error(t, m.OnAcknowledgementPacket(ctx, sent, nil, nil))
	require.Error(t, m.OnTimeoutPacket(ctx, sent, nil))
	require.Equal(t, int64(10), k.InFlight(ctx, voucher).Int64())
}

// A send in flight holds its room against a receive.
func TestInFlight_RecvCountsInFlight(t *testing.T) {
	voucher := inflowcap.VoucherDenom("channel-2", usdc)
	h := host{
		active:   true,
		params:   inflowcap.Params{Entries: []inflowcap.Entry{entry("channel-2", usdc, 1000)}},
		supply:   map[string]sdkmath.Int{voucher: sdkmath.NewInt(600)},
		inFlight: map[string]sdkmath.Int{voucher: sdkmath.NewInt(399)},
	}
	ack, n, _ := recv(t, h, packet(usdc, "1"))
	require.True(t, ack.Success())
	require.Equal(t, 1, n.calls)
	ack, n, c := recv(t, h, packet(usdc, "2"))
	require.False(t, ack.Success())
	require.Zero(t, n.calls)
	require.Equal(t, inflowcap.ReasonOverCap, refusedReason(t, c))
}

// Settling a recorded send may not leave more of its voucher than it held:
// a correct refund re-mints exactly the burn, and a refund path that
// mints more is refused, so the message fails and nothing lands.
func TestInFlight_SettlementMayNotGrowHeld(t *testing.T) {
	voucher := inflowcap.VoucherDenom("channel-2", usdc)
	sent := channeltypes.Packet{Sequence: 4, SourcePort: "transfer", SourceChannel: "channel-2"}
	for _, tc := range []struct {
		name    string
		record  bool
		minted  int64
		timeout bool
		grew    bool
	}{
		{name: "timeout refunds the burn", record: true, minted: 10, timeout: true},
		{name: "error ack refunds the burn", record: true, minted: 10},
		{name: "success ack: the burn is final", record: true, minted: 0},
		{name: "a refund twice the burn", record: true, minted: 20, timeout: true, grew: true},
		{name: "one unit over the burn", record: true, minted: 11, grew: true},
		{name: "a send from before activation: not checked", record: false, minted: 10, timeout: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bank := supplyBank{voucher: sdkmath.NewInt(90)}
			k, ctx := newKeeper(t, bank)
			if tc.record {
				k.AddInFlight(ctx, send("channel-2", 4, voucher, 10))
			}
			n := &next{onSettle: func() { bank[voucher] = bank[voucher].Add(sdkmath.NewInt(tc.minted)) }}
			m := inflowcap.NewIBCModule(n, keeperHost{Keeper: k, active: true})
			var err error
			if tc.timeout {
				err = m.OnTimeoutPacket(ctx, sent, nil)
			} else {
				err = m.OnAcknowledgementPacket(ctx, sent, nil, nil)
			}
			if tc.grew {
				require.ErrorIs(t, err, inflowcap.ErrSettlementGrew)
				return // the message fails: the store and the mint revert with it
			}
			require.NoError(t, err)
			_, still := k.GetInFlightSend(ctx, "transfer", "channel-2", 4)
			require.False(t, still, "released")
		})
	}
}
