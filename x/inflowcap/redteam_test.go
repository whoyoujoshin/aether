package inflowcap_test

import (
	"math/big"
	"strings"
	"testing"

	sdkmath "cosmossdk.io/math"
	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/x/inflowcap"
)

// transferMintDenom is the denom ibc-go's transfer OnRecvPacket mints for
// a packet whose destination channel is channel-2, or ("", false) when
// the packet takes the unescrow path (ReceiverChainIsSource) and mints
// nothing. It reimplements that branch so a test can compare it with the
// denom the cap computes.
func transferMintDenom(destChannel, dataDenom string) (string, bool) {
	const sourceChannel = "channel-77152" // packet()
	if transfertypes.ReceiverChainIsSource(transfertypes.PortID, sourceChannel, dataDenom) {
		return "", false
	}
	prefixed := transfertypes.GetDenomPrefix(transfertypes.PortID, destChannel) + dataDenom
	return transfertypes.ParseDenomTrace(prefixed).IBCDenom(), true
}

// Denom strings an attacker can put in a packet, or a governor can put in
// an entry. The question is whether any of them hash to a listed voucher
// while Find misses them, or the other way round.
func denomVariants(channel string) []string {
	hash := strings.TrimPrefix(inflowcap.VoucherDenom(channel, usdc), "ibc/")
	return []string{
		usdc,
		usdc + "/",
		usdc + "//",
		"/" + usdc,
		"//" + usdc,
		usdc + "/" + usdc,
		"transfer/" + channel + "/" + usdc,
		"transfer/channel-77152/" + usdc,
		"transfer/channel-77152/" + usdc + "/",
		"transfer/channel-77152//" + usdc,
		"Transfer/channel-77152/" + usdc,
		"TRANSFER/channel-77152/" + usdc,
		"transfer/CHANNEL-77152/" + usdc,
		"transfer/channel-77152/" + strings.ToUpper(usdc),
		"ibc/" + hash,
		"IBC/" + hash,
		"ibc/" + strings.ToLower(hash),
		"IBC/" + strings.ToLower(hash),
		"ibc/" + hash + "/",
		usdc + "/channel-0/pool",
		"foo/channel-0/" + usdc,
		"transfer/channel-0/" + usdc,
		"transfer/channel-0/transfer/channel-1/" + usdc,
		"gamm/pool/1",
		"transfer/channel-01/" + usdc,
		"port/channel-9/" + usdc,
		strings.ToUpper(usdc),
		" " + usdc,
		usdc + " ",
		usdc + "\n",
		"%s",
		"%d/" + usdc,
		usdc + "\u200b",
		"transfer//channel-77152/" + usdc,
		"transfer/channel-77152",
		"",
		"/",
		"//",
		"transfer/",
		"ibc/",
		"IBC/",
	}
}

// The cap and the transfer module must name the same coin. A denom that
// the cap treats as unlisted (or as a different listed coin) while
// transfer mints the listed voucher would bypass the cap; the reverse
// would mint an unlisted coin the cap had allowed.
func TestRedTeam_MiddlewareVoucherMatchesTransferMint(t *testing.T) {
	const channel = "channel-2"
	listed := inflowcap.VoucherDenom(channel, usdc)
	for _, denom := range denomVariants(channel) {
		minted, mints := transferMintDenom(channel, denom)
		capped := inflowcap.VoucherDenom(channel, denom)
		if !mints {
			// Coming home: transfer unescrows and mints nothing. The cap
			// must not have hashed this into the listed voucher and then
			// skipped the check — the skip is the prefix test, and the
			// coin transfer will move is the unprefixed denom, never a
			// fresh mint of `listed`.
			require.True(t, transfertypes.ReceiverChainIsSource(transfertypes.PortID, "channel-77152", denom), denom)
			continue
		}
		require.Equal(t, minted, capped, "denom %q: cap voucher %s, transfer mints %s", denom, capped, minted)
		require.True(t, strings.HasPrefix(minted, "ibc/"), "denom %q minted native %s", denom, minted)
		if minted == listed {
			require.Equal(t, usdc, denom, "only the listed spelling may mint the listed voucher, got %q", denom)
		}
		slash := transfertypes.GetPrefixedDenom(transfertypes.PortID, channel, denom)
		plus := transfertypes.GetDenomPrefix(transfertypes.PortID, channel) + denom
		require.Equal(t, slash, plus, "prefix builders disagree on %q", denom)
	}
}

