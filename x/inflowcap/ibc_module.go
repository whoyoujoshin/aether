package inflowcap

import (
	"math/big"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	channeltypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	porttypes "github.com/cosmos/ibc-go/v8/modules/core/05-port/types"
	ibcexported "github.com/cosmos/ibc-go/v8/modules/core/exported"
)

// Host is what the middleware reads from the app, at call time: the
// module's keeper is wired after the IBC router is built, and only from
// its activation height.
type Host interface {
	// Active reports whether the cap applies to ctx's block.
	Active(ctx sdk.Context) bool
	Params(ctx sdk.Context) Params
	Supply(ctx sdk.Context, denom string) sdk.Coin
	// InFlight, AddInFlight and ReleaseInFlight keep the sends back that
	// a failure would re-mint (Keeper's methods of the same names).
	InFlight(ctx sdk.Context, voucher string) sdkmath.Int
	AddInFlight(ctx sdk.Context, s InFlightSend)
	GetInFlightSend(ctx sdk.Context, portID, channelID string, sequence uint64) (InFlightSend, bool)
	ReleaseInFlight(ctx sdk.Context, portID, channelID string, sequence uint64)
}

// IBCModule wraps the transfer route's IBC callbacks. OnRecvPacket
// checks the cap. Acknowledgements and timeouts of Aether's own sends
// pass through, and then the send's in-flight record (ICS4Wrapper) is
// released: in the same step as a failure's refund, so a refund never
// lands on top of room it still held.
//
// A refund is refused in one case only: settling a recorded send must
// not leave more of its voucher (supply plus in flight) than there was
// before. A correct refund re-mints exactly what the send burned, so
// this only stops a refund path that mints more (a bug like ibc-go's
// 2024 timeout reentrancy): the message fails, nothing is refunded, and
// the packet waits for a fixed binary. Sends with no record (from
// before activation) aren't checked.
type IBCModule struct {
	porttypes.IBCModule
	host Host
}

// NewIBCModule wraps next, the rest of the transfer route. It belongs
// outermost, so it sees a transfer before Ligase acts on its memo.
func NewIBCModule(next porttypes.IBCModule, host Host) IBCModule {
	return IBCModule{IBCModule: next, host: host}
}

// OnRecvPacket refuses a voucher that isn't listed for its channel, or
// that would take its supply over the cap, and passes everything else
// on unchanged.
func (m IBCModule) OnRecvPacket(ctx sdk.Context, packet channeltypes.Packet, relayer sdk.AccAddress) ibcexported.Acknowledgement {
	if !m.host.Active(ctx) {
		return m.IBCModule.OnRecvPacket(ctx, packet, relayer)
	}
	var data transfertypes.FungibleTokenPacketData
	if err := transfertypes.ModuleCdc.UnmarshalJSON(packet.GetData(), &data); err != nil {
		return m.IBCModule.OnRecvPacket(ctx, packet, relayer) // transfer refuses it
	}
	if transfertypes.ReceiverChainIsSource(packet.SourcePort, packet.SourceChannel, data.Denom) {
		// Coming home: unlocks Aether's own escrow, mints nothing.
		return m.IBCModule.OnRecvPacket(ctx, packet, relayer)
	}
	voucher := VoucherDenom(packet.DestinationChannel, data.Denom)
	attrs := []sdk.Attribute{
		sdk.NewAttribute(AttributeChannel, packet.DestinationChannel),
		sdk.NewAttribute(AttributeSender, data.Sender),
		sdk.NewAttribute(AttributeDenom, data.Denom),
		sdk.NewAttribute(AttributeVoucher, voucher),
		sdk.NewAttribute(AttributeAmount, data.Amount),
	}
	if packet.DestinationPort != PortID {
		// Never, while this wraps only the transfer route; refuse rather
		// than mint uncounted if it ever wraps another.
		return m.refuse(ctx, attrs, ReasonNotListed, ErrNotListed.Wrapf("port %s", packet.DestinationPort))
	}
	amount, ok := sdkmath.NewIntFromString(data.Amount)
	if !ok || !amount.IsPositive() {
		return m.refuse(ctx, attrs, ReasonBadAmount, ErrBadPacket.Wrapf("amount %q", data.Amount))
	}
	entry, ok := m.host.Params(ctx).Find(packet.DestinationChannel, voucher)
	if !ok {
		return m.refuse(ctx, attrs, ReasonNotListed, ErrNotListed.Wrapf("%s over %s", data.Denom, packet.DestinationChannel))
	}
	supply := m.host.Supply(ctx, voucher).Amount
	inFlight := m.host.InFlight(ctx, voucher)
	attrs = append(attrs,
		sdk.NewAttribute(AttributeSupply, supply.String()),
		sdk.NewAttribute(AttributeInFlight, inFlight.String()),
		sdk.NewAttribute(AttributeCap, entry.Cap.String()))
	// amount > room, rather than supply + in flight + amount > cap, so no
	// sum can pass 2^256-1 and panic before the sender gets its refund.
	room := sdkmath.ZeroInt()
	if held, ok := Held(supply, inFlight); ok {
		room = Room(entry.Cap, held)
	}
	if amount.GT(room) {
		return m.refuse(ctx, attrs, ReasonOverCap, ErrOverCap.Wrapf("%s %s over %s: supply %s, in flight %s, cap %s, room %s",
			amount, data.Denom, packet.DestinationChannel, supply, inFlight, entry.Cap, room))
	}
	return m.IBCModule.OnRecvPacket(ctx, packet, relayer)
}

