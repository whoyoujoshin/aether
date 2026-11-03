package shopper

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/directory"
	"github.com/whoyoujoshin/aether/paywall"
	"github.com/whoyoujoshin/aether/wallet"
	"github.com/whoyoujoshin/aether/x402"
)

type memChain struct {
	mu   sync.Mutex
	code map[string]uint32
}

func (m *memChain) Account(context.Context, string) (uint64, uint64, error) { return 1, 0, nil }

func (m *memChain) Tx(_ context.Context, hash string) (bool, uint32, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	code, ok := m.code[hash]
	return ok, code, 10, nil
}

func (m *memChain) land(hash string, code uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.code == nil {
		m.code = map[string]uint32{}
	}
	m.code[hash] = code
}

func testWallet(t *testing.T) (*wallet.Wallet, string) {
	t.Helper()
	reg := codectypes.NewInterfaceRegistry()
	mldsa.RegisterInterfaces(reg)
	w, err := wallet.NewWallet("shopper-test", "memory", t.TempDir(), codec.NewProtoCodec(reg))
	require.NoError(t, err)
	acc, _, err := w.CreateAccount("buyer")
	require.NoError(t, err)
	return w, acc.Address
}

type buyEnv struct {
	payee    string
	dir      string
	quoteID  string
	buyer    Buyer
	settles  *int
	paidHits *int
	chain    *memChain
}

