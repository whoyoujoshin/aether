package app

import (
	"fmt"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cosmos/cosmos-sdk/baseapp"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/mempool"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	ibctransfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	channeltypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	ibctesting "github.com/cosmos/ibc-go/v8/testing"
	"github.com/stretchr/testify/require"
)

type fixedHelicaseSource [][]byte

func (s fixedHelicaseSource) RelayTxs(int64) [][]byte { return s }

// commitHelicaseBlock is ibctesting's unexported TestChain.commitBlock:
// commit, then advance the chain's headers and validator sets.
func commitHelicaseBlock(t *testing.T, chain *ibctesting.TestChain, res *abci.ResponseFinalizeBlock) {
	t.Helper()
	_, err := chain.App.Commit()
	require.NoError(t, err)
	chain.LastHeader = chain.CurrentTMClientHeader()
	chain.Vals = chain.NextVals
	chain.NextVals = ibctesting.ApplyValSetChanges(chain, chain.Vals, res.ValidatorUpdates)
	chain.Vals.IncrementProposerPriority(1)
	chain.CurrentHeader = cmtproto.Header{
		ChainID:            chain.ChainID,
		Height:             chain.App.LastBlockHeight() + 1,
		AppHash:            chain.App.LastCommitID().Hash,
		Time:               chain.CurrentHeader.Time,
		ValidatorsHash:     chain.Vals.Hash(),
		NextValidatorsHash: chain.NextVals.Hash(),
		ProposerAddress:    chain.Vals.Proposer.Address,
	}
}

// proposeBlock runs one block through the proposer's path: source's
// relay transactions into PrepareProposal (plus mempoolTxs), the result
// through ProcessProposal, then FinalizeBlock and Commit.
func proposeBlock(t *testing.T, chain *ibctesting.TestChain, source HelicaseSource, mempoolTxs ...[]byte) (*abci.ResponsePrepareProposal, *abci.ResponseFinalizeBlock) {
	t.Helper()
	app := chain.App.(*App)
	chain.Coordinator.UpdateTimeForChain(chain)
	app.SetHelicaseSource(source)
	defer app.SetHelicaseSource(nil)

	height := app.LastBlockHeight() + 1
	prep, err := app.PrepareProposal(&abci.RequestPrepareProposal{
		Height:             height,
		Time:               chain.CurrentHeader.Time,
		MaxTxBytes:         1 << 20,
		Txs:                mempoolTxs,
		NextValidatorsHash: chain.NextVals.Hash(),
		ProposerAddress:    chain.CurrentHeader.ProposerAddress,
	})
	require.NoError(t, err)

	proc, err := app.ProcessProposal(&abci.RequestProcessProposal{
		Height:             height,
		Time:               chain.CurrentHeader.Time,
		Txs:                prep.Txs,
		NextValidatorsHash: chain.NextVals.Hash(),
		ProposerAddress:    chain.CurrentHeader.ProposerAddress,
	})
	require.NoError(t, err)
	require.Equal(t, abci.ResponseProcessProposal_ACCEPT, proc.Status, "validators must accept the proposer's relay transactions")

	res, err := app.FinalizeBlock(&abci.RequestFinalizeBlock{
		Height:             height,
		Time:               chain.CurrentHeader.Time,
		Txs:                prep.Txs,
		NextValidatorsHash: chain.NextVals.Hash(),
		ProposerAddress:    chain.CurrentHeader.ProposerAddress,
	})
	require.NoError(t, err)
	commitHelicaseBlock(t, chain, res)
	chain.Coordinator.IncrementTime()
	return prep, res
}

