package x402

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/wallet"
)

const testChain = "aether-testnet-1"

type fakeChain struct {
	mu        sync.Mutex
	number    uint64
	sequence  uint64
	height    int64
	simErr    error
	simGas    uint64 // gas the simulation reports (default 100,000)
	checkCode uint32
	checkLog  string
	deliver   uint32 // code the tx gets in its block
	sent      [][]byte
}

func (f *fakeChain) Account(context.Context, string) (uint64, uint64, error) {
	return f.number, f.sequence, nil
}
func (f *fakeChain) LatestHeight(context.Context) (int64, error) { return f.height, nil }
func (f *fakeChain) Simulate(context.Context, []byte) (uint64, error) {
	if f.simGas == 0 {
		return 100000, f.simErr
	}
	return f.simGas, f.simErr
}
func (f *fakeChain) Broadcast(_ context.Context, b []byte) (string, uint32, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, b)
	return TxHash(b), f.checkCode, f.checkLog, nil
}
func (f *fakeChain) TxResult(_ context.Context, hash string) (bool, uint32, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, b := range f.sent {
		if TxHash(b) == hash {
			return true, f.deliver, "", nil
		}
	}
	return false, 0, "", nil
}

type signer struct {
	w    *wallet.Wallet
	addr string
}

func newSigner(t *testing.T) signer {
	t.Helper()
	reg := codectypes.NewInterfaceRegistry()
	mldsa.RegisterInterfaces(reg)
	w, err := wallet.NewWallet("x402-test", "memory", t.TempDir(), codec.NewProtoCodec(reg))
	require.NoError(t, err)
	acc, _, err := w.CreateAccount("payer")
	require.NoError(t, err)
	return signer{w: w, addr: acc.Address}
}

func (s signer) sign(t *testing.T, msgs []sdk.Msg, number, sequence uint64, timeoutHeight uint64) []byte {
	t.Helper()
	signed, err := s.w.BuildAndSignMsgsTx("payer", msgs, wallet.TxParams{
		ChainID: testChain, AccountNumber: number, Sequence: sequence, GasLimit: 200000,
		Fees: sdk.NewCoins(sdk.NewCoin("uaeth", math.NewInt(20))),
	})
	require.NoError(t, err)
	if timeoutHeight == 0 {
		return signed.Bytes
	}
	// Add a timeout height (the wallet doesn't set one) and sign again.
	var raw txtypes.TxRaw
	require.NoError(t, raw.Unmarshal(signed.Bytes))
	var body txtypes.TxBody
	require.NoError(t, body.Unmarshal(raw.BodyBytes))
	body.TimeoutHeight = timeoutHeight
	bb, err := body.Marshal()
	require.NoError(t, err)
	raw.BodyBytes = bb
	doc := txtypes.SignDoc{BodyBytes: raw.BodyBytes, AuthInfoBytes: raw.AuthInfoBytes, ChainId: testChain, AccountNumber: number}
	signBytes, err := doc.Marshal()
	require.NoError(t, err)
	sig, _, err := s.w.SignBytes("payer", signBytes)
	require.NoError(t, err)
	raw.Signatures[0] = sig
	out, err := raw.Marshal()
	require.NoError(t, err)
	return out
}

func send(from, to string, coins sdk.Coins) sdk.Msg {
	return &banktypes.MsgSend{FromAddress: from, ToAddress: to, Amount: coins}
}

func payloadFor(req PaymentRequirements, txBytes []byte) PaymentPayload {
	pl, _ := json.Marshal(ExactPayload{Transaction: base64.StdEncoding.EncodeToString(txBytes)})
	return PaymentPayload{X402Version: Version, Accepted: req, Payload: pl}
}

// payTo needs the aether prefixes, set before it's computed (package vars
// initialize before init funcs run).
var payTo = func() string {
	app.SetAddressPrefixes()
	return sdk.AccAddress("x402_test_payee_addr").String()
}()

func requirement() PaymentRequirements {
	return PaymentRequirements{Scheme: SchemeExact, Network: Network(testChain), Amount: "500", Asset: "uaeth", PayTo: payTo, MaxTimeoutSeconds: 60}
}

