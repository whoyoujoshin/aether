package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/directory"
	"github.com/whoyoujoshin/aether/paywall"
	"github.com/whoyoujoshin/aether/wallet"
)

// --- owner approvals and alerts ---

type alertSink struct {
	mu     sync.Mutex
	events []map[string]any
	badSig int
}

func newAlertSink(t *testing.T, secret string) *alertSink {
	s := &alertSink{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		m := hmac.New(sha256.New, []byte(secret))
		m.Write(body)
		s.mu.Lock()
		defer s.mu.Unlock()
		if r.Header.Get("X-Aether-Signature") != hex.EncodeToString(m.Sum(nil)) {
			s.badSig++
		}
		var e map[string]any
		_ = json.Unmarshal(body, &e)
		s.events = append(s.events, e)
	}))
	t.Cleanup(srv.Close)
	notifyWebhook, notifySecret = srv.URL, secret
	t.Cleanup(func() { notifyWebhook, notifySecret = "", "" })
	return s
}

func (s *alertSink) names() []string {
	notifyWG.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, e := range s.events {
		out = append(out, e["event"].(string))
	}
	return out
}

type owner struct {
	dir, key, address string
	w                 *wallet.Wallet
}

func setupApprovals(t *testing.T, threshold int64) owner {
	t.Helper()
	dir := t.TempDir()
	registry := codectypes.NewInterfaceRegistry()
	mldsa.RegisterInterfaces(registry)
	w, err := wallet.NewWallet("aetherd", "test", dir, codec.NewProtoCodec(registry))
	require.NoError(t, err)
	acc, _, err := w.CreateAccount("owner")
	require.NoError(t, err)
	approvalThreshold, approver = math.NewInt(threshold), acc.Address
	t.Cleanup(func() { approvalThreshold, approver = math.Int{}, "" })
	return owner{dir: dir, key: "owner", address: acc.Address, w: w}
}

func (o owner) decide(t *testing.T, cmd, id string) {
	t.Helper()
	ks, sf := keyringDir, stateFile
	require.NoError(t, runApprovalCommand([]string{cmd, id, "--approver-keyring-dir", o.dir, "--approver-key", o.key,
		"--approver-keyring-backend", "test", "--keyring-dir", keyringDir, "--state-file", stateFile}))
	keyringDir, stateFile = ks, sf
}

func TestApprovals_BigPaymentWaitsForTheOwner(t *testing.T) {
	f := setupAgent(t)
	o := setupApprovals(t, 500_000)
	alerts := newAlertSink(t, "s3cret")

	// Small payments go straight through.
	small, err := send(t, "small", "0.5 AETH", "")
	require.NoError(t, err)
	require.Equal(t, statusPending, small.Status)
	require.Len(t, f.broadcasts, 1)

	// A big one waits: nothing signed, sent or reserved.
	big, err := send(t, "big", "0.8 AETH", "rent")
	require.NoError(t, err)
	require.Equal(t, statusPendingApproval, big.Status)
	require.NotEmpty(t, big.ApprovalID)
	require.Len(t, f.broadcasts, 1)
	require.Equal(t, int64(500_000), spent(t))

	again, err := send(t, "big", "0.8 AETH", "rent")
	require.NoError(t, err)
	require.Equal(t, big.ApprovalID, again.ApprovalID, "the same request, not a new one")

	o.decide(t, "approve", big.ApprovalID)
	sent, err := send(t, "big", "0.8 AETH", "rent")
	require.NoError(t, err)
	require.Equal(t, statusPending, sent.Status)
	require.Len(t, f.broadcasts, 2)
	memo, _ := decode(t, f.broadcasts[1])
	require.Equal(t, "rent", memo)
	st, _ := loadState()
	require.Empty(t, st.Approvals, "an approval is used once")

	// Retrying the sent payment is a replay, not a new approval.
	replay, err := send(t, "big", "0.8 AETH", "rent")
	require.NoError(t, err)
	require.True(t, replay.Replayed)

	require.Equal(t, []string{"payment_sent", "approval_requested", "payment_sent"}, alerts.names())
	require.Zero(t, alerts.badSig, "every alert is signed with the secret")
}

func TestApprovals_RejectedStaysRejected(t *testing.T) {
	f := setupAgent(t)
	o := setupApprovals(t, 100_000)
	alerts := newAlertSink(t, "k")

	out, err := send(t, "no", "0.2 AETH", "")
	require.NoError(t, err)
	o.decide(t, "reject", out.ApprovalID)
	for i := 0; i < 2; i++ {
		_, err = send(t, "no", "0.2 AETH", "")
		requireCode(t, err, codeApprovalRejected)
	}
	require.Empty(t, f.broadcasts)
	require.Contains(t, alerts.names(), "payment_refused")

	_, err = send(t, "no", "0.3 AETH", "")
	requireCode(t, err, codeIdempotencyConflict)
}

