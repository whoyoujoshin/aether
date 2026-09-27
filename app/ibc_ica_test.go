package app

import (
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/cosmos/gogoproto/proto"
	icacontrollertypes "github.com/cosmos/ibc-go/v8/modules/apps/27-interchain-accounts/controller/types"
	icatypes "github.com/cosmos/ibc-go/v8/modules/apps/27-interchain-accounts/types"
	channeltypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	ibctesting "github.com/cosmos/ibc-go/v8/testing"
	"github.com/stretchr/testify/require"
)

// icaTestVersion mirrors ibc-go's own controller test fixture (see
// controller_test.TestVersion): the JSON-encoded Metadata a real
// relayer negotiates during channel handshake.
func icaTestVersion(connectionID string) string {
	return string(icatypes.ModuleCdc.MustMarshalJSON(&icatypes.Metadata{
		Version:                icatypes.Version,
		ControllerConnectionId: connectionID,
		HostConnectionId:       connectionID,
		Encoding:               icatypes.EncodingProtobuf,
		TxType:                 icatypes.TxTypeSDKMultiMsg,
	}))
}

// TestICARegisterAndExecute proves milestone 4's wiring is real: chain
// A registers an interchain account on chain B with NO custom
// authentication module (icacontroller.NewIBCMiddleware(nil, ...) --
// see app.go), completes the channel handshake, funds the resulting
// interchain account directly (interchain accounts start empty), then
// drives a real MsgSend FROM that account by relaying a MsgSendTx
// packet -- exactly the flow an agent with no bespoke auth-module
// integration would use to control an account on another chain.
func TestICARegisterAndExecute(t *testing.T) {
	coord := &ibctesting.Coordinator{T: t, CurrentTime: testGenesisTime}
	chainA := newIBCTestChain(t, coord, "aether-ica-a")
	chainB := newIBCTestChain(t, coord, "aether-ica-b")
	coord.Chains = map[string]*ibctesting.TestChain{chainA.ChainID: chainA, chainB.ChainID: chainB}

	path := ibctesting.NewPath(chainA, chainB)
	coord.SetupClients(path)
	coord.SetupConnections(path)

	owner := chainA.SenderAccount.GetAddress().String()
	version := icaTestVersion(path.EndpointA.ConnectionID)
	path.EndpointA.ChannelConfig.Order = channeltypes.ORDERED
	path.EndpointB.ChannelConfig.Order = channeltypes.ORDERED
	path.EndpointA.ChannelConfig.Version = version
	path.EndpointB.ChannelConfig.Version = version

	appA := chainA.App.(*App)
	channelSeq := appA.IBCKeeper.ChannelKeeper.GetNextChannelSequence(chainA.GetContext())
	require.NoError(t, appA.ICAControllerKeeper.RegisterInterchainAccountWithOrdering(
		chainA.GetContext(), path.EndpointA.ConnectionID, owner, version, channeltypes.ORDERED,
	))
	chainA.NextBlock()

	portID, err := icatypes.NewControllerPortID(owner)
	require.NoError(t, err)
	path.EndpointA.ChannelConfig.PortID = portID
	path.EndpointA.ChannelID = channeltypes.FormatChannelIdentifier(channelSeq)
	path.EndpointB.ChannelConfig.PortID = icatypes.HostPortID

	require.NoError(t, path.EndpointB.ChanOpenTry())
	require.NoError(t, path.EndpointA.ChanOpenAck())
	require.NoError(t, path.EndpointB.ChanOpenConfirm())

	appB := chainB.App.(*App)
	icaAddr, found := appB.ICAHostKeeper.GetInterchainAccountAddress(chainB.GetContext(), path.EndpointB.ConnectionID, portID)
	require.True(t, found, "host must have created the interchain account on channel confirm")

	// Interchain accounts start empty: fund it directly on chain B so it
	// has something to send, exactly like funding any other account.
	icaSdkAddr, err := sdk.AccAddressFromBech32(icaAddr)
	require.NoError(t, err)
	require.NoError(t, appB.BankKeeper.SendCoins(chainB.GetContext(), chainB.SenderAccount.GetAddress(), icaSdkAddr,
		sdk.NewCoins(sdk.NewCoin("uaeth", sdkmath.NewInt(500)))))
	chainB.NextBlock()

	// Now drive a real MsgSend FROM the interchain account, via a
	// MsgSendTx submitted through the normal, ML-DSA-signed tx pipeline
	// on chain A -- this is what actually exercises the nil-app
	// controller middleware's packet path end to end.
	sendMsg := banktypes.NewMsgSend(icaSdkAddr, chainB.SenderAccount.GetAddress(), sdk.NewCoins(sdk.NewCoin("uaeth", sdkmath.NewInt(200))))
	packetData, err := icatypes.SerializeCosmosTx(chainA.Codec, []proto.Message{sendMsg}, icatypes.EncodingProtobuf)
	require.NoError(t, err)
	icaPacketData := icatypes.InterchainAccountPacketData{Type: icatypes.EXECUTE_TX, Data: packetData}

	sendTxMsg := icacontrollertypes.NewMsgSendTx(owner, path.EndpointA.ConnectionID, uint64(chainA.CurrentHeader.Time.Add(time.Hour).UnixNano()), icaPacketData)
	res, err := chainA.SendMsgs(sendTxMsg)
	require.NoError(t, err)

	packet, err := ibctesting.ParsePacketFromEvents(res.Events)
	require.NoError(t, err)
	require.NoError(t, path.RelayPacket(packet))

	finalBalance := appB.BankKeeper.GetBalance(chainB.GetContext(), icaSdkAddr, "uaeth")
	require.Equal(t, sdkmath.NewInt(300), finalBalance.Amount, "the interchain account must have sent exactly 200 of its 500 uaeth")
}