// Two entries that mint one coin must not both be listable, whatever the
// spelling. Find returns the first; a second cap on the same coin would
// be a dead entry, and a spelling the check misses would be a bypass.
func TestRedTeam_SameVoucherCannotBeListedTwice(t *testing.T) {
	const channel = "channel-2"
	seen := map[string]string{}
	for _, denom := range denomVariants(channel) {
		e := inflowcap.Entry{ChannelId: channel, Denom: denom, Cap: sdkmath.NewInt(1)}
		one := inflowcap.Params{Entries: []inflowcap.Entry{e}}
		if err := one.Validate(); err != nil {
			continue // not a legal entry
		}
		v := e.VoucherDenom()
		if prev, ok := seen[v]; ok {
			p := inflowcap.Params{Entries: []inflowcap.Entry{
				{ChannelId: channel, Denom: prev, Cap: sdkmath.NewInt(1)},
				e,
			}}
			require.Error(t, p.Validate(), "voucher %s listed as %q and %q", v, prev, denom)
		}
		seen[v] = denom
	}
	// The spelling that does collide with itself is rejected.
	twice := inflowcap.Params{Entries: []inflowcap.Entry{
		{ChannelId: channel, Denom: usdc, Cap: sdkmath.NewInt(1)},
		{ChannelId: channel, Denom: usdc, Cap: sdkmath.NewInt(2)},
	}}
	require.Error(t, twice.Validate())
	// Different coins may share a channel.
	distinct := inflowcap.Params{Entries: []inflowcap.Entry{
		{ChannelId: channel, Denom: usdc, Cap: sdkmath.NewInt(1)},
		{ChannelId: channel, Denom: strings.ToUpper(usdc), Cap: sdkmath.NewInt(1)},
		{ChannelId: channel, Denom: "transfer/channel-0/" + usdc, Cap: sdkmath.NewInt(1)},
	}}
	require.NoError(t, distinct.Validate())
}

func maxUint256() sdkmath.Int {
	n := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	return sdkmath.NewIntFromBigInt(n)
}

// supply + amount is compared with Int.Add, which panics past 2^256-1.
// Once any of the voucher exists, a packet whose amount is the 256-bit
// maximum panics the receive instead of refusing it, so no error
// acknowledgement is written and the sender's refund waits for timeout.
func TestRedTeam_SupplyPlusAmountOverflowPanics(t *testing.T) {
	voucher := inflowcap.VoucherDenom("channel-2", usdc)
	h := host{
		active: true,
		params: inflowcap.Params{Entries: []inflowcap.Entry{entry("channel-2", usdc, 5_000_000_000)}},
		supply: map[string]sdkmath.Int{voucher: sdkmath.NewInt(1)},
	}
	require.NotPanics(t, func() {
		ack, n, cx := recv(t, h, packet(usdc, maxUint256().String()))
		require.False(t, ack.Success())
		require.Zero(t, n.calls, "transfer must not run")
		require.Equal(t, inflowcap.ReasonOverCap, refusedReason(t, cx))
	}, "supply 1 + (2^256-1) must be refused, not panic; use Int.SafeAdd")
}