func TestApprovals_OnlyTheOwnersSignatureCounts(t *testing.T) {
	f := setupAgent(t)
	o := setupApprovals(t, 100_000)
	out, err := send(t, "k", "0.2 AETH", "")
	require.NoError(t, err)
	st, _ := loadState()
	req := st.Approvals["k"]

	write := func(d decisionFile) {
		bz, _ := json.Marshal(d)
		require.NoError(t, os.MkdirAll(approvalsDir(), 0o700))
		require.NoError(t, os.WriteFile(decisionPath(req.ID), bz, 0o600))
	}
	signWith := func(w *wallet.Wallet, name, decision, hash string) decisionFile {
		sig, pub, err := w.SignBytes(name, approvalMessage(req.ID, decision, hash))
		require.NoError(t, err)
		return decisionFile{ID: req.ID, Decision: decision, ParamsHash: hash,
			PubKey: base64.StdEncoding.EncodeToString(pub), Signature: base64.StdEncoding.EncodeToString(sig)}
	}

	// The agent signing its own approval (it can write files here).
	agentWallet, err := newWallet()
	require.NoError(t, err)
	write(signWith(agentWallet, accountName, decisionApprove, req.paramsHash()))
	still, err := send(t, "k", "0.2 AETH", "")
	require.NoError(t, err)
	require.Equal(t, statusPendingApproval, still.Status)

	// The owner's signature, but over a different payment.
	write(signWith(o.w, o.key, decisionApprove, "0000"))
	still, err = send(t, "k", "0.2 AETH", "")
	require.NoError(t, err)
	require.Equal(t, statusPendingApproval, still.Status)

	// A valid signature with the decision flipped after signing.
	d := signWith(o.w, o.key, decisionReject, req.paramsHash())
	d.Decision = decisionApprove
	write(d)
	still, err = send(t, "k", "0.2 AETH", "")
	require.NoError(t, err)
	require.Equal(t, statusPendingApproval, still.Status)
	require.Empty(t, f.broadcasts)

	// The owner's command refuses the agent's own keyring.
	err = runApprovalCommand([]string{"approve", out.ApprovalID, "--approver-keyring-dir", keyringDir, "--approver-key", accountName,
		"--approver-keyring-backend", "test", "--keyring-dir", keyringDir, "--state-file", stateFile})
	require.ErrorContains(t, err, "must not be the agent's keyring")
}

func TestApprovals_SurviveATransientFailure(t *testing.T) {
	f := setupAgent(t)
	o := setupApprovals(t, 100_000)
	out, err := send(t, "k", "0.2 AETH", "")
	require.NoError(t, err)
	o.decide(t, "approve", out.ApprovalID)

	f.accountErr = status.Error(codes.Unavailable, "node down")
	_, err = send(t, "k", "0.2 AETH", "")
	requireCode(t, classify(err), codeNodeUnreachable) // as coded() reports it

	f.accountErr = nil
	sent, err := send(t, "k", "0.2 AETH", "")
	require.NoError(t, err)
	require.Equal(t, statusPending, sent.Status, "the owner doesn't have to approve twice")
}

func TestApprovals_ExpireIfUndecided(t *testing.T) {
	setupAgent(t)
	setupApprovals(t, 100_000)
	out, err := send(t, "k", "0.2 AETH", "")
	require.NoError(t, err)
	st, _ := loadState()
	st.Approvals["k"].CreatedAt = time.Now().Add(-25 * time.Hour)
	require.NoError(t, st.save())
	again, err := send(t, "k", "0.2 AETH", "")
	require.NoError(t, err)
	require.NotEqual(t, out.ApprovalID, again.ApprovalID, "a stale request is asked again")
}

func TestApprovals_FetchPaidWaitsToo(t *testing.T) {
	f := setupAgent(t)
	f.autoInclude = true
	o := setupApprovals(t, 10_000)
	srv, served := sellerServer(t, f, 20_000)

	out, err := fetch(t, srv.URL+"/forecast", "1 AETH", "buy", 5)
	require.NoError(t, err)
	require.Equal(t, "approval_pending", out.Status)
	require.Empty(t, f.broadcasts)

	o.decide(t, "approve", out.ApprovalID)
	out, err = fetch(t, srv.URL+"/forecast", "1 AETH", "buy", 5)
	require.NoError(t, err)
	require.Equal(t, "paid", out.Status)
	require.Equal(t, int32(1), served.Load())
}

