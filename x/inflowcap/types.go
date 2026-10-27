// Package inflowcap caps how much of each token from another chain there
// may be on Aether, and refuses every token from another chain that
// isn't on its allowlist (docs/IBC-INFLOW-CAP.md).
//
// A voucher on Aether (ibc/...) is backed by the same amount locked in
// the counterparty's escrow for Aether's channel. Whatever a takeover of
// Aether, a bug here or a forged header on Aether's client of the other
// chain could take is at most that escrow, so the cap is on the stock:
// a received transfer that would take its voucher's supply over the cap
// gets an error acknowledgement, nothing is minted, and the other chain
// refunds the sender. Tokens coming home to Aether (AETH, or a voucher
// returning to where it was minted) only unlock Aether's own escrow and
// are never limited.
//
// The supply is read when the packet arrives. A voucher sent back burns,
// but a send that fails or times out re-mints what it burned, so until
// its packet is acknowledged or times out the burn still takes up room:
// the cap is on supply plus those sends in flight. Room frees only once
// a send back succeeds.
package inflowcap

import (
	"encoding/binary"
	"fmt"
	"strings"

	sdkmath "cosmossdk.io/math"
	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	host "github.com/cosmos/ibc-go/v8/modules/core/24-host"
	coretypes "github.com/cosmos/ibc-go/v8/modules/core/types"
)

const (
	ModuleName = "inflowcap"
	StoreKey   = ModuleName

	// The only port the cap applies to: the middleware wraps transfer.
	PortID = transfertypes.PortID

	// MaxEntries bounds the list each received packet looks through.
	MaxEntries = 100
)

// Events. A refusal's reason is only in the event: an acknowledgement
// carries the error's code, not its message. Because the acknowledgement
// is an error, ibc-go keeps the event but renames it and each of its
// keys with coretypes.ErrorAttributeKeyPrefix, so in a transaction's
// result it is RefusedEvent with keys RefusedKey(AttributeReason) etc.
const (
	EventTypeRefused = "inflow_cap_refused"

	AttributeChannel  = "channel"
	AttributeSender   = "sender"
	AttributeDenom    = "denom"
	AttributeVoucher  = "voucher_denom"
	AttributeAmount   = "amount"
	AttributeSupply   = "supply"
	AttributeInFlight = "in_flight"
	AttributeCap      = "cap"
	AttributeReason   = "reason"
	ReasonNotListed   = "not_listed"
	ReasonOverCap     = "over_cap"
	ReasonBadAmount   = "bad_amount"
)

// RefusedEvent is EventTypeRefused as it appears in a transaction's
// result (see the events comment).
const RefusedEvent = coretypes.ErrorAttributeKeyPrefix + EventTypeRefused

// RefusedKey is attribute key as it appears in RefusedEvent.
func RefusedKey(key string) string { return coretypes.ErrorAttributeKeyPrefix + key }

var (
	// params -> Params.
	KeyParams = []byte{0x01}
	// port/channel/ + big-endian sequence -> InFlightSend.
	KeyInFlightSendPrefix = []byte{0x02}
	// voucher -> the sum of its InFlightSends' amounts.
	KeyInFlightTotalPrefix = []byte{0x03}
)

// InFlightSendKey is the key of the send of packet sequence over port and
// channel. Identifiers never contain "/".
func InFlightSendKey(portID, channelID string, sequence uint64) []byte {
	k := append(append([]byte{}, KeyInFlightSendPrefix...), portID+"/"+channelID+"/"...)
	return binary.BigEndian.AppendUint64(k, sequence)
}

// InFlightTotalKey is the key of voucher's in-flight total.
func InFlightTotalKey(voucher string) []byte {
	return append(append([]byte{}, KeyInFlightTotalPrefix...), voucher...)
}

// VoucherDenom is the denom a token sent as denom over Aether's end
// channelID of a transfer channel gets on Aether, as the transfer module
// mints it.
func VoucherDenom(channelID, denom string) string {
	return transfertypes.ParseDenomTrace(transfertypes.GetPrefixedDenom(PortID, channelID, denom)).IBCDenom()
}

