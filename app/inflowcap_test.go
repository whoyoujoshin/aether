package app

import (
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	ibctransfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	channeltypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	ibctesting "github.com/cosmos/ibc-go/v8/testing"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/gates"
	"github.com/whoyoujoshin/aether/ligase"
	"github.com/whoyoujoshin/aether/x/governance"
	"github.com/whoyoujoshin/aether/x/inflowcap"
)

func TestPlanInflowCap(t *testing.T) {
	const activation = 100
	for _, c := range []struct {
		lastCommitted int64
		want          inflowCapPlan
	}{
		{0, inflowCapPlan{}},
		{98, inflowCapPlan{}},
		{99, inflowCapPlan{wire: true, addStores: true}},
		{100, inflowCapPlan{wire: true}},
		{5000, inflowCapPlan{wire: true}},
	} {
		require.Equal(t, c.want, planInflowCap(c.lastCommitted, activation), "last committed height %d", c.lastCommitted)
	}
}

// newInflowCapFixture is the Ligase fixture (Aether is chain A; B's uaeth
// arrives on A as a voucher, standing in for USDC from Injective) with
// the inflow cap live on both chains from block 1. Both start from the
// default genesis, which lists nothing.
func newInflowCapFixture(t *testing.T) *ligaseFixture {
	orig := inflowCapActivationHeight
	inflowCapActivationHeight = 1
	t.Cleanup(func() { inflowCapActivationHeight = orig })
	f := newLigaseFixture(t, 0)
	require.True(t, f.appA.inflowCapWired)
	return f
}

// setCaps replaces chain's list as governance does: MsgUpdateParams
// signed by the governance module account, through the Msg router.
func setCaps(t *testing.T, chain *ibctesting.TestChain, entries ...inflowcap.Entry) {
	t.Helper()
	app := chain.App.(*App)
	msg := &inflowcap.MsgUpdateParams{
		Authority: authtypes.NewModuleAddress(governance.ModuleName).String(),
		Params:    inflowcap.Params{Entries: entries},
	}
	_, err := app.MsgServiceRouter().Handler(msg)(chain.GetContext(), msg)
	require.NoError(t, err)
}

func (f *ligaseFixture) capVoucher(capAmount int64) {
	setCaps(f.t, f.a, inflowcap.Entry{ChannelId: f.path.EndpointA.ChannelID, Denom: "uaeth", Cap: sdkmath.NewInt(capAmount)})
}

func (f *ligaseFixture) supplyA(denom string) int64 {
	return f.appA.BankKeeper.GetSupply(f.a.GetContext(), denom).Amount.Int64()
}

// sendBack sends amount of the voucher from A's sender to B's, and
// returns the packet, unrelayed.
func (f *ligaseFixture) sendBack(amount int64, timeout clienttypes.Height) channeltypes.Packet {
	f.t.Helper()
	f.a.SendMsgsOverride = nil
	msg := ibctransfertypes.NewMsgTransfer("transfer", f.path.EndpointA.ChannelID, sdk.NewInt64Coin(f.voucher, amount),
		f.a.SenderAccount.GetAddress().String(), f.remote, timeout, 0, "")
	res, err := f.a.SendMsgs(msg)
	require.NoError(f.t, err)
	packet, err := ibctesting.ParsePacketFromEvents(res.Events)
	require.NoError(f.t, err)
	return packet
}

// A transfer over the cap gets an error acknowledgement, nothing is
// minted on Aether, and the sender's chain refunds it.
func TestInflowCap_RefusedTransferIsRefunded(t *testing.T) {
	f := newInflowCapFixture(t)
	receiver := f.a.SenderAccount.GetAddress().String()

	f.send(10, receiver, "")
	require.False(t, f.lastAckSuccess, "nothing listed: refused")
	require.Equal(t, inflowcap.ReasonNotListed, refusal(t, f.lastRecv))
	require.Zero(t, f.supplyA(f.voucher))

	f.capVoucher(1000)
	f.send(600, receiver, "")
	require.True(t, f.lastAckSuccess)
	f.send(400, receiver, "")
	require.True(t, f.lastAckSuccess, "up to the cap exactly")
	require.Equal(t, int64(1000), f.supplyA(f.voucher))

	before := f.balanceB("uaeth")
	f.send(1, receiver, "")
	require.False(t, f.lastAckSuccess, "one over")
	require.Equal(t, inflowcap.ReasonOverCap, refusal(t, f.lastRecv), "the reason survives ibc-go's error-ack renaming")
	require.Equal(t, before, f.balanceB("uaeth"), "refunded on B")
	require.Equal(t, int64(1000), f.supplyA(f.voucher))

	caps, err := inflowcap.NewQueryServerImpl(f.appA.InflowCapKeeper).Caps(f.a.GetContext(), &inflowcap.QueryCapsRequest{})
	require.NoError(t, err)
	require.Len(t, caps.Caps, 1)
	require.Equal(t, f.voucher, caps.Caps[0].VoucherDenom)
	require.Equal(t, int64(1000), caps.Caps[0].Supply.Int64())
	require.True(t, caps.Caps[0].Room.IsZero())
}