// OnAcknowledgementPacket settles one of Aether's sends: transfer refunds
// it on an error acknowledgement, then its burn stops counting.
func (m IBCModule) OnAcknowledgementPacket(ctx sdk.Context, packet channeltypes.Packet, acknowledgement []byte, relayer sdk.AccAddress) error {
	return m.settle(ctx, packet, func() error {
		return m.IBCModule.OnAcknowledgementPacket(ctx, packet, acknowledgement, relayer)
	})
}

// OnTimeoutPacket settles one of Aether's sends that timed out: transfer
// refunds it, then its burn stops counting.
func (m IBCModule) OnTimeoutPacket(ctx sdk.Context, packet channeltypes.Packet, relayer sdk.AccAddress) error {
	return m.settle(ctx, packet, func() error {
		return m.IBCModule.OnTimeoutPacket(ctx, packet, relayer)
	})
}

// settle runs transfer's settlement of packet, then releases its
// in-flight record, refusing a settlement that leaves more of the
// voucher than there was (see IBCModule).
func (m IBCModule) settle(ctx sdk.Context, packet channeltypes.Packet, next func() error) error {
	if !m.host.Active(ctx) {
		return next()
	}
	rec, recorded := m.host.GetInFlightSend(ctx, packet.SourcePort, packet.SourceChannel, packet.Sequence)
	var before *big.Int
	if recorded {
		before = m.held(ctx, rec.VoucherDenom)
	}
	if err := next(); err != nil {
		return err
	}
	if !recorded {
		return nil
	}
	m.host.ReleaseInFlight(ctx, packet.SourcePort, packet.SourceChannel, packet.Sequence)
	if after := m.held(ctx, rec.VoucherDenom); after.Cmp(before) > 0 {
		return ErrSettlementGrew.Wrapf("%s %s over %s sequence %d: held %s before, %s after",
			rec.Amount, rec.VoucherDenom, packet.SourceChannel, packet.Sequence, before, after)
	}
	return nil
}

// held is voucher's supply plus its sends in flight, as a big.Int so the
// sum can't overflow (each alone is under 2^256).
func (m IBCModule) held(ctx sdk.Context, voucher string) *big.Int {
	return new(big.Int).Add(m.host.Supply(ctx, voucher).Amount.BigInt(), m.host.InFlight(ctx, voucher).BigInt())
}

// refuse answers with an error acknowledgement, before anything ran, and
// the other chain refunds the sender once it's relayed.
func (m IBCModule) refuse(ctx sdk.Context, attrs []sdk.Attribute, reason string, err error) ibcexported.Acknowledgement {
	attrs = append(attrs, sdk.NewAttribute(AttributeReason, reason))
	ctx.EventManager().EmitEvent(sdk.NewEvent(EventTypeRefused, attrs...))
	return channeltypes.NewErrorAcknowledgement(err)
}
