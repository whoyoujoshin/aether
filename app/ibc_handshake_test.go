package app

import (
	"encoding/json"
	"testing"
	"time"

	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	cometencoding "github.com/cometbft/cometbft/crypto/encoding"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	ibctransfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	ibctesting "github.com/cosmos/ibc-go/v8/testing"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/x/pow"
)

// This file proves the wiring from milestones 1-2 (capability, core IBC,
// ICS-20 transfer) is not just "compiles and genesis doesn't panic" but
// actually completes a real client/connection/channel handshake and
// relays a real token-transfer packet, using ibc-go's own ibctesting
// package -- the same harness ibc-go's own core/transfer modules are
// tested with.
//
// ibctesting.NewTestChain / SetupWithGenesisValSet can't be used as-is:
// they hardcode an x/staking-shaped genesis (delegations, a bonded
// pool, secp256k1 sender keys) this chain doesn't have at all. Instead,
// newIBCTestChain below builds ibctesting.TestChain's exported fields
// directly, driven by a real *App bootstrapped exactly the way a live
// node's genesis.json is (one CometBFT validator via
// InitChainer -> PowKeeper.BootstrapValidator, real ML-DSA sender keys
// since PostQuantumDecorator rejects anything else). Once TestChain is
// built this way, all of ibctesting's higher-level orchestration
// (Coordinator.Setup, CreateTransferChannels, SendPacket, RelayPacket)
// works unmodified, because none of it depends on how the chain's
// genesis was constructed.

func newIBCTestChain(t *testing.T, coord *ibctesting.Coordinator, chainID string) *ibctesting.TestChain {
	t.Helper()
	return newIBCTestChainWithOptions(t, coord, chainID)
}

// newIBCTestChainWithOptions is newIBCTestChain with extra BaseApp
// options, e.g. a mempool.
func newIBCTestChainWithOptions(t *testing.T, coord *ibctesting.Coordinator, chainID string, opts ...func(*baseapp.BaseApp)) *ibctesting.TestChain {
	t.Helper()

	// One real CometBFT validator at exactly x/pow's flat voting power,
	// so app.GetStakingKeeper()'s test view (see testing_support.go),
	// which hardcodes that same constant, matches what this validator
	// set actually carries -- no BootstrapPowerCorrection dance needed
	// (this chain never runs long enough to reach that height, nor an
	// epoch boundary: EpochLength defaults to 1440 blocks).
	cmtVal, privVal := cmttypes.RandValidator(false, pow.ValidatorVotingPower)
	valSet := cmttypes.NewValidatorSet([]*cmttypes.Validator{cmtVal})
	signers := map[string]cmttypes.PrivValidator{cmtVal.Address.String(): privVal}

	protoPubKey, err := cometencoding.PubKeyToProto(cmtVal.PubKey)
	require.NoError(t, err)

	sender, err := mldsa.GenPrivKey()
	require.NoError(t, err)
	senderAddr := sdk.AccAddress(sender.PubKey().Address())
	senderAcc := authtypes.NewBaseAccount(senderAddr, sender.PubKey(), 0, 0)
	balance := sdk.NewCoins(sdk.NewCoin("uaeth", sdkmath.NewInt(1_000_000_000)))

	cdc := MakeEncodingConfig().Codec
	genesisState := ModuleBasics.DefaultGenesis(cdc)
	genesisState[authtypes.ModuleName] = cdc.MustMarshalJSON(authtypes.NewGenesisState(authtypes.DefaultParams(), []authtypes.GenesisAccount{senderAcc}))
	genesisState[banktypes.ModuleName] = cdc.MustMarshalJSON(banktypes.NewGenesisState(
		banktypes.DefaultGenesisState().Params,
		[]banktypes.Balance{{Address: senderAddr.String(), Coins: balance}},
		balance,
		[]banktypes.Metadata{}, []banktypes.SendEnabled{},
	))
	stateBytes, err := json.Marshal(genesisState)
	require.NoError(t, err)

	// IBC live from genesis for this test: see planIBC(0, 0).
	defer func(orig int64) { ibcActivationHeight = orig }(ibcActivationHeight)
	ibcActivationHeight = 0

	app := New(log.NewNopLogger(), dbm.NewMemDB(), nil, true, nil, t.TempDir(), 0, emptyAppOptions{}, append([]func(*baseapp.BaseApp){baseapp.SetChainID(chainID)}, opts...)...).(*App)
	require.True(t, app.ibcWired)

	genesisTime := coord.CurrentTime.UTC()
	_, err = app.InitChain(&abci.RequestInitChain{
		ChainId:         chainID,
		Time:            genesisTime,
		InitialHeight:   1,
		AppStateBytes:   stateBytes,
		Validators:      []abci.ValidatorUpdate{{PubKey: protoPubKey, Power: pow.ValidatorVotingPower}},
		ConsensusParams: simtestutil.DefaultConsensusParams,
	})
	require.NoError(t, err)

	chain := &ibctesting.TestChain{
		TB:             t,
		Coordinator:    coord,
		ChainID:        chainID,
		App:            app,
		CurrentHeader:  cmtproto.Header{ChainID: chainID, Height: 1, Time: genesisTime},
		QueryServer:    nil, // set below once populated post-NextBlock, matching upstream's own ordering
		TxConfig:       app.GetTxConfig(),
		Codec:          app.AppCodec(),
		Vals:           valSet,
		NextVals:       valSet,
		Signers:        signers,
		SenderPrivKey:  sender,
		SenderAccount:  senderAcc,
		SenderAccounts: []ibctesting.SenderAccount{{SenderPrivKey: sender, SenderAccount: senderAcc}},
	}
	chain.QueryServer = app.IBCKeeper

	chain.NextBlock() // commits the genesis block, exactly like NewTestChainWithValSet's own last step
	return chain
}