// refusal is the reason in a receive's inflow_cap_refused event, as
// ibc-go renames it for an error acknowledgement.
func refusal(t *testing.T, events []abci.Event) string {
	t.Helper()
	for _, ev := range events {
		if ev.Type != inflowcap.RefusedEvent {
			continue
		}
		for _, a := range ev.Attributes {
			if a.Key == inflowcap.RefusedKey(inflowcap.AttributeReason) {
				return a.Value
			}
		}
	}
	return ""
}

// Vouchers sent back burn on Aether, so the room they took is free again.
func TestInflowCap_SendingBackFreesRoom(t *testing.T) {
	f := newInflowCapFixture(t)
	receiver := f.a.SenderAccount.GetAddress().String()
	f.capVoucher(1000)
	f.send(1000, receiver, "")
	require.True(t, f.lastAckSuccess)

	packet := f.sendBack(300, clienttypes.NewHeight(0, 1_000_000))
	require.NoError(t, f.path.RelayPacket(packet))
	require.Equal(t, int64(700), f.supplyA(f.voucher))

	f.send(300, receiver, "")
	require.True(t, f.lastAckSuccess, "room freed by the send back")
	f.send(1, receiver, "")
	require.False(t, f.lastAckSuccess)
}

// A send back that times out is refunded on Aether: the voucher is
// minted again and the supply is where it was, so the cap still holds.
func TestInflowCap_TimedOutSendRestoresSupply(t *testing.T) {
	f := newInflowCapFixture(t)
	receiver := f.a.SenderAccount.GetAddress().String()
	f.capVoucher(1000)
	f.send(1000, receiver, "")
	require.True(t, f.lastAckSuccess)

	timeout := clienttypes.NewHeight(0, uint64(f.b.GetContext().BlockHeight())+1)
	packet := f.sendBack(500, timeout)
	require.Equal(t, int64(500), f.supplyA(f.voucher), "burned while in flight")

	f.coord.CommitNBlocks(f.b, 3)
	require.NoError(t, f.path.EndpointA.UpdateClient())
	require.NoError(t, f.path.EndpointA.TimeoutPacket(packet))
	require.Equal(t, int64(1000), f.supplyA(f.voucher), "the refund minted it again")

	f.send(1, receiver, "")
	require.False(t, f.lastAckSuccess, "still at the cap")
}

// The cap runs before Ligase: an escrow funded by a transfer over the
// cap is refused as a whole.
func TestInflowCap_LigaseEscrowOverCapRefused(t *testing.T) {
	f := newInflowCapFixture(t)
	f.capVoucher(1000)
	f.send(600, f.payee, "")
	require.True(t, f.lastAckSuccess)

	before := f.balanceB("uaeth")
	f.send(500, ligase.Address.String(), escrowMemo(f.payee))
	require.False(t, f.lastAckSuccess)
	require.Equal(t, before, f.balanceB("uaeth"), "refunded on B")
	require.True(t, f.balanceA(f.mailbox, f.voucher).IsZero())
	require.Equal(t, int64(600), f.supplyA(f.voucher))

	f.send(400, ligase.Address.String(), escrowMemo(f.payee))
	require.Equal(t, "escrow", f.result().Action, "within the cap it's an escrow as before")
}

