package inflowcap_test

import (
	"testing"

	sdkmath "cosmossdk.io/math"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	channeltypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	porttypes "github.com/cosmos/ibc-go/v8/modules/core/05-port/types"
	ibcexported "github.com/cosmos/ibc-go/v8/modules/core/exported"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/x/inflowcap"
)

const usdc = "erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d"

// next stands in for Ligase and transfer: it records that it was called.
type next struct {
	porttypes.IBCModule
	calls     int
	settleErr error
	onSettle  func() // what transfer's settlement does, e.g. mint a refund
}

func (n *next) OnAcknowledgementPacket(sdk.Context, channeltypes.Packet, []byte, sdk.AccAddress) error {
	return n.settle()
}

func (n *next) OnTimeoutPacket(sdk.Context, channeltypes.Packet, sdk.AccAddress) error {
	return n.settle()
}

func (n *next) settle() error {
	if n.settleErr != nil {
		return n.settleErr
	}
	if n.onSettle != nil {
		n.onSettle()
	}
	return nil
}

func (n *next) OnRecvPacket(ctx sdk.Context, packet channeltypes.Packet, relayer sdk.AccAddress) ibcexported.Acknowledgement {
	n.calls++
	return channeltypes.NewResultAcknowledgement([]byte{1})
}

type host struct {
	active   bool
	params   inflowcap.Params
	supply   map[string]sdkmath.Int
	inFlight map[string]sdkmath.Int
}

func (h host) InFlight(_ sdk.Context, voucher string) sdkmath.Int {
	if v, ok := h.inFlight[voucher]; ok {
		return v
	}
	return sdkmath.ZeroInt()
}
func (h host) AddInFlight(sdk.Context, inflowcap.InFlightSend)     {}
func (h host) ReleaseInFlight(sdk.Context, string, string, uint64) {}
func (h host) GetInFlightSend(sdk.Context, string, string, uint64) (inflowcap.InFlightSend, bool) {
	return inflowcap.InFlightSend{}, false
}

func (h host) Active(sdk.Context) bool             { return h.active }
func (h host) Params(sdk.Context) inflowcap.Params { return h.params }
func (h host) Supply(_ sdk.Context, denom string) sdk.Coin {
	s, ok := h.supply[denom]
	if !ok {
		s = sdkmath.ZeroInt()
	}
	return sdk.NewCoin(denom, s)
}

func entry(channel, denom string, capAmount int64) inflowcap.Entry {
	return inflowcap.Entry{ChannelId: channel, Denom: denom, Cap: sdkmath.NewInt(capAmount)}
}

// packet is a transfer of amount denom from the other chain's
// channel-77152 to Aether's channel-2.
func packet(denom, amount string) channeltypes.Packet {
	data := transfertypes.NewFungibleTokenPacketData(denom, amount, "inj1sender", "aether1receiver", "")
	return channeltypes.Packet{
		Sequence: 1, SourcePort: "transfer", SourceChannel: "channel-77152",
		DestinationPort: "transfer", DestinationChannel: "channel-2", Data: data.GetBytes(),
	}
}

func ctx() sdk.Context {
	return sdk.NewContext(nil, cmtproto.Header{Height: 10}, false, nil)
}

func recv(t *testing.T, h host, p channeltypes.Packet) (ibcexported.Acknowledgement, *next, sdk.Context) {
	t.Helper()
	n := &next{}
	c := ctx()
	ack := inflowcap.NewIBCModule(n, h).OnRecvPacket(c, p, nil)
	return ack, n, c
}

func refusedReason(t *testing.T, c sdk.Context) string {
	t.Helper()
	for _, ev := range c.EventManager().Events() {
		if ev.Type != inflowcap.EventTypeRefused {
			continue
		}
		for _, a := range ev.Attributes {
			if a.Key == inflowcap.AttributeReason {
				return a.Value
			}
		}
	}
	return ""
}

func TestCap(t *testing.T) {
	voucher := inflowcap.VoucherDenom("channel-2", usdc)
	require.Equal(t, "ibc/064D82A67318DD30F54A4E17B6E487C2864E8B075A5871D3ACAD7A7129F32C5C", voucher, "the testnet's USDC on Aether")
	h := host{active: true, params: inflowcap.Params{Entries: []inflowcap.Entry{entry("channel-2", usdc, 5_000_000_000)}},
		supply: map[string]sdkmath.Int{voucher: sdkmath.NewInt(4_000_000_000)}}

	for _, c := range []struct {
		name   string
		amount string
		pass   bool
	}{
		{"under the cap", "999999999", true},
		{"up to the cap exactly", "1000000000", true},
		{"one over", "1000000001", false},
		{"far over", "5000000000", false},
	} {
		ack, n, cx := recv(t, h, packet(usdc, c.amount))
		require.Equal(t, c.pass, ack.Success(), c.name)
		require.Equal(t, c.pass, n.calls == 1, "%s: passed on", c.name)
		if !c.pass {
			require.Equal(t, inflowcap.ReasonOverCap, refusedReason(t, cx), c.name)
		}
	}
}