// The largest sum that still fits in 256 bits is refused normally, and so
// is the maximum amount when supply is still zero.
func TestRedTeam_Uint256BoundaryRefusesWithoutPanic(t *testing.T) {
	voucher := inflowcap.VoucherDenom("channel-2", usdc)
	max := maxUint256()
	justFits := max.Sub(sdkmath.NewInt(5))
	h := host{
		active: true,
		params: inflowcap.Params{Entries: []inflowcap.Entry{entry("channel-2", usdc, 5_000_000_000)}},
		supply: map[string]sdkmath.Int{voucher: sdkmath.NewInt(5)},
	}
	require.NotPanics(t, func() {
		ack, n, cx := recv(t, h, packet(usdc, justFits.String()))
		require.False(t, ack.Success())
		require.Zero(t, n.calls)
		require.Equal(t, inflowcap.ReasonOverCap, refusedReason(t, cx))
	})

	h.supply = nil
	require.NotPanics(t, func() {
		ack, n, _ := recv(t, h, packet(usdc, max.String()))
		require.False(t, ack.Success())
		require.Zero(t, n.calls)
	})

	// One past the type's range is not a number the cap accepts.
	over := new(big.Int).Lsh(big.NewInt(1), 256)
	ack, n, cx := recv(t, h, packet(usdc, over.String()))
	require.False(t, ack.Success())
	require.Zero(t, n.calls)
	require.Equal(t, inflowcap.ReasonBadAmount, refusedReason(t, cx))
}

// Amount strings the cap and transfer both parse with math.NewIntFromString
// (base 0: leading 0 is octal, 0x is hex, underscores are ignored, '+' is a
// sign). The cap's integer is the integer transfer would mint, so none of
// these mint more than the cap counted. Strings that are not a positive
// integer are refused before transfer runs.
func TestRedTeam_AmountParsingMatchesWhatWouldMint(t *testing.T) {
	voucher := inflowcap.VoucherDenom("channel-2", usdc)
	h := host{
		active: true,
		params: inflowcap.Params{Entries: []inflowcap.Entry{entry("channel-2", usdc, 100)}},
		supply: map[string]sdkmath.Int{voucher: sdkmath.NewInt(0)},
	}
	cases := []struct {
		amount string
		parsed string // empty: refused as a bad amount
	}{
		{"1", "1"},
		{"+1", "1"},
		{"+10", "10"},
		{"00010", "8"}, // octal
		{"010", "8"},   // octal
		{"0x10", "16"}, // hex
		{"0b10", "2"},  // binary
		{"1_0", "10"},  // underscores
		{"100", "100"}, // exactly the cap
		{"101", ""},    // over the cap, but a legal integer: over_cap, not bad_amount
		{"0", ""},
		{"+0", ""},
		{"-1", ""},
		{"+1", "1"},
		{"08", ""}, // 8 is not an octal digit
		{"1.5", ""},
		{"1e2", ""},
		{" 10", ""},
		{"10 ", ""},
		{"", ""},
		{"+", ""},
		{"0x", ""},
	}
	for _, c := range cases {
		ack, n, cx := recv(t, h, packet(usdc, c.amount))
		parsed, ok := sdkmath.NewIntFromString(c.amount)
		if c.parsed == "" || !ok || !parsed.IsPositive() || parsed.GT(sdkmath.NewInt(100)) {
			require.False(t, ack.Success(), c.amount)
			require.Zero(t, n.calls, c.amount)
			if !ok || !parsed.IsPositive() {
				require.Equal(t, inflowcap.ReasonBadAmount, refusedReason(t, cx), c.amount)
			} else {
				require.Equal(t, inflowcap.ReasonOverCap, refusedReason(t, cx), c.amount)
			}
			continue
		}
		require.Equal(t, c.parsed, parsed.String(), c.amount)
		require.True(t, ack.Success(), "%s parsed as %s must pass a cap of 100", c.amount, parsed)
		require.Equal(t, 1, n.calls, c.amount)
	}
}