// Aether's own tokens coming home are never limited, even with nothing
// listed on Aether.
func TestInflowCap_ComingHomeIsNotLimited(t *testing.T) {
	f := newInflowCapFixture(t)
	require.Empty(t, f.appA.InflowCapKeeper.GetParams(f.a.GetContext()).Entries)
	// B lists A's uaeth, so it can leave Aether.
	setCaps(t, f.b, inflowcap.Entry{ChannelId: f.path.EndpointB.ChannelID, Denom: "uaeth", Cap: sdkmath.NewInt(1_000_000)})

	aSender := f.a.SenderAccount.GetAddress()
	start := f.balanceA(aSender, "uaeth")
	f.a.SendMsgsOverride = nil
	out := ibctransfertypes.NewMsgTransfer("transfer", f.path.EndpointA.ChannelID, sdk.NewInt64Coin("uaeth", 5000),
		aSender.String(), f.remote, clienttypes.NewHeight(0, 1_000_000), 0, "")
	res, err := f.a.SendMsgs(out)
	require.NoError(t, err)
	packet, err := ibctesting.ParsePacketFromEvents(res.Events)
	require.NoError(t, err)
	require.NoError(t, f.path.RelayPacket(packet))

	onB := ibctransfertypes.ParseDenomTrace(ibctransfertypes.GetPrefixedDenom("transfer", f.path.EndpointB.ChannelID, "uaeth")).IBCDenom()
	require.Equal(t, int64(5000), f.balanceB(onB).Int64())

	back := ibctransfertypes.NewMsgTransfer("transfer", f.path.EndpointB.ChannelID, sdk.NewInt64Coin(onB, 5000),
		f.remote, aSender.String(), clienttypes.NewHeight(0, 1_000_000), 0, "")
	res, err = f.b.SendMsgs(back)
	require.NoError(t, err)
	packet, err = ibctesting.ParsePacketFromEvents(res.Events)
	require.NoError(t, err)
	require.NoError(t, f.path.RelayPacket(packet))
	require.Equal(t, start, f.balanceA(aSender, "uaeth"), "home again")
}

// Only the governance module account can change the list, and only to a
// valid one.
func TestInflowCap_UpdateParams(t *testing.T) {
	f := newInflowCapFixture(t)
	router := f.appA.MsgServiceRouter()
	good := inflowcap.Params{Entries: []inflowcap.Entry{{ChannelId: "channel-0", Denom: "uaeth", Cap: sdkmath.NewInt(1)}}}

	msg := &inflowcap.MsgUpdateParams{Authority: f.payee, Params: good}
	_, err := router.Handler(msg)(f.a.GetContext(), msg)
	require.ErrorIs(t, err, inflowcap.ErrInvalidAuthority)

	gov := authtypes.NewModuleAddress(governance.ModuleName).String()
	msg = &inflowcap.MsgUpdateParams{Authority: gov, Params: inflowcap.Params{Entries: append(good.Entries, good.Entries...)}}
	_, err = router.Handler(msg)(f.a.GetContext(), msg)
	require.ErrorIs(t, err, inflowcap.ErrInvalidParams)

	msg = &inflowcap.MsgUpdateParams{Authority: gov, Params: good}
	_, err = router.Handler(msg)(f.a.GetContext(), msg)
	require.NoError(t, err)
	require.Equal(t, good.Entries, f.appA.InflowCapKeeper.GetParams(f.a.GetContext()).Entries)

	msg = &inflowcap.MsgUpdateParams{Authority: gov}
	_, err = router.Handler(msg)(f.a.GetContext(), msg)
	require.NoError(t, err, "an empty list closes every path")
	require.Empty(t, f.appA.InflowCapKeeper.GetParams(f.a.GetContext()).Entries)
}

// The restarts a live node goes through across the activation. On the
// testnet, where no genesis set a list, the activation block sets
// testnetInflowEntries.
func TestInflowCap_Activation(t *testing.T) {
	const activation = 3
	defer func(orig int64) { inflowCapActivationHeight = orig }(inflowCapActivationHeight)
	inflowCapActivationHeight = activation
	const msgURL = "/aether.inflowcap.v1.MsgUpdateParams"
	db := dbm.NewMemDB()

	before := newTestApp(t, db)
	require.False(t, before.inflowCapWired)
	_, err := before.interfaceRegistry.Resolve(msgURL)
	require.Error(t, err, "a pre-activation node doesn't know the message")
	initTestChain(t, before)
	finalizeAndCommit(t, before, 1)
	finalizeAndCommit(t, before, 2)
	for _, name := range inflowCapStoreKeys {
		require.False(t, committedStoreNames(t, before, 2)[name], "no store before activation")
	}
	require.ErrorContains(t, finalizeBlock(before, activation), "restart this node")

	atActivation := newTestApp(t, db)
	require.True(t, atActivation.inflowCapWired)
	_, err = atActivation.interfaceRegistry.Resolve(msgURL)
	require.NoError(t, err)
	finalizeAndCommit(t, atActivation, activation)
	for _, name := range inflowCapStoreKeys {
		require.True(t, committedStoreNames(t, atActivation, activation)[name], "store in the AppHash from activation on")
	}

	after := newTestApp(t, db)
	require.True(t, after.inflowCapWired)
	finalizeAndCommit(t, after, activation+1)
	ctx := after.NewUncachedContext(false, cmtproto.Header{Height: activation + 2, Time: testGenesisTime.Add((activation + 2) * time.Minute)})
	require.Equal(t, testnetInflowEntries(), after.InflowCapKeeper.GetParams(ctx).Entries)
	require.True(t, inflowCapHost{after}.Active(ctx))

	usdc := after.InflowCapKeeper.Caps(ctx)[0]
	require.Equal(t, "ibc/064D82A67318DD30F54A4E17B6E487C2864E8B075A5871D3ACAD7A7129F32C5C", usdc.VoucherDenom, "wallet.TestnetUSDC")
	osmo := after.InflowCapKeeper.Caps(ctx)[1]
	require.Equal(t, "ibc/47BD209179859CDE4A2806763D7189B6E6FE13A17880FE2B42DE1E6C1E329E23", osmo.VoucherDenom, "docs/OSMOSIS-POOL.md")
}

