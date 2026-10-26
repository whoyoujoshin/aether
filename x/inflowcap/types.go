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
// The supply is read when the packet arrives, so there is no counter:
// vouchers sent back burn and free room, and a send that times out or
// fails re-mints what it burned.
package inflowcap

import (
	"fmt"

	sdkmath "cosmossdk.io/math"
	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	host "github.com/cosmos/ibc-go/v8/modules/core/24-host"
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
// carries the error's code, not its message.
const (
	EventTypeRefused = "inflow_cap_refused"

	AttributeChannel = "channel"
	AttributeSender  = "sender"
	AttributeDenom   = "denom"
	AttributeVoucher = "voucher_denom"
	AttributeAmount  = "amount"
	AttributeSupply  = "supply"
	AttributeCap     = "cap"
	AttributeReason  = "reason"
	ReasonNotListed  = "not_listed"
	ReasonOverCap    = "over_cap"
	ReasonBadAmount  = "bad_amount"
)

var (
	// params -> Params.
	KeyParams = []byte{0x01}
)

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
func DefaultGenesisState() GenesisState { return GenesisState{Params: Params{Entries: []Entry{}}} }

func (g GenesisState) Validate() error { return g.Params.Validate() }

// Room is how much more of e's voucher may come in with supply out.
func Room(capAmount, supply sdkmath.Int) sdkmath.Int {
	if supply.GTE(capAmount) {
		return sdkmath.ZeroInt()
	}
	return capAmount.Sub(supply)
}