// relayViaHelicase replaces chain's SendMsgs: relay messages go into the
// next block as one unsigned relay transaction the proposer includes,
// and anything else is signed and sent as usual. relayed counts the
// relay transactions delivered.
func relayViaHelicase(t *testing.T, chain *ibctesting.TestChain, relayed *int, last *[]byte) func(msgs ...sdk.Msg) (*abci.ExecTxResult, error) {
	var send func(msgs ...sdk.Msg) (*abci.ExecTxResult, error)
	send = func(msgs ...sdk.Msg) (*abci.ExecTxResult, error) {
		for _, msg := range msgs {
			if _, ok := isRelayMsg(msg); !ok {
				chain.SendMsgsOverride = nil
				defer func() { chain.SendMsgsOverride = send }()
				return chain.SendMsgs(msgs...)
			}
		}
		app := chain.App.(*App)
		bz, err := EncodeHelicaseTx(app.GetTxConfig(), msgs...)
		require.NoError(t, err)

		prep, res := proposeBlock(t, chain, fixedHelicaseSource{bz})
		require.Equal(t, [][]byte{bz}, prep.Txs, "the proposer must put the relay transaction in its block")
		require.Len(t, res.TxResults, 1)
		result := res.TxResults[0]
		if result.Code != 0 {
			return result, fmt.Errorf("%s/%d: %q", result.Codespace, result.Code, result.Log)
		}
		*relayed++
		*last = bz

		// Attributed to the Helicase address, not to any account: the
		// first message event names the message's signer.
		sender := ""
		for _, ev := range result.Events {
			if ev.Type != sdk.EventTypeMessage {
				continue
			}
			for _, a := range ev.Attributes {
				if a.Key == sdk.AttributeKeySender {
					sender = a.Value
				}
			}
			break
		}
		require.Equal(t, HelicaseAddress.String(), sender)
		return result, nil
	}
	return send
}

func activateHelicaseAt(t *testing.T, height int64) {
	orig := helicaseActivationHeight
	helicaseActivationHeight = height
	t.Cleanup(func() { helicaseActivationHeight = orig })
}

// TestHelicaseRelaysWithoutSignatures drives real ICS-20 traffic between
// Aether (chain A) and another chain, where every message relayed onto
// Aether -- client updates, a packet, an acknowledgement and a timeout --
// arrives in an unsigned relay transaction the proposer included, and
// only the other chain's side is relayed with signed transactions.
func TestHelicaseRelaysWithoutSignatures(t *testing.T) {
	// Operators choose the app-side mempool (app.toml mempool.max-txs):
	// none by default, or the SDK's sender-nonce mempool. A relay
	// transaction must execute the same under either, or nodes would
	// disagree on the app hash.
	t.Run("no app mempool", func(t *testing.T) { testHelicaseRelays(t) })
	t.Run("sender-nonce mempool", func(t *testing.T) {
		testHelicaseRelays(t, baseapp.SetMempool(mempool.NewSenderNonceMempool(mempool.SenderNonceMaxTxOpt(5000))))
	})
}