func newBuyEnv(t *testing.T, failOnce bool) buyEnv {
	t.Helper()
	w, payer := testWallet(t)
	payee := bech("sellertest")
	var settles, paid int
	chain := &memChain{}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(paywall.HeaderPaymentSignature) == "" {
			h, _ := paywall.EncodeHeader(x402.PaymentRequired{
				X402Version: x402.Version,
				Accepts:     []x402.PaymentRequirements{exactReq(payee, "1", "", nil)},
			})
			w.Header().Set(paywall.HeaderPaymentRequired, h)
			w.WriteHeader(http.StatusPaymentRequired)
			return
		}
		paid++
		if failOnce && paid == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"hello":"ok"}`))
	}))
	t.Cleanup(srv.Close)
	fac := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body x402.VerifyRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		var ep x402.ExactPayload
		_ = json.Unmarshal(body.PaymentPayload.Payload, &ep)
		raw, _ := base64.StdEncoding.DecodeString(ep.Transaction)
		hash := x402.TxHash(raw)
		if r.URL.Path == "/verify" {
			_ = json.NewEncoder(w).Encode(x402.VerifyResponse{IsValid: true, Payer: payer})
			return
		}
		settles++
		chain.land(hash, 0)
		_ = json.NewEncoder(w).Encode(x402.SettleResponse{
			Success: true, Payer: payer, Transaction: hash, Network: x402.Network("aether-testnet-1"), Amount: "1",
		})
	}))
	t.Cleanup(fac.Close)

	dir := t.TempDir()
	res, err := Quote(context.Background(), QuoteInput{
		ChainID: "aether-testnet-1", Assets: testAssets(t), Policy: testPolicy(),
		Listings: directoryListing(srv.URL, payee),
		Request:  Request{Query: "practice"},
		Probe: func(ctx context.Context, method, raw string, body []byte) (ProbeResult, error) {
			return ProbePayment(ctx, srv.Client(), method, srv.URL, body)
		},
	})
	require.NoError(t, err)
	require.NotNil(t, res.Selected)
	require.NoError(t, SaveQuote(dir, "aether-testnet-1", *res.Selected, res.Decision))

	pol := testPolicy()
	return buyEnv{
		payee: payee, dir: dir, quoteID: res.Selected.QuoteID, settles: &settles, paidHits: &paid, chain: chain,
		buyer: Buyer{
			ChainID: "aether-testnet-1", StateDir: dir, Policy: pol, Wallet: w, KeyName: "buyer",
			Facilitator: Facilitator{BaseURL: fac.URL, Client: fac.Client()}, Chain: chain,
			HTTP: srv.Client(), ResourceWait: -1,
		},
	}
}

func directoryListing(url, payee string) []directory.Listing {
	return []directory.Listing{listing(url, "Aether hello", "practice purchase", payee, "1", 1)}
}

func mustSettleHeader(payer string) string {
	h, err := paywall.EncodeHeader(x402.SettleResponse{Success: true, Payer: payer, Transaction: "PENDING", Network: x402.Network("aether-testnet-1"), Amount: "1"})
	if err != nil {
		panic(err)
	}
	return h
}

func TestBuyWithoutConfirmDoesNotSign(t *testing.T) {
	env := newBuyEnv(t, false)
	res, err := env.buyer.Buy(context.Background(), BuyInput{QuoteID: env.quoteID, IdempotencyKey: "hello-1"})
	require.NoError(t, err)
	require.Equal(t, CodeNeedsConfirm, res.Decision.Code)
	require.False(t, res.Decision.Spent)
	require.Equal(t, 0, *env.settles)
	_, err = loadAttempt(env.dir, "hello-1")
	require.NoError(t, err)
	got, _ := loadAttempt(env.dir, "hello-1")
	require.Nil(t, got)
}

func TestBuyPaysOnceAndRetryDoesNotSignAgain(t *testing.T) {
	env := newBuyEnv(t, false)
	in := BuyInput{QuoteID: env.quoteID, IdempotencyKey: "hello-1", Confirm: true}
	res, err := env.buyer.Buy(context.Background(), in)
	require.NoError(t, err)
	require.True(t, res.Decision.Spent)
	require.NotNil(t, res.Receipt)
	require.NotEmpty(t, res.Receipt.TxHash)
	require.Equal(t, res.Receipt.TxHash, res.Receipt.Facilitator.Transaction)
	require.Equal(t, 1, *env.settles)
	raw, err := osReadReceipt(env.dir, res.Receipt.TxHash)
	require.NoError(t, err)
	require.NotContains(t, raw, "tx_b64")
	require.Contains(t, raw, res.Receipt.TxHash)

	again, err := env.buyer.Buy(context.Background(), in)
	require.NoError(t, err)
	require.Equal(t, CodeAlreadyPaid, again.Decision.Code)
	require.Equal(t, res.Receipt.TxHash, again.Receipt.TxHash)
	require.Equal(t, 1, *env.settles, "a retry must not settle a second transaction")
	require.Equal(t, 1, *env.paidHits)
}

func TestBuyRetriesTheServiceAfterA502WithoutASecondPayment(t *testing.T) {
	env := newBuyEnv(t, true)
	res, err := env.buyer.Buy(context.Background(), BuyInput{QuoteID: env.quoteID, IdempotencyKey: "hello-1", Confirm: true})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.Receipt.HTTPStatus)
	require.Equal(t, 1, *env.settles)
	require.Equal(t, 2, *env.paidHits)
	require.Contains(t, res.Body, "hello")
}

func TestBuyFacilitatorDownKeepsOneSignature(t *testing.T) {
	env := newBuyEnv(t, false)
	env.buyer.Facilitator.BaseURL = "http://127.0.0.1:1"
	_, err := env.buyer.Buy(context.Background(), BuyInput{QuoteID: env.quoteID, IdempotencyKey: "hello-1", Confirm: true})
	require.Error(t, err)
	var se *Error
	require.ErrorAs(t, err, &se)
	require.Equal(t, CodeFacilitatorDown, se.Code)
	first, err := loadAttempt(env.dir, "hello-1")
	require.NoError(t, err)
	require.NotNil(t, first)
	require.NotEmpty(t, first.TxHash)

	_, err = env.buyer.Buy(context.Background(), BuyInput{QuoteID: env.quoteID, IdempotencyKey: "hello-1", Confirm: true})
	require.Error(t, err)
	second, err := loadAttempt(env.dir, "hello-1")
	require.NoError(t, err)
	require.Equal(t, first.TxHash, second.TxHash)
	require.Equal(t, first.TxB64, second.TxB64)
}

func TestBuyOverCapDoesNotSign(t *testing.T) {
	env := newBuyEnv(t, false)
	env.buyer.Policy.MaxPerPayment = sdkmath.NewInt(0)
	res, err := env.buyer.Buy(context.Background(), BuyInput{QuoteID: env.quoteID, IdempotencyKey: "hello-1", Confirm: true})
	require.NoError(t, err)
	require.Equal(t, CodeOverCap, res.Decision.Code)
	got, err := loadAttempt(env.dir, "hello-1")
	require.NoError(t, err)
	require.Nil(t, got)
	require.Equal(t, 0, *env.settles)
}

func TestBuyLockBlocksASecondSigner(t *testing.T) {
	env := newBuyEnv(t, false)
	unlock, err := acquireLock(env.dir)
	require.NoError(t, err)
	defer unlock()
	_, err = env.buyer.Buy(context.Background(), BuyInput{QuoteID: env.quoteID, IdempotencyKey: "hello-1", Confirm: true})
	require.Error(t, err)
	require.Equal(t, 0, *env.settles)
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

func osReadReceipt(dir, hash string) (string, error) {
	b, err := loadReceipt(dir, hash)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(b)
	return string(raw), err
}
