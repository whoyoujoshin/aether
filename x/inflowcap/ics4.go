package inflowcap

import (
	"crypto/sha256"
	"fmt"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	capabilitytypes "github.com/cosmos/ibc-go/modules/capability/types"
	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	porttypes "github.com/cosmos/ibc-go/v8/modules/core/05-port/types"
)

// ICS4Wrapper sits between the transfer keeper and the channel keeper,
// where each send's packet gets its sequence. A voucher sent back where
// it came from is burned before the packet leaves, and re-minted if the
// packet fails or times out, so it records the send as in flight until
// IBCModule sees it settled. Transfer's own sends and Ligase's withdraws
// both go through it.
type ICS4Wrapper struct {
	porttypes.ICS4Wrapper
	host Host
}

// NewICS4Wrapper wraps next, the channel keeper the transfer keeper
// would otherwise send through.
func NewICS4Wrapper(next porttypes.ICS4Wrapper, host Host) ICS4Wrapper {
	return ICS4Wrapper{ICS4Wrapper: next, host: host}
}

func (w ICS4Wrapper) SendPacket(
	ctx sdk.Context,
	chanCap *capabilitytypes.Capability,
	sourcePort, sourceChannel string,
	timeoutHeight clienttypes.Height,
	timeoutTimestamp uint64,
	data []byte,
) (uint64, error) {
	sequence, err := w.ICS4Wrapper.SendPacket(ctx, chanCap, sourcePort, sourceChannel, timeoutHeight, timeoutTimestamp, data)
	if err != nil || sourcePort != PortID || !w.host.Active(ctx) {
		return sequence, err
	}
	var d transfertypes.FungibleTokenPacketData
	if transfertypes.ModuleCdc.UnmarshalJSON(data, &d) != nil {
		return sequence, nil
	}
	if transfertypes.SenderChainIsSource(sourcePort, sourceChannel, d.Denom) {
		return sequence, nil // escrowed, not burned: the supply didn't move
	}
	amount, ok := sdkmath.NewIntFromString(d.Amount)
	if !ok || !amount.IsPositive() {
		return sequence, nil
	}
	w.host.AddInFlight(ctx, InFlightSend{
		PortId:       sourcePort,
		ChannelId:    sourceChannel,
		Sequence:     sequence,
		VoucherDenom: burnedVoucher(d.Denom),
		Amount:       amount,
	})
	return sequence, nil
}

// burnedVoucher is the voucher transfer burned to send fullDenomPath:
// the one whose trace that is, ibc/ and the hex of its sha256.
func burnedVoucher(fullDenomPath string) string {
	return fmt.Sprintf("%s/%X", transfertypes.DenomPrefix, sha256.Sum256([]byte(fullDenomPath)))
}