func TestApprovalsCommand_Lists(t *testing.T) {
	setupAgent(t)
	setupApprovals(t, 100_000)
	_, err := send(t, "k", "0.2 AETH", "")
	require.NoError(t, err)
	require.NoError(t, runApprovalCommand([]string{"approvals", "--keyring-dir", keyringDir, "--state-file", stateFile}))
}

// --- service directory ---

func resetDirectory(t *testing.T) {
	dir, dirOnce = nil, sync.Once{}
	manifestFetch = directory.SafeFetcher(true)
	t.Cleanup(func() { dir, dirOnce, manifestFetch = nil, sync.Once{}, nil })
}

func manifestAt(t *testing.T, m paywall.Manifest) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(m) }))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestFindServices(t *testing.T) {
	f := setupAgent(t)
	resetDirectory(t)
	sellerA, sellerB := sdk.AccAddress("seller_a____________").String(), sdk.AccAddress("seller_b____________").String()
	weather := manifestAt(t, paywall.Manifest{X402Version: 1, Name: "Weather", Description: "Forecasts by city", Network: chainID, PayTo: sellerA, Price: "20000", Schemes: []string{"aether-memo", "aether-prepaid"}, MinDeposit: "100000"})
	translate := manifestAt(t, paywall.Manifest{X402Version: 1, Name: "Translate", Description: "Any language", Network: chainID, PayTo: sellerB, Price: "300000", Schemes: []string{"aether-memo"}})
	fake := manifestAt(t, paywall.Manifest{X402Version: 1, Name: "Weather (fake)", Network: chainID, PayTo: sellerA, Price: "1"})
	one := sdk.NewCoins(sdk.NewInt64Coin("uaeth", 1))
	f.payments = []wallet.IncomingPayment{
		{Hash: "A", Height: 10, From: sellerA, Memo: directory.AnnouncePrefix + weather, Amount: one},
		{Hash: "B", Height: 11, From: sellerB, Memo: directory.AnnouncePrefix + translate, Amount: one},
		{Hash: "C", Height: 12, From: sellerB, Memo: directory.AnnouncePrefix + fake, Amount: one}, // payee isn't the announcer
	}

	_, out, err := toolFindServices(context.Background(), nil, findServicesInput{})
	require.NoError(t, err)
	require.Len(t, out.Services, 2)

	_, out, err = toolFindServices(context.Background(), nil, findServicesInput{Query: "weather city"})
	require.NoError(t, err)
	require.Len(t, out.Services, 1)
	s := out.Services[0]
	require.Equal(t, "Weather", s.Name)
	require.Equal(t, amountDTO{Uaeth: "20000", Aeth: "0.02"}, s.Price)
	require.Equal(t, "0.1", s.MinDeposit.Aeth)
	require.Equal(t, sellerA, s.PayTo)

	_, out, err = toolFindServices(context.Background(), nil, findServicesInput{MaxPrice: "0.1 AETH"})
	require.NoError(t, err)
	require.Len(t, out.Services, 1)
	require.Equal(t, "Weather", out.Services[0].Name)
}

func TestAnnounceService(t *testing.T) {
	f := setupAgent(t)
	resetDirectory(t)
	w, _ := newWallet()
	agent, _ := getOrCreateAgentAccount(w)

	mine := manifestAt(t, paywall.Manifest{X402Version: 1, Name: "Mine", Network: chainID, PayTo: agent.Address, Price: "5"})
	notMine := manifestAt(t, paywall.Manifest{X402Version: 1, Name: "Theirs", Network: chainID, PayTo: sellerAddr(), Price: "5"})

	_, _, err := toolAnnounceService(context.Background(), nil, announceServiceInput{URL: notMine, IdempotencyKey: "a1"})
	requireCode(t, err, codeServiceUnverifiable)
	require.Empty(t, f.broadcasts, "nothing paid for a listing that wouldn't show")

	_, out, err := toolAnnounceService(context.Background(), nil, announceServiceInput{URL: mine + "/", IdempotencyKey: "a2"})
	require.NoError(t, err)
	require.Equal(t, statusPending, out.Status)
	memo, _ := decode(t, f.broadcasts[0])
	require.Equal(t, directory.AnnouncePrefix+mine, memo)
	tx := decodeTx(t, f.broadcasts[0])
	require.Equal(t, directory.Address(), tx.GetMsgs()[0].(*banktypes.MsgSend).ToAddress)

	_, out, err = toolAnnounceService(context.Background(), nil, announceServiceInput{URL: mine, Delist: true, IdempotencyKey: "a3"})
	require.NoError(t, err)
	memo, _ = decode(t, f.broadcasts[1])
	require.Equal(t, directory.DelistPrefix+mine, memo)
}
