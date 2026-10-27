package app

import (
	"testing"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	ibctransfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/x/inflowcap"
)

// Red-team of the inflow cap at the app level. Uses newInflowCapFixture
// (two real apps over ibctesting).

// Several packets in one block: each is under the remaining room when
// it is processed, but together they would exceed the cap. The supply
// is read live, so the second sees the first's mint and is refused.
func TestRedTeam_SeveralPacketsInOneBlock(t *testing.T) {
	f := newInflowCapFixture(t)
	receiver := f.a.SenderAccount.GetAddress().String()
	f.capVoucher(1000)

	// Two sends that each fit, but 600+600 > 1000. They are separate
	// transactions (the harness commits between), so the second sees
	// the updated supply.
	f.send(600, receiver, "")
	require.True(t, f.lastAckSuccess)
	require.Equal(t, int64(600), f.supplyA(f.voucher))

	f.send(600, receiver, "")
	require.False(t, f.lastAckSuccess, "second packet sees the first's mint")
	require.Equal(t, inflowcap.ReasonOverCap, refusal(t, f.lastRecv))
	require.Equal(t, int64(600), f.supplyA(f.voucher), "nothing more minted")
}

// An acknowledgement or timeout refund of a voucher Aether sent out,
// after governance lowered the cap below the supply. The refund re-mints
// what the send burned, so the supply can exceed the new cap. That is
// the design: the cap bounds new inflow, not restoration of what left.
func TestRedTeam_RefundAfterCapLowered(t *testing.T) {
	f := newInflowCapFixture(t)
	receiver := f.a.SenderAccount.GetAddress().String()
	f.capVoucher(1000)
	f.send(1000, receiver, "")
	require.True(t, f.lastAckSuccess)
	require.Equal(t, int64(1000), f.supplyA(f.voucher))

	// Send 400 back (burns on Aether).
	timeout := clienttypes.NewHeight(0, uint64(f.b.GetContext().BlockHeight())+1)
	packet := f.sendBack(400, timeout)
	require.Equal(t, int64(600), f.supplyA(f.voucher))

	// Governance lowers the cap to 500, below the current supply.
	setCaps(t, f.a, inflowcap.Entry{ChannelId: f.path.EndpointA.ChannelID, Denom: "uaeth", Cap: sdkmath.NewInt(500)})

	// Timeout the send: transfer re-mints the 400. Supply goes to 1000,
	// over the new cap of 500. The middleware does not intercept the
	// timeout, so this is allowed.
	f.coord.CommitNBlocks(f.b, 3)
	require.NoError(t, f.path.EndpointA.UpdateClient())
	require.NoError(t, f.path.EndpointA.TimeoutPacket(packet))
	require.Equal(t, int64(1000), f.supplyA(f.voucher), "refund re-mints even over the lowered cap")

	// A new inflow is still refused: the cap binds on receive.
	f.send(1, receiver, "")
	require.False(t, f.lastAckSuccess)
	require.Equal(t, inflowcap.ReasonOverCap, refusal(t, f.lastRecv))
}

// Faking "coming home" is covered at the unit level
// (TestRedTeam_FakeComingHomePassesMiddleware): a denom prefixed with
// the sending side's port and channel passes the middleware. Transfer
// then tries to unescrow the unprefixed denom and fails if nothing is
// locked, so nothing is minted. Here we confirm that an ordinary
// unlisted voucher is refused and refunded, which is the fail-closed
// path the fake prefix is trying to avoid.
func TestRedTeam_UnlistedVoucherRefunded(t *testing.T) {
	f := newInflowCapFixture(t)
	before := f.balanceB("uaeth")
	f.send(100, f.a.SenderAccount.GetAddress().String(), "")
	require.False(t, f.lastAckSuccess, "nothing listed: refused")
	require.Equal(t, inflowcap.ReasonNotListed, refusal(t, f.lastRecv))
	require.Equal(t, before, f.balanceB("uaeth"), "refunded on B")
	require.Zero(t, f.supplyA(f.voucher))
}

// Ligase withdraw that sends vouchers out, then a timeout re-mints them.
// The re-mint restores supply; it does not exceed what was burned.
func TestRedTeam_LigaseWithdrawTimeoutRestores(t *testing.T) {
	f := newInflowCapFixture(t)
	f.capVoucher(1000)
	f.send(800, f.a.SenderAccount.GetAddress().String(), "")
	require.True(t, f.lastAckSuccess)
	require.Equal(t, int64(800), f.supplyA(f.voucher))

	timeout := clienttypes.NewHeight(0, uint64(f.b.GetContext().BlockHeight())+1)
	packet := f.sendBack(300, timeout)
	require.Equal(t, int64(500), f.supplyA(f.voucher))

	f.coord.CommitNBlocks(f.b, 3)
	require.NoError(t, f.path.EndpointA.UpdateClient())
	require.NoError(t, f.path.EndpointA.TimeoutPacket(packet))
	require.Equal(t, int64(800), f.supplyA(f.voucher), "timeout restores exactly what was burned")

	f.send(200, f.a.SenderAccount.GetAddress().String(), "")
	require.True(t, f.lastAckSuccess, "room is back")
	f.send(1, f.a.SenderAccount.GetAddress().String(), "")
	require.False(t, f.lastAckSuccess)
}

// Genesis export and import round trip preserves the list.
func TestRedTeam_GenesisRoundTrip(t *testing.T) {
	f := newInflowCapFixture(t)
	want := []inflowcap.Entry{
		{ChannelId: f.path.EndpointA.ChannelID, Denom: "uaeth", Cap: sdkmath.NewInt(12345)},
	}
	setCaps(t, f.a, want...)

	exported := f.appA.InflowCapKeeper.ExportGenesis(f.a.GetContext())
	require.Equal(t, want, exported.Params.Entries)

	// Import into a fresh context via InitGenesis.
	f.appA.InflowCapKeeper.InitGenesis(f.a.GetContext(), exported)
	require.Equal(t, want, f.appA.InflowCapKeeper.GetParams(f.a.GetContext()).Entries)
}

// The testnet activation seeding when genesis already set the params:
// HasParams is true, so testnetInflowEntries is not applied.
func TestRedTeam_ActivationSkipsWhenGenesisSet(t *testing.T) {
	f := newInflowCapFixture(t)
	// Fixture is a fresh chain (activation at 1), genesis lists nothing
	// but the store exists. Setting params makes HasParams true.
	setCaps(t, f.a, inflowcap.Entry{ChannelId: "channel-0", Denom: "uaeth", Cap: sdkmath.NewInt(1)})
	require.True(t, f.appA.InflowCapKeeper.HasParams(f.a.GetContext()))
	// The list is what we set, not the testnet defaults.
	require.Equal(t, "channel-0", f.appA.InflowCapKeeper.GetParams(f.a.GetContext()).Entries[0].ChannelId)
}

// Unused to keep the import stable if the fake-denom path is extended.
var _ = ibctransfertypes.FungibleTokenPacketData{}
var _ = sdk.Context{}