func TestCapZero(t *testing.T) {
	h := host{active: true, params: inflowcap.Params{Entries: []inflowcap.Entry{entry("channel-2", usdc, 0)}}}
	ack, n, cx := recv(t, h, packet(usdc, "1"))
	require.False(t, ack.Success())
	require.Zero(t, n.calls)
	require.Equal(t, inflowcap.ReasonOverCap, refusedReason(t, cx))
}

func TestNotListed(t *testing.T) {
	h := host{active: true, params: inflowcap.Params{Entries: []inflowcap.Entry{entry("channel-2", usdc, 5_000_000_000)}}}
	for name, p := range map[string]channeltypes.Packet{
		"another token": packet("inj", "1"),
		"the same token routed through a third chain": packet("transfer/channel-9/"+usdc, "1"),
		"over another channel": func() channeltypes.Packet {
			p := packet(usdc, "1")
			p.DestinationChannel = "channel-7"
			return p
		}(),
	} {
		ack, n, cx := recv(t, h, p)
		require.False(t, ack.Success(), name)
		require.Zero(t, n.calls, name)
		require.Equal(t, inflowcap.ReasonNotListed, refusedReason(t, cx), name)
	}

	// An empty list closes every path.
	ack, n, _ := recv(t, host{active: true}, packet(usdc, "1"))
	require.False(t, ack.Success())
	require.Zero(t, n.calls)
}

// AETH (or anything Aether sent out) coming back over the channel it
// left by unlocks Aether's own escrow: never limited, even with nothing
// listed.
func TestComingHome(t *testing.T) {
	for _, denom := range []string{"transfer/channel-77152/uaeth", "transfer/channel-77152/" + inflowcap.VoucherDenom("channel-3", "uosmo")} {
		ack, n, _ := recv(t, host{active: true}, packet(denom, "1000000000000"))
		require.True(t, ack.Success(), denom)
		require.Equal(t, 1, n.calls, denom)
	}
}

func TestBadAmountRefused(t *testing.T) {
	h := host{active: true, params: inflowcap.Params{Entries: []inflowcap.Entry{entry("channel-2", usdc, 5_000_000_000)}}}
	for _, amount := range []string{"", "abc", "-5", "0"} {
		ack, n, cx := recv(t, h, packet(usdc, amount))
		require.False(t, ack.Success(), amount)
		require.Zero(t, n.calls, amount)
		require.Equal(t, inflowcap.ReasonBadAmount, refusedReason(t, cx), amount)
	}
}

func TestInactivePassesEverything(t *testing.T) {
	ack, n, _ := recv(t, host{}, packet("inj", "1"))
	require.True(t, ack.Success())
	require.Equal(t, 1, n.calls)
}

func TestUndecodablePassesOn(t *testing.T) {
	p := packet(usdc, "1")
	p.Data = []byte("not json")
	_, n, _ := recv(t, host{active: true}, p)
	require.Equal(t, 1, n.calls, "transfer refuses it itself")
}

func TestParamsValidate(t *testing.T) {
	ok := inflowcap.Params{Entries: []inflowcap.Entry{entry("channel-2", usdc, 5_000_000_000), entry("channel-3", "uosmo", 0)}}
	require.NoError(t, ok.Validate())
	require.NoError(t, inflowcap.Params{}.Validate(), "nothing listed")

	for name, p := range map[string]inflowcap.Params{
		"twice":        {Entries: []inflowcap.Entry{entry("channel-2", usdc, 1), entry("channel-2", usdc, 2)}},
		"bad channel":  {Entries: []inflowcap.Entry{entry("chan 2", usdc, 1)}},
		"no denom":     {Entries: []inflowcap.Entry{entry("channel-2", "", 1)}},
		"negative cap": {Entries: []inflowcap.Entry{entry("channel-2", usdc, -1)}},
		"nil cap":      {Entries: []inflowcap.Entry{{ChannelId: "channel-2", Denom: usdc}}},
		"too many": {Entries: func() []inflowcap.Entry {
			var es []inflowcap.Entry
			for i := 0; i <= inflowcap.MaxEntries; i++ {
				es = append(es, entry("channel-2", "d"+sdkmath.NewInt(int64(i)).String(), 1))
			}
			return es
		}()},
	} {
		require.Error(t, p.Validate(), name)
	}
}

func TestRoom(t *testing.T) {
	require.Equal(t, int64(3), inflowcap.Room(sdkmath.NewInt(10), sdkmath.NewInt(7)).Int64())
	require.True(t, inflowcap.Room(sdkmath.NewInt(10), sdkmath.NewInt(12)).IsZero(), "a cap lowered below the supply")
}
