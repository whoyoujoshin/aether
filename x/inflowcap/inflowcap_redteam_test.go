package inflowcap_test

import (
	"fmt"
	"strings"
	"testing"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	channeltypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/x/inflowcap"
)

// Red-team of x/inflowcap. Each attack calls the real OnRecvPacket.
// A failing assertion is a bypass; a passing one proves the block.

// Denom tricks: trailing slash, double slash, uppercase, and a path
// whose segments look like a port/channel pair. None produce the same
// ibc/ hash as the listed voucher, so all are refused as not listed.
func TestRedTeam_DenomTricksDoNotMatchListed(t *testing.T) {
	listed := usdc
	voucher := inflowcap.VoucherDenom("channel-2", listed)
	h := host{active: true, params: inflowcap.Params{Entries: []inflowcap.Entry{entry("channel-2", listed, 5_000_000_000)}}}

	tricks := []string{
		listed + "/",                          // trailing slash
		listed + "//",                         // double trailing
		"//" + listed,                         // leading double slash
		"transfer/channel-2/" + listed,        // already prefixed with dest
		"TRANSFER/CHANNEL-2/" + listed,        // uppercase port/channel
		"ibc/" + strings.ToUpper(voucher[4:]), // uppercase IBC hash
		listed + "/extra",                     // extra segment
	}
	for _, d := range tricks {
		got := inflowcap.VoucherDenom("channel-2", d)
		require.NotEqual(t, voucher, got, "trick %q must not hash to the listed voucher", d)
		ack, n, cx := recv(t, h, packet(d, "1"))
		require.False(t, ack.Success(), d)
		require.Zero(t, n.calls, d)
		require.Equal(t, inflowcap.ReasonNotListed, refusedReason(t, cx), d)
	}
}

// A different string that ParseDenomTrace normalizes to the same full
// path would match. Confirm that the only way to get the listed hash
// is the listed denom itself (no normalization bypass).
func TestRedTeam_SameHashOnlyFromSameDenom(t *testing.T) {
	listed := usdc
	voucher := inflowcap.VoucherDenom("channel-2", listed)
	// Leading/trailing whitespace and case changes must not collide.
	for _, d := range []string{" " + listed, listed + " ", strings.ToUpper(listed), listed[:len(listed)-1]} {
		require.NotEqual(t, voucher, inflowcap.VoucherDenom("channel-2", d), d)
	}
}

// Faking "coming home": a denom prefixed with the sending side's port
// and channel is treated as returning. The middleware passes it. The
// transfer module then tries to unescrow the unprefixed denom and fails
// if nothing is escrowed, so nothing is minted. This test shows the
// middleware passes; transfer's failure to unescrow is what blocks the mint.
func TestRedTeam_FakeComingHomePassesMiddleware(t *testing.T) {
	// Source is channel-77152 (see packet()). Prefix with that.
	fake := "transfer/channel-77152/uaeth"
	ack, n, _ := recv(t, host{active: true}, packet(fake, "1000000"))
	require.True(t, ack.Success(), "middleware treats it as coming home")
	require.Equal(t, 1, n.calls, "passed to transfer, which must then fail to unescrow")
}

// Amounts: leading zeros (base-0, so octal), a plus sign, and a value
// near the 256-bit limit. NewIntFromString is what transfer uses too,
// so the cap and the mint see the same integer. A plus sign is accepted
// as positive. A leading zero makes the number octal, not a bypass.
func TestRedTeam_AmountEdgeCases(t *testing.T) {
	voucher := inflowcap.VoucherDenom("channel-2", usdc)
	h := host{active: true, params: inflowcap.Params{Entries: []inflowcap.Entry{entry("channel-2", usdc, 100)}},
		supply: map[string]sdkmath.Int{voucher: sdkmath.NewInt(50)}}

	// "0050" is octal 40, under the cap (room 50). Transfer sees 40 too.
	ack, n, _ := recv(t, h, packet(usdc, "0050"))
	require.True(t, ack.Success(), "leading-zero amount is octal, and under the cap")
	require.Equal(t, 1, n.calls)

	// "0100" is octal 64, over the remaining 50.
	ack, n, cx := recv(t, h, packet(usdc, "0100"))
	require.False(t, ack.Success())
	require.Zero(t, n.calls)
	require.Equal(t, inflowcap.ReasonOverCap, refusedReason(t, cx))

	// Plus sign: NewIntFromString accepts it as 50, under the cap.
	// Transfer accepts it the same way, so no bypass.
	ack, n, _ = recv(t, h, packet(usdc, "+50"))
	require.True(t, ack.Success(), "plus sign is a valid positive integer to both parsers")
	require.Equal(t, 1, n.calls)

	// Near 256-bit: a 78-digit number is positive and over the cap.
	huge := "1" + strings.Repeat("0", 77)
	ack, n, cx = recv(t, h, packet(usdc, huge))
	require.False(t, ack.Success())
	require.Zero(t, n.calls)
	require.Equal(t, inflowcap.ReasonOverCap, refusedReason(t, cx), "huge amount is checked, not overflowed")
}