// VoucherDenom is e's voucher on Aether.
func (e Entry) VoucherDenom() string { return VoucherDenom(e.ChannelId, e.Denom) }

func (e Entry) validate() error {
	if err := host.ChannelIdentifierValidator(e.ChannelId); err != nil {
		return fmt.Errorf("channel_id %q: %w", e.ChannelId, err)
	}
	if e.Denom == "" {
		return fmt.Errorf("denom is empty")
	}
	if err := transfertypes.ValidatePrefixedDenom(transfertypes.GetPrefixedDenom(PortID, e.ChannelId, e.Denom)); err != nil {
		return fmt.Errorf("denom %q: %w", e.Denom, err)
	}
	if e.Cap.IsNil() || e.Cap.IsNegative() {
		return fmt.Errorf("cap must be 0 or more")
	}
	return nil
}

// Validate checks every entry, and that no voucher is listed twice.
func (p Params) Validate() error {
	if len(p.Entries) > MaxEntries {
		return fmt.Errorf("%d entries; at most %d", len(p.Entries), MaxEntries)
	}
	seen := map[string]bool{}
	for i, e := range p.Entries {
		if err := e.validate(); err != nil {
			return fmt.Errorf("entry %d: %w", i, err)
		}
		v := e.VoucherDenom()
		if seen[v] {
			return fmt.Errorf("entry %d: %s over %s is already listed", i, e.Denom, e.ChannelId)
		}
		seen[v] = true
	}
	return nil
}

// Find is the entry for voucher received over channelID, if listed.
func (p Params) Find(channelID, voucher string) (Entry, bool) {
	for _, e := range p.Entries {
		if e.ChannelId == channelID && e.VoucherDenom() == voucher {
			return e, true
		}
	}
	return Entry{}, false
}

// DefaultGenesisState lists nothing: every token from another chain is
// refused until genesis or governance lists it.
func DefaultGenesisState() GenesisState {
	return GenesisState{Params: Params{Entries: []Entry{}}, InFlight: []InFlightSend{}}
}

func (g GenesisState) Validate() error {
	if err := g.Params.Validate(); err != nil {
		return err
	}
	seen := make(map[string]bool, len(g.InFlight))
	for i, s := range g.InFlight {
		if err := s.Validate(); err != nil {
			return fmt.Errorf("in_flight %d: %w", i, err)
		}
		k := string(InFlightSendKey(s.PortId, s.ChannelId, s.Sequence))
		if seen[k] {
			return fmt.Errorf("in_flight %d: %s/%s sequence %d listed twice", i, s.PortId, s.ChannelId, s.Sequence)
		}
		seen[k] = true
	}
	return nil
}

func (s InFlightSend) Validate() error {
	if err := host.PortIdentifierValidator(s.PortId); err != nil {
		return err
	}
	if err := host.ChannelIdentifierValidator(s.ChannelId); err != nil {
		return err
	}
	if s.Sequence == 0 {
		return fmt.Errorf("sequence 0")
	}
	if err := transfertypes.ValidateIBCDenom(s.VoucherDenom); err != nil || !strings.HasPrefix(s.VoucherDenom, transfertypes.DenomPrefix+"/") {
		return fmt.Errorf("voucher %q is not an ibc/ denom", s.VoucherDenom)
	}
	if s.Amount.IsNil() || !s.Amount.IsPositive() {
		return fmt.Errorf("amount must be positive")
	}
	return nil
}

// Held is supply plus inFlight, what a cap is measured against, and
// false if that overflows a 256-bit amount.
func Held(supply, inFlight sdkmath.Int) (sdkmath.Int, bool) {
	held, err := supply.SafeAdd(inFlight)
	return held, err == nil
}

// Room is how much more of a voucher may come in with held (supply plus
// in flight) out under capAmount.
func Room(capAmount, held sdkmath.Int) sdkmath.Int {
	if held.GTE(capAmount) {
		return sdkmath.ZeroInt()
	}
	return capAmount.Sub(held)
}