// A coming-home denom is not capped, however large the amount and however
// empty the allowlist. The app test shows transfer then unescrows (and
// fails closed when the escrow is short) instead of minting.
func TestRedTeam_FakeComingHomeSkipsTheCap(t *testing.T) {
	for _, denom := range []string{
		"transfer/channel-77152/uaeth",
		"transfer/channel-77152/" + usdc,
		"transfer/channel-77152/" + inflowcap.VoucherDenom("channel-2", usdc),
		"transfer/channel-77152/",
	} {
		if !transfertypes.ReceiverChainIsSource(transfertypes.PortID, "channel-77152", denom) {
			continue
		}
		ack, n, _ := recv(t, host{active: true}, packet(denom, maxUint256().String()))
		require.True(t, ack.Success(), denom)
		require.Equal(t, 1, n.calls, denom)
	}
	// A prefix that only looks similar is a new voucher, and unlisted.
	for _, denom := range []string{
		"transfer/channel-771520/uaeth", // longer channel, the trailing slash saves us
		"Transfer/channel-77152/uaeth",
		"transfer/channel-77152", // no trailing slash, so the prefix test fails
		"transfer//channel-77152/uaeth",
	} {
		require.False(t, transfertypes.ReceiverChainIsSource(transfertypes.PortID, "channel-77152", denom), denom)
		ack, n, cx := recv(t, host{active: true}, packet(denom, "1"))
		require.False(t, ack.Success(), denom)
		require.Zero(t, n.calls, denom)
		require.Equal(t, inflowcap.ReasonNotListed, refusedReason(t, cx), denom)
	}
}

// Weird packet bytes that the cap cannot decode are handed to transfer.
// Transfer uses the same JSON decoder, so it refuses them too; this only
// checks the cap doesn't itself accept them.
func TestRedTeam_UndecodableStillNotAVoucher(t *testing.T) {
	payloads := [][]byte{
		[]byte("not json"),
		[]byte(`{"denom":"uosmo","amount":1,"sender":"s","receiver":"r","memo":""}`),
		[]byte(`{"denom":"uosmo","amount":"1","sender":"s","receiver":"r","memo":"","amount":"999999"}`),
		{0x0a, 0x05, 0x75, 0x6f, 0x73, 0x6d, 0x6f}, // a protobuf-ish blob, not ICS-20 JSON
	}
	for _, data := range payloads {
		p := packet(usdc, "1")
		p.Data = data
		var decoded transfertypes.FungibleTokenPacketData
		capErr := transfertypes.ModuleCdc.UnmarshalJSON(data, &decoded)
		var again transfertypes.FungibleTokenPacketData
		transferErr := transfertypes.ModuleCdc.UnmarshalJSON(data, &again)
		require.Equal(t, capErr == nil, transferErr == nil, "cap and transfer disagree on %q", data)
		if capErr == nil {
			require.Equal(t, decoded.Amount, again.Amount)
			ack, _, _ := recv(t, host{active: true, params: inflowcap.Params{Entries: []inflowcap.Entry{entry("channel-2", usdc, 100)}}}, p)
			parsed, ok := sdkmath.NewIntFromString(decoded.Amount)
			if !ok || !parsed.IsPositive() || parsed.GT(sdkmath.NewInt(100)) || decoded.Denom != usdc {
				require.False(t, ack.Success(), "decoded amount %q denom %q", decoded.Amount, decoded.Denom)
			}
			continue
		}
		ack, n, _ := recv(t, host{active: true}, p)
		require.True(t, ack.Success(), "undecodable packets are transfer's to refuse")
		require.Equal(t, 1, n.calls)
	}
}

// Channel/port on the packet are not the attacker's to choose (the
// channel end fixes them), but a packet that did arrive on another port
// is refused rather than minted uncounted.
func TestRedTeam_OtherPortRefused(t *testing.T) {
	p := packet(usdc, "1")
	p.DestinationPort = "icahost"
	h := host{active: true, params: inflowcap.Params{Entries: []inflowcap.Entry{entry("channel-2", usdc, 100)}}}
	ack, n, cx := recv(t, h, p)
	require.False(t, ack.Success())
	require.Zero(t, n.calls)
	require.Equal(t, inflowcap.ReasonNotListed, refusedReason(t, cx))
}