func TestVerifyAcceptsAnExactSignedSend(t *testing.T) {
	s := newSigner(t)
	chain := &fakeChain{number: 7, sequence: 3, height: 100}
	f := NewFacilitator(testChain, chain)
	req := requirement()
	tx := s.sign(t, []sdk.Msg{send(s.addr, payTo, sdk.NewCoins(sdk.NewInt64Coin("uaeth", 500)))}, 7, 3, 0)

	got := f.Verify(context.Background(), payloadFor(req, tx), req)
	require.Equal(t, VerifyResponse{IsValid: true, Payer: s.addr}, got)
	require.Empty(t, chain.sent, "verify never broadcasts")

	sup := f.Supported()
	require.Equal(t, "cosmos:aether-testnet-1", sup.Kinds[0].Network)
	require.NotNil(t, sup.Signers, "the spec requires signers, even empty: this facilitator holds no key")
}

func TestVerifyRefusesWhatDoesntPayExactly(t *testing.T) {
	s := newSigner(t)
	chain := &fakeChain{number: 7, sequence: 3, height: 100}
	f := NewFacilitator(testChain, chain)
	req := requirement()
	ctx := context.Background()
	sign := func(msgs ...sdk.Msg) []byte { return s.sign(t, msgs, 7, 3, 0) }
	good := send(s.addr, payTo, sdk.NewCoins(sdk.NewInt64Coin("uaeth", 500)))
	other := sdk.AccAddress("someone_else_address").String()

	cases := []struct {
		name   string
		tx     []byte
		req    PaymentRequirements
		reason string
	}{
		{"less", sign(send(s.addr, payTo, sdk.NewCoins(sdk.NewInt64Coin("uaeth", 499)))), req, ReasonAmountMismatch},
		{"more", sign(send(s.addr, payTo, sdk.NewCoins(sdk.NewInt64Coin("uaeth", 501)))), req, ReasonAmountMismatch},
		{"other denom", sign(send(s.addr, payTo, sdk.NewCoins(sdk.NewInt64Coin("ibc/usdc", 500)))), req, ReasonAmountMismatch},
		{"extra coin", sign(send(s.addr, payTo, sdk.NewCoins(sdk.NewInt64Coin("uaeth", 500), sdk.NewInt64Coin("ibc/usdc", 1)))), req, ReasonAmountMismatch},
		{"other recipient", sign(send(s.addr, other, sdk.NewCoins(sdk.NewInt64Coin("uaeth", 500)))), req, ReasonRecipientMismatch},
		{"two messages", sign(good, send(s.addr, other, sdk.NewCoins(sdk.NewInt64Coin("uaeth", 1)))), req, ReasonMessage},
		{"someone else's money", sign(send(other, payTo, sdk.NewCoins(sdk.NewInt64Coin("uaeth", 500)))), req, ReasonSigner},
	}
	for _, c := range cases {
		got := f.Verify(ctx, payloadFor(c.req, c.tx), c.req)
		require.False(t, got.IsValid, c.name)
		require.Equal(t, c.reason, got.InvalidReason, c.name)
	}

	// The payload names a different price than this resource asks.
	cheap := req
	cheap.Amount = "1"
	tx := sign(good)
	require.Equal(t, ReasonAcceptedMismatch, f.Verify(ctx, payloadFor(cheap, tx), req).InvalidReason)

	// Wrong version, scheme, network.
	p := payloadFor(req, tx)
	p.X402Version = 1
	require.Equal(t, ReasonVersion, f.Verify(ctx, p, req).InvalidReason)
	r2 := req
	r2.Network = "eip155:8453"
	require.Equal(t, ReasonNetwork, f.Verify(ctx, payloadFor(r2, tx), r2).InvalidReason)
	r3 := req
	r3.Scheme = "upto"
	require.Equal(t, ReasonScheme, f.Verify(ctx, payloadFor(r3, tx), r3).InvalidReason)

	// Not base64, not a transaction.
	bad := payloadFor(req, tx)
	bad.Payload = json.RawMessage(`{"transaction":"%%%"}`)
	require.Equal(t, ReasonPayload, f.Verify(ctx, bad, req).InvalidReason)
	require.Equal(t, ReasonTransaction, f.Verify(ctx, payloadFor(req, []byte("garbage")), req).InvalidReason)
}