// TestIBCHandshakeAndTransfer drives two real *App instances through a
// complete client/connection/channel handshake and relays one ICS-20
// transfer packet each way, using ibc-go's own ibctesting.Coordinator
// (Setup, CreateTransferChannels, SendPacket, RelayPacket) -- proving
// milestones 1-2's wiring (capability, core IBC, transfer) is real, not
// just something that happens to compile and pass genesis.
func TestIBCHandshakeAndTransfer(t *testing.T) {
	coord := &ibctesting.Coordinator{T: t, CurrentTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	chainA := newIBCTestChain(t, coord, "aether-test-a")
	chainB := newIBCTestChain(t, coord, "aether-test-b")
	coord.Chains = map[string]*ibctesting.TestChain{chainA.ChainID: chainA, chainB.ChainID: chainB}

	path := ibctesting.NewTransferPath(chainA, chainB)
	coord.Setup(path)
	require.NotEmpty(t, path.EndpointA.ChannelID)
	require.NotEmpty(t, path.EndpointB.ChannelID)

	amount := sdkmath.NewInt(100)
	coin := sdk.NewCoin("uaeth", amount)
	timeoutHeight := clienttypes.NewHeight(0, 1_000_000)

	transferMsg := ibctransfertypes.NewMsgTransfer(
		path.EndpointA.ChannelConfig.PortID, path.EndpointA.ChannelID,
		coin, chainA.SenderAccount.GetAddress().String(), chainB.SenderAccount.GetAddress().String(),
		timeoutHeight, 0, "",
	)
	res, err := chainA.SendMsgs(transferMsg)
	require.NoError(t, err)

	packet, err := ibctesting.ParsePacketFromEvents(res.Events)
	require.NoError(t, err)

	require.NoError(t, path.RelayPacket(packet))

	voucherDenom := ibctransfertypes.ParseDenomTrace(ibctransfertypes.GetPrefixedDenom(path.EndpointB.ChannelConfig.PortID, path.EndpointB.ChannelID, "uaeth")).IBCDenom()
	bAppB := chainB.App.(*App)
	balance := bAppB.BankKeeper.GetBalance(chainB.GetContext(), chainB.SenderAccount.GetAddress(), voucherDenom)
	require.Equal(t, amount, balance.Amount, "the receiver's voucher balance must equal exactly what was sent")

	// And back the other way: chain B sends the voucher back to chain A,
	// which must unwind it to the original native uaeth, not another
	// wrapped layer.
	returnMsg := ibctransfertypes.NewMsgTransfer(
		path.EndpointB.ChannelConfig.PortID, path.EndpointB.ChannelID,
		sdk.NewCoin(voucherDenom, amount), chainB.SenderAccount.GetAddress().String(), chainA.SenderAccount.GetAddress().String(),
		timeoutHeight, 0, "",
	)
	res, err = chainB.SendMsgs(returnMsg)
	require.NoError(t, err)
	returnPacket, err := ibctesting.ParsePacketFromEvents(res.Events)
	require.NoError(t, err)
	require.NoError(t, path.RelayPacket(returnPacket))

	appA := chainA.App.(*App)
	finalBalance := appA.BankKeeper.GetBalance(chainA.GetContext(), chainA.SenderAccount.GetAddress(), "uaeth")
	require.Equal(t, sdkmath.NewInt(1_000_000_000), finalBalance.Amount, "round-tripping the transfer must return exactly the original native balance")
}