// Find matches only the exact voucher. A packet whose denom hashes to
// the same ibc/ is the same voucher and is capped. A packet that hashes
// differently is not listed.
func TestRedTeam_FindIsExact(t *testing.T) {
	voucher := inflowcap.VoucherDenom("channel-2", usdc)
	p := inflowcap.Params{Entries: []inflowcap.Entry{entry("channel-2", usdc, 100)}}
	e, ok := p.Find("channel-2", voucher)
	require.True(t, ok)
	require.Equal(t, usdc, e.Denom)

	_, ok = p.Find("channel-2", inflowcap.VoucherDenom("channel-2", usdc+"/"))
	require.False(t, ok, "trailing-slash denom is a different voucher")
	_, ok = p.Find("channel-7", voucher)
	require.False(t, ok, "same voucher over another channel")
}

// 100 entries validate; 101 do not. Duplicates that differ only by
// denom spelling (different hash) are allowed; same hash is not.
func TestRedTeam_GovernanceListEdges(t *testing.T) {
	var hundred []inflowcap.Entry
	for i := 0; i < inflowcap.MaxEntries; i++ {
		hundred = append(hundred, entry("channel-2", fmt.Sprintf("d%d", i), 1))
	}
	require.NoError(t, inflowcap.Params{Entries: hundred}.Validate(), "100 entries")

	hundred = append(hundred, entry("channel-2", "d-extra", 1))
	require.Error(t, inflowcap.Params{Entries: hundred}.Validate(), "101 entries")

	// Different spelling, different hash: allowed.
	mixed := inflowcap.Params{Entries: []inflowcap.Entry{
		entry("channel-2", "usdc", 1),
		entry("channel-2", "USDC", 1),
	}}
	require.NoError(t, mixed.Validate())
	require.NotEqual(t, mixed.Entries[0].VoucherDenom(), mixed.Entries[1].VoucherDenom())
}

// A cap lowered below the current supply: Room is zero, a new receive
// is refused, existing supply is unchanged.
func TestRedTeam_CapLoweredBelowSupply(t *testing.T) {
	voucher := inflowcap.VoucherDenom("channel-2", usdc)
	h := host{active: true, params: inflowcap.Params{Entries: []inflowcap.Entry{entry("channel-2", usdc, 10)}},
		supply: map[string]sdkmath.Int{voucher: sdkmath.NewInt(100)}}
	require.True(t, inflowcap.Room(sdkmath.NewInt(10), sdkmath.NewInt(100)).IsZero())
	ack, n, cx := recv(t, h, packet(usdc, "1"))
	require.False(t, ack.Success())
	require.Zero(t, n.calls)
	require.Equal(t, inflowcap.ReasonOverCap, refusedReason(t, cx))
}

// Sanity: the voucher computation matches what transfer would mint.
func TestRedTeam_VoucherMatchesTransfer(t *testing.T) {
	prefixed := transfertypes.GetPrefixedDenom("transfer", "channel-2", usdc)
	want := transfertypes.ParseDenomTrace(prefixed).IBCDenom()
	require.Equal(t, want, inflowcap.VoucherDenom("channel-2", usdc))
	// And it is not treated as coming home when the source is different.
	require.False(t, transfertypes.ReceiverChainIsSource("transfer", "channel-77152", usdc))
	require.True(t, transfertypes.ReceiverChainIsSource("transfer", "channel-77152", "transfer/channel-77152/uaeth"))
}

// Packet with a non-transfer destination port is refused, not passed.
func TestRedTeam_WrongPortRefused(t *testing.T) {
	h := host{active: true, params: inflowcap.Params{Entries: []inflowcap.Entry{entry("channel-2", usdc, 100)}}}
	p := packet(usdc, "1")
	p.DestinationPort = "icahost"
	ack, n, cx := recv(t, h, p)
	require.False(t, ack.Success())
	require.Zero(t, n.calls)
	require.Equal(t, inflowcap.ReasonNotListed, refusedReason(t, cx))
}

// Unused import guard.
var _ = sdk.Context{}
var _ = channeltypes.Packet{}