func testHelicaseRelays(t *testing.T, opts ...func(*baseapp.BaseApp)) {
	activateHelicaseAt(t, 0)

	coord := &ibctesting.Coordinator{T: t, CurrentTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	chainA := newIBCTestChainWithOptions(t, coord, "aether-test-a", opts...)
	chainB := newIBCTestChain(t, coord, "aether-test-b")
	coord.Chains = map[string]*ibctesting.TestChain{chainA.ChainID: chainA, chainB.ChainID: chainB}

	// The handshake is signed: it happens once, by our own relayer.
	path := ibctesting.NewTransferPath(chainA, chainB)
	coord.Setup(path)

	relayed := 0
	var last []byte
	chainA.SendMsgsOverride = relayViaHelicase(t, chainA, &relayed, &last)
	appA := chainA.App.(*App)
	appB := chainB.App.(*App)
	amount := sdkmath.NewInt(250)
	farTimeout := clienttypes.NewHeight(0, 1_000_000)

	// In: B sends to A. Helicase updates A's client of B and delivers the
	// packet; B gets A's acknowledgement by an ordinary signed relay.
	msg := ibctransfertypes.NewMsgTransfer(path.EndpointB.ChannelConfig.PortID, path.EndpointB.ChannelID,
		sdk.NewCoin("uaeth", amount), chainB.SenderAccount.GetAddress().String(), chainA.SenderAccount.GetAddress().String(), farTimeout, 0, "")
	res, err := chainB.SendMsgs(msg)
	require.NoError(t, err)
	packet, err := ibctesting.ParsePacketFromEvents(res.Events)
	require.NoError(t, err)
	require.NoError(t, path.RelayPacket(packet))

	voucher := ibctransfertypes.ParseDenomTrace(ibctransfertypes.GetPrefixedDenom(path.EndpointA.ChannelConfig.PortID, path.EndpointA.ChannelID, "uaeth")).IBCDenom()
	require.Equal(t, amount, appA.BankKeeper.GetBalance(chainA.GetContext(), chainA.SenderAccount.GetAddress(), voucher).Amount,
		"the packet Helicase delivered must mint the voucher on Aether")
	require.False(t, appB.IBCKeeper.ChannelKeeper.HasPacketCommitment(chainB.GetContext(), packet.SourcePort, packet.SourceChannel, packet.Sequence),
		"B must have cleared its commitment once Aether's acknowledgement reached it")

	// A proposer holding a stale batch proposes the same packet again: a
	// no-op, not a failure and not a second voucher.
	_, dup := proposeBlock(t, chainA, fixedHelicaseSource{last})
	require.Len(t, dup.TxResults, 1)
	require.Zero(t, dup.TxResults[0].Code, dup.TxResults[0].Log)
	require.Equal(t, amount, appA.BankKeeper.GetBalance(chainA.GetContext(), chainA.SenderAccount.GetAddress(), voucher).Amount,
		"a packet relayed twice must be received once")

	// Out: A sends to B (a signed transfer, as any user's is). B receives
	// it by a signed relay; Helicase brings B's acknowledgement back.
	msg = ibctransfertypes.NewMsgTransfer(path.EndpointA.ChannelConfig.PortID, path.EndpointA.ChannelID,
		sdk.NewCoin("uaeth", amount), chainA.SenderAccount.GetAddress().String(), chainB.SenderAccount.GetAddress().String(), farTimeout, 0, "")
	res, err = chainA.SendMsgs(msg)
	require.NoError(t, err)
	packet, err = ibctesting.ParsePacketFromEvents(res.Events)
	require.NoError(t, err)
	require.NoError(t, path.RelayPacket(packet))
	require.False(t, appA.IBCKeeper.ChannelKeeper.HasPacketCommitment(chainA.GetContext(), packet.SourcePort, packet.SourceChannel, packet.Sequence),
		"the acknowledgement Helicase delivered must clear Aether's commitment")

	// Timeout: A sends with a timeout B passes before anyone relays it.
	// Helicase proves B never received it, and A refunds the sender.
	before := appA.BankKeeper.GetBalance(chainA.GetContext(), chainA.SenderAccount.GetAddress(), "uaeth").Amount
	soon := clienttypes.NewHeight(0, uint64(chainB.CurrentHeader.Height)+2)
	msg = ibctransfertypes.NewMsgTransfer(path.EndpointA.ChannelConfig.PortID, path.EndpointA.ChannelID,
		sdk.NewCoin("uaeth", amount), chainA.SenderAccount.GetAddress().String(), chainB.SenderAccount.GetAddress().String(), soon, 0, "")
	res, err = chainA.SendMsgs(msg)
	require.NoError(t, err)
	packet, err = ibctesting.ParsePacketFromEvents(res.Events)
	require.NoError(t, err)
	require.Equal(t, before.Sub(amount), appA.BankKeeper.GetBalance(chainA.GetContext(), chainA.SenderAccount.GetAddress(), "uaeth").Amount)
	for i := 0; i < 3; i++ {
		chainB.NextBlock()
	}
	require.NoError(t, path.EndpointA.UpdateClient())
	require.NoError(t, path.EndpointA.TimeoutPacket(packet))
	require.Equal(t, before, appA.BankKeeper.GetBalance(chainA.GetContext(), chainA.SenderAccount.GetAddress(), "uaeth").Amount,
		"the timeout Helicase delivered must refund the sender")

	// 2 client updates + packet + ack, 1 update + ack... every Aether-side
	// relay step, at least one per kind.
	require.GreaterOrEqual(t, relayed, 6)
}

func helicaseTestChain(t *testing.T) *ibctesting.TestChain {
	coord := &ibctesting.Coordinator{T: t, CurrentTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	chain := newIBCTestChain(t, coord, "aether-test-a")
	coord.Chains = map[string]*ibctesting.TestChain{chain.ChainID: chain}
	return chain
}

// wellFormedRecv passes ValidateBasic, which runs before the ante
// handler; its proof is never checked in these tests.
func wellFormedRecv() sdk.Msg {
	packet := channeltypes.NewPacket([]byte("data"), 1, "transfer", "channel-0", "transfer", "channel-0", clienttypes.ZeroHeight(), 1)
	return channeltypes.NewMsgRecvPacket(packet, []byte("proof"), clienttypes.NewHeight(0, 1), HelicaseAddress.String())
}

func encodeRelay(t *testing.T, app *App, msgs ...sdk.Msg) []byte {
	bz, err := EncodeHelicaseTx(app.GetTxConfig(), msgs...)
	require.NoError(t, err)
	return bz
}

// TestHelicaseRefusedOutsideBlocks: a relay transaction can't be
// submitted, only proposed. CheckTx and simulation refuse it, so no
// mempool ever holds one.
func TestHelicaseRefusedOutsideBlocks(t *testing.T) {
	activateHelicaseAt(t, 0)
	chain := helicaseTestChain(t)
	app := chain.App.(*App)
	bz := encodeRelay(t, app, wellFormedRecv())

	res, err := app.CheckTx(&abci.RequestCheckTx{Tx: bz, Type: abci.CheckTxType_New})
	require.NoError(t, err)
	require.Equal(t, ErrHelicaseNotFromProposer.ABCICode(), res.Code, res.Log)
	require.Equal(t, "app", res.Codespace)

	res, err = app.CheckTx(&abci.RequestCheckTx{Tx: bz, Type: abci.CheckTxType_Recheck})
	require.NoError(t, err)
	require.Equal(t, ErrHelicaseNotFromProposer.ABCICode(), res.Code, res.Log)

	_, _, err = app.Simulate(bz)
	require.ErrorIs(t, err, ErrHelicaseNotFromProposer)
}

// TestHelicaseInactiveBeforeActivation: below the activation height a
// relay transaction is what it always was -- an unsigned transaction the
// ante handler refuses -- and a proposer includes none.
func TestHelicaseInactiveBeforeActivation(t *testing.T) {
	activateHelicaseAt(t, 1_000_000)
	chain := helicaseTestChain(t)
	app := chain.App.(*App)
	bz := encodeRelay(t, app, wellFormedRecv())

	prep, _ := proposeBlock(t, chain, fixedHelicaseSource{bz})
	require.Empty(t, prep.Txs, "no relay transactions before activation")

	// Forced in anyway, it fails like any unsigned transaction.
	res, err := app.FinalizeBlock(&abci.RequestFinalizeBlock{
		Height: app.LastBlockHeight() + 1, Time: chain.CurrentHeader.Time,
		NextValidatorsHash: chain.NextVals.Hash(), Txs: [][]byte{bz},
	})
	require.NoError(t, err)
	require.NotZero(t, res.TxResults[0].Code)
	require.Equal(t, "sdk", res.TxResults[0].Codespace, res.TxResults[0].Log)
	commitHelicaseBlock(t, chain, res)
}

// TestHelicaseProposalChecks: validators reject a proposal whose relay
// transactions are malformed or too many, and a proposer drops invalid
// ones from its own proposal instead of proposing them.
func TestHelicaseProposalChecks(t *testing.T) {
	activateHelicaseAt(t, 0)
	chain := helicaseTestChain(t)
	app := chain.App.(*App)
	cfg := app.GetTxConfig()
	height := app.LastBlockHeight() + 1

	process := func(txs ...[]byte) abci.ResponseProcessProposal_ProposalStatus {
		t.Helper()
		res, err := app.ProcessProposal(&abci.RequestProcessProposal{
			Height: height, Time: chain.CurrentHeader.Time, Txs: txs,
			NextValidatorsHash: chain.NextVals.Hash(), ProposerAddress: chain.CurrentHeader.ProposerAddress,
		})
		require.NoError(t, err)
		return res.Status
	}
	build := func(memo string, gas uint64, msgs ...sdk.Msg) []byte {
		t.Helper()
		b := cfg.NewTxBuilder()
		require.NoError(t, b.SetMsgs(msgs...))
		b.SetMemo(memo)
		b.SetGasLimit(gas)
		bz, err := cfg.TxEncoder()(b.GetTx())
		require.NoError(t, err)
		return bz
	}
	helicase := HelicaseAddress.String()
	someone := chain.SenderAccount.GetAddress().String()
	update := func(signer string) sdk.Msg {
		return &clienttypes.MsgUpdateClient{ClientId: "07-tendermint-0", Signer: signer}
	}

	valid := build(HelicaseMemo, HelicaseTxGas, update(helicase))
	require.Equal(t, abci.ResponseProcessProposal_ACCEPT, process(valid))

	for name, bad := range map[string][]byte{
		"another signer":   build(HelicaseMemo, HelicaseTxGas, update(someone)),
		"not a relay msg":  build(HelicaseMemo, HelicaseTxGas, update(helicase), &banktypes.MsgSend{FromAddress: helicase, ToAddress: someone, Amount: sdk.NewCoins(sdk.NewInt64Coin("uaeth", 1))}),
		"no memo":          build("", HelicaseTxGas, update(helicase)),
		"too much gas":     build(HelicaseMemo, helicaseMaxGas+1, update(helicase)),
		"a timeout packet": build(HelicaseMemo, HelicaseTxGas, &channeltypes.MsgTimeout{Signer: someone}),
	} {
		require.Equal(t, abci.ResponseProcessProposal_REJECT, process(valid, bad), name)
	}

	many := make([][]byte, helicaseMaxTxsPerBlock+1)
	for i := range many {
		many[i] = valid
	}
	require.Equal(t, abci.ResponseProcessProposal_ACCEPT, process(many[:helicaseMaxTxsPerBlock]...))
	require.Equal(t, abci.ResponseProcessProposal_REJECT, process(many...))

	// The proposer keeps the valid one and drops the rest.
	bad := build(HelicaseMemo, HelicaseTxGas, update(someone))
	prep, _ := proposeBlock(t, chain, fixedHelicaseSource{bad, valid, []byte("not a transaction")})
	require.Equal(t, [][]byte{valid}, prep.Txs)
}

// TestHelicaseRespectsBlockGasLimit: with a block gas limit, relay
// transactions take at most half of it, and the rest of the block is
// filled within what's left of the gas and bytes.
func TestHelicaseRespectsBlockGasLimit(t *testing.T) {
	activateHelicaseAt(t, 0)
	chain := helicaseTestChain(t)
	app := chain.App.(*App)

	a := encodeRelay(t, app, wellFormedRecv())
	b := encodeRelay(t, app, wellFormedRecv())
	app.SetHelicaseSource(fixedHelicaseSource{a, b})
	defer app.SetHelicaseSource(nil)

	maxGas := int64(3 * HelicaseTxGas) // half of it fits one relay transaction
	ctx := chain.GetContext().WithConsensusParams(cmtproto.ConsensusParams{Block: &cmtproto.BlockParams{MaxBytes: 1 << 20, MaxGas: maxGas}})
	var nextGas, nextBytes int64
	next := func(ctx sdk.Context, req *abci.RequestPrepareProposal) (*abci.ResponsePrepareProposal, error) {
		nextGas, nextBytes = ctx.ConsensusParams().Block.MaxGas, req.MaxTxBytes
		return &abci.ResponsePrepareProposal{Txs: [][]byte{[]byte("mempool tx")}}, nil
	}
	resp, err := app.helicasePrepareProposal(next)(ctx, &abci.RequestPrepareProposal{Height: 10, MaxTxBytes: 1 << 20})
	require.NoError(t, err)
	require.Equal(t, [][]byte{a, []byte("mempool tx")}, resp.Txs)
	require.Equal(t, maxGas-int64(HelicaseTxGas), nextGas, "the rest of the block gets the gas relay transactions left")
	require.Equal(t, int64(1<<20-len(a)), nextBytes)
	require.Equal(t, maxGas, ctx.ConsensusParams().Block.MaxGas, "the caller's params are untouched")
}