// How a token gets listed on aether-1 (docs/IBC-INFLOW-CAP.md, "Listing
// on aether-1"): genesis lists nothing, the path is opened, and a
// governance param-change proposal lists the channel it opened. Here on a
// fresh chain ID, so every gate is open from block 1 as on aether-1, with
// the real x/governance proposal, vote and execution.
func TestInflowCap_ListedByGovernance(t *testing.T) {
	f := newLigaseFixtureOn(t, 0, gates.RehearsalPrefix+"test-a")
	require.True(t, f.appA.inflowCapWired)
	receiver := f.a.SenderAccount.GetAddress().String()

	f.send(10, receiver, "")
	require.False(t, f.lastAckSuccess, "genesis lists nothing: refused")

	// One validator we hold the key of, active long enough to have
	// tenure, and a short voting period for the test.
	voter := f.a.SenderAccount.GetAddress()
	ctx := f.a.GetContext()
	gov := f.appA.GovernanceKeeper
	f.appA.PowKeeper.SetActiveValidator(ctx, voter)
	gov.SetVotingPeriod(ctx, 60)
	f.coord.IncrementTimeBy(time.Hour)
	f.coord.CommitBlock(f.a)

	update := &inflowcap.MsgUpdateParams{
		Authority: authtypes.NewModuleAddress(governance.ModuleName).String(),
		Params:    inflowcap.Params{Entries: []inflowcap.Entry{{ChannelId: f.path.EndpointA.ChannelID, Denom: "uaeth", Cap: sdkmath.NewInt(1000)}}},
	}
	srv := governance.NewMsgServerImpl(gov)

	// A bad list is refused when it's proposed.
	bad := &inflowcap.MsgUpdateParams{Authority: update.Authority, Params: inflowcap.Params{Entries: append(update.Params.Entries, update.Params.Entries...)}}
	badAny, err := codectypes.NewAnyWithValue(bad)
	require.NoError(t, err)
	_, err = srv.SubmitParamChangeProposal(f.a.GetContext(), &governance.MsgSubmitParamChangeProposal{Proposer: voter.String(), Deposit: "25000000", ExecuteMsg: badAny})
	require.ErrorContains(t, err, "already listed")

	anyMsg, err := codectypes.NewAnyWithValue(update)
	require.NoError(t, err)
	res, err := srv.SubmitParamChangeProposal(f.a.GetContext(), &governance.MsgSubmitParamChangeProposal{Proposer: voter.String(), Deposit: "25000000", ExecuteMsg: anyMsg})
	require.NoError(t, err)
	// Quorum is 60% of the active validators: the genesis one votes too
	// (a msg server call needs no key).
	active := f.appA.PowKeeper.IterateActiveValidators(f.a.GetContext())
	require.Len(t, active, 2)
	for _, v := range active {
		_, err = srv.Vote(f.a.GetContext(), &governance.MsgVote{ProposalId: res.ProposalId, Voter: v.String(), Option: governance.VoteOption_VOTE_OPTION_YES})
		require.NoError(t, err)
	}

	f.coord.IncrementTimeBy(2 * time.Minute)
	f.coord.CommitBlock(f.a) // the voting period ends; EndBlock resolves it
	p, ok := gov.GetProposal(f.a.GetContext(), res.ProposalId)
	require.True(t, ok)
	require.Equal(t, governance.ProposalStatus_PROPOSAL_STATUS_PASSED, p.Status)
	require.Equal(t, update.Params.Entries, f.appA.InflowCapKeeper.GetParams(f.a.GetContext()).Entries)

	f.send(1000, receiver, "")
	require.True(t, f.lastAckSuccess, "listed by governance: in, up to the cap")
	f.send(1, receiver, "")
	require.False(t, f.lastAckSuccess, "over the cap")
}