func TestVerifyChecksTheSignatureSequenceAndChain(t *testing.T) {
	s := newSigner(t)
	chain := &fakeChain{number: 7, sequence: 3, height: 100}
	f := NewFacilitator(testChain, chain)
	req := requirement()
	ctx := context.Background()
	msg := send(s.addr, payTo, sdk.NewCoins(sdk.NewInt64Coin("uaeth", 500)))

	// A flipped signature byte.
	tx := s.sign(t, []sdk.Msg{msg}, 7, 3, 0)
	var raw txtypes.TxRaw
	require.NoError(t, raw.Unmarshal(tx))
	raw.Signatures[0][10] ^= 1
	forged, _ := raw.Marshal()
	require.Equal(t, ReasonSignature, f.Verify(ctx, payloadFor(req, forged), req).InvalidReason)

	// Signed for another account number (another chain or account).
	require.Equal(t, ReasonSignature, f.Verify(ctx, payloadFor(req, s.sign(t, []sdk.Msg{msg}, 8, 3, 0)), req).InvalidReason)

	// A sequence already used, or not yet reached.
	require.Equal(t, ReasonSequence, f.Verify(ctx, payloadFor(req, s.sign(t, []sdk.Msg{msg}, 7, 2, 0)), req).InvalidReason)
	require.Equal(t, ReasonSequence, f.Verify(ctx, payloadFor(req, s.sign(t, []sdk.Msg{msg}, 7, 4, 0)), req).InvalidReason)

	// The chain says the balance doesn't cover it.
	chain.simErr = errors.New("spendable balance 10uaeth is smaller than 500uaeth: insufficient funds")
	require.Equal(t, ReasonInsufficientFunds, f.Verify(ctx, payloadFor(req, tx), req).InvalidReason)
	chain.simErr = nil

	// More gas than the tx's limit (200,000): it would land, charge the
	// fee and fail.
	chain.simGas = 200001
	require.Equal(t, ReasonGas, f.Verify(ctx, payloadFor(req, tx), req).InvalidReason)
	chain.simGas = 200000
	require.True(t, f.Verify(ctx, payloadFor(req, tx), req).IsValid)
	chain.simGas = 0

	// A timeout height already passed fails; one ahead passes. (The
	// timeout is in the signed body, so a payer can bound how long a
	// payment it handed over stays good.)
	expired := s.sign(t, []sdk.Msg{msg}, 7, 3, 100)
	require.Equal(t, ReasonExpired, f.Verify(ctx, payloadFor(req, expired), req).InvalidReason)
	ahead := s.sign(t, []sdk.Msg{msg}, 7, 3, 101)
	require.True(t, f.Verify(ctx, payloadFor(req, ahead), req).IsValid)
}

func TestSettleBroadcastsOnceAndWaitsForTheBlock(t *testing.T) {
	s := newSigner(t)
	chain := &fakeChain{number: 7, sequence: 3, height: 100}
	f := NewFacilitator(testChain, chain)
	f.PollInterval = time.Millisecond
	req := requirement()
	ctx := context.Background()
	tx := s.sign(t, []sdk.Msg{send(s.addr, payTo, sdk.NewCoins(sdk.NewInt64Coin("uaeth", 500)))}, 7, 3, 0)

	got := f.Settle(ctx, payloadFor(req, tx), req)
	require.True(t, got.Success, got.ErrorReason)
	require.Equal(t, TxHash(tx), got.Transaction)
	require.Equal(t, s.addr, got.Payer)
	require.Equal(t, "500", got.Amount)
	require.Equal(t, "cosmos:aether-testnet-1", got.Network)
	require.Len(t, chain.sent, 1)

	again := f.Settle(ctx, payloadFor(req, tx), req)
	require.False(t, again.Success)
	require.Equal(t, ReasonAlreadySettled, again.ErrorReason, "one payment buys one request")
	require.Len(t, chain.sent, 1)
}

func TestSettleReportsARefusal(t *testing.T) {
	s := newSigner(t)
	chain := &fakeChain{number: 7, sequence: 3, height: 100, checkCode: 5, checkLog: "insufficient funds"}
	f := NewFacilitator(testChain, chain)
	f.PollInterval = time.Millisecond
	req := requirement()
	ctx := context.Background()
	tx := s.sign(t, []sdk.Msg{send(s.addr, payTo, sdk.NewCoins(sdk.NewInt64Coin("uaeth", 500)))}, 7, 3, 0)

	got := f.Settle(ctx, payloadFor(req, tx), req)
	require.False(t, got.Success)
	require.Equal(t, ReasonInsufficientFunds, got.ErrorReason)

	// Refused at CheckTx moves nothing, so the same payment may be tried again.
	chain.checkCode, chain.checkLog = 0, ""
	require.True(t, f.Settle(ctx, payloadFor(req, tx), req).Success)

	// Failing in its block is reported, not success.
	chain2 := &fakeChain{number: 7, sequence: 3, height: 100, deliver: 5}
	f2 := NewFacilitator(testChain, chain2)
	f2.PollInterval = time.Millisecond
	got = f2.Settle(ctx, payloadFor(req, tx), req)
	require.False(t, got.Success)
	require.Equal(t, ReasonSettlementRejected, got.ErrorReason)
}
