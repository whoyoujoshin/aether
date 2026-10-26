package inflowcap

import (
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
}

// IBCModule wraps the transfer route's IBC callbacks. Only OnRecvPacket
// changes: acknowledgements and timeouts of Aether's own sends pass
// through, since a refund re-mints only what the send burned.
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
	attrs = append(attrs, sdk.NewAttribute(AttributeSupply, supply.String()), sdk.NewAttribute(AttributeCap, entry.Cap.String()))
	if supply.Add(amount).GT(entry.Cap) {
		return m.refuse(ctx, attrs, ReasonOverCap, ErrOverCap.Wrapf("%s %s over %s: supply %s, cap %s, room %s",
			amount, data.Denom, packet.DestinationChannel, supply, entry.Cap, Room(entry.Cap, supply)))
	}
	return m.IBCModule.OnRecvPacket(ctx, packet, relayer)
}

// refuse answers with an error acknowledgement, before anything ran, and
// the other chain refunds the sender once it's relayed.
func (m IBCModule) refuse(ctx sdk.Context, attrs []sdk.Attribute, reason string, err error) ibcexported.Acknowledgement {
	attrs = append(attrs, sdk.NewAttribute(AttributeReason, reason))
	ctx.EventManager().EmitEvent(sdk.NewEvent(EventTypeRefused, attrs...))
	return channeltypes.NewErrorAcknowledgement(err)
}
