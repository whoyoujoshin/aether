package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/jobs"
	"github.com/whoyoujoshin/aether/wallet"
	"github.com/whoyoujoshin/aether/x/escrow"
)

// jobsFake is escrowFake that also keeps what a node's tx search would
// find: payments to the board, and escrow_created events.
type jobsFake struct {
	*escrowFake
	created []wallet.EscrowCreated
}

func (j *jobsFake) broadcast(s wallet.SignedTx) (wallet.BroadcastResult, error) {
	res, err := j.escrowFake.broadcast(s)
	if err != nil || res.Code != 0 {
		return res, err
	}
	tx, derr := app.MakeEncodingConfig().TxConfig.TxDecoder()(s.Bytes)
	if derr != nil {
		return res, err
	}
	j.fakeChain.mu.Lock()
	d := j.fakeChain.blocks[res.TxHash]
	j.fakeChain.mu.Unlock()
	if d == nil || d.Code != 0 {
		return res, err
	}
	for _, m := range tx.GetMsgs() {
		switch m := m.(type) {
		case *banktypes.MsgSend:
			if m.ToAddress == jobs.Address() {
				j.fakeChain.mu.Lock()
				j.fakeChain.payments = append(j.fakeChain.payments, wallet.IncomingPayment{Hash: res.TxHash, Height: d.Height, From: m.FromAddress,
					Memo: tx.(sdk.TxWithMemo).GetMemo(), Amount: sdk.Coins(m.Amount)})
				j.fakeChain.mu.Unlock()
			}
		case *escrow.MsgCreateEscrow:
			j.escrowFake.mu.Lock()
			id := j.createdBy[res.TxHash]
			j.escrowFake.mu.Unlock()
			j.created = append(j.created, wallet.EscrowCreated{ID: id, Payer: m.Payer, Payee: m.Payee, Arbiter: m.Arbiter, Amount: sdk.Coins(m.Amount).String(),
				ExpiresAt: m.ExpiresAt, OnExpiry: m.OnExpiry.String(), Terms: m.Terms, TxHash: res.TxHash, Height: d.Height})
		}
	}
	return res, err
}

func (j *jobsFake) escrowsWithTerms(substr string, _ int) ([]wallet.EscrowCreated, error) {
	var out []wallet.EscrowCreated
	for _, e := range j.created {
		if strings.Contains(e.Terms, substr) {
			out = append(out, e)
		}
	}
	return out, nil
}

// fakeJobHost stores whatever matches its hash.
func fakeJobHost(t *testing.T) *httptest.Server {
	var mu sync.Mutex
	texts := map[string][]byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		hash := strings.TrimPrefix(r.URL.Path, "/jobs/blobs/")
		switch {
		case r.URL.Path == "/jobs/healthz":
		case r.Method == http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			if jobs.Hash(b) != hash {
				http.Error(w, "hash", http.StatusBadRequest)
				return
			}
			texts[hash] = b
			w.WriteHeader(http.StatusCreated)
		case texts[hash] != nil:
			w.Write(texts[hash])
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// as switches which agent the tools act as.
func as(t *testing.T, dir, name string) string {
	t.Helper()
	keyringDir, stateFile = filepath.Join(dir, name, "keyring"), filepath.Join(dir, name, "state.json")
	w, err := newWallet()
	require.NoError(t, err)
	acc, err := getOrCreateAgentAccount(w)
	require.NoError(t, err)
	return acc.Address
}

func TestJobs_PostOfferHireDeliverRelease(t *testing.T) {
	e, _ := setupEscrow(t)
	f := &jobsFake{escrowFake: e}
	dialChain = func() (chain, error) { return f, nil }
	host := fakeJobHost(t)
	prevURL := jobBoardURL
	jobBoardURL = host.URL + "/jobs"
	t.Cleanup(func() { jobBoardURL = prevURL })
	ctx := context.Background()
	dir := t.TempDir()
	seller := as(t, dir, "seller")
	buyer := as(t, dir, "buyer")

	// The buyer posts.
	_, posted, err := toolPostJob(ctx, nil, postJobInput{Title: "Translate a README", Description: "English to Spanish, keep the Markdown.",
		Budget: "0.5 AETH", Tags: []string{"translation"}, IdempotencyKey: "readme-es", WaitSeconds: 1})
	require.NoError(t, err)
	require.Equal(t, statusConfirmed, posted.Status, posted.Message)
	require.Equal(t, jobs.IDOf(posted.TxHash), posted.JobID)
	id := posted.JobID
	_, again, err := toolPostJob(ctx, nil, postJobInput{Title: "Translate a README", Description: "English to Spanish, keep the Markdown.",
		Budget: "0.5 AETH", Tags: []string{"translation"}, IdempotencyKey: "readme-es", WaitSeconds: 1})
	require.NoError(t, err)
	require.Equal(t, posted.TxHash, again.TxHash, "a retry doesn't post twice")

	_, _, err = toolOfferOnJob(ctx, nil, offerOnJobInput{JobID: id, Price: "0.4 AETH"})
	requireCode(t, err, codeJobNotAllowed)

	// The seller finds it and offers.
	as(t, dir, "seller")
	_, found, err := toolFindJobs(ctx, nil, findJobsInput{Query: "translation"})
	require.NoError(t, err)
	require.Len(t, found.Jobs, 1)
	require.Equal(t, "Translate a README", found.Jobs[0].Title)
	require.Equal(t, "none", found.Jobs[0].YourRole)
	_, offered, err := toolOfferOnJob(ctx, nil, offerOnJobInput{JobID: id, Price: "0.4 AETH", Note: "an hour"})
	require.NoError(t, err)
	require.Equal(t, statusConfirmed, offered.Status)
	_, _, err = toolDeliverJob(ctx, nil, deliverJobInput{JobID: id, Content: "# Léame"})
	requireCode(t, err, codeJobNotAllowed)

	// The buyer sees the offer and hires.
	as(t, dir, "buyer")
	_, job, err := toolGetJob(ctx, nil, getJobInput{JobID: id})
	require.NoError(t, err)
	require.Equal(t, "poster", job.YourRole)
	require.Equal(t, "English to Spanish, keep the Markdown.", job.Spec.Description)
	require.Len(t, job.Offers, 1)
	require.Equal(t, seller, job.Offers[0].Seller)
	require.Contains(t, job.Next, "hire_for_job")
	_, hired, err := toolHireForJob(ctx, nil, hireForJobInput{JobID: id, Seller: seller, Amount: "0.4 AETH", ExpiresIn: "48h", IdempotencyKey: "hire-1", WaitSeconds: 1})
	require.NoError(t, err)
	require.Equal(t, statusConfirmed, hired.Escrow.Status, hired.Escrow.Message)
	require.Equal(t, "0.4 AETH", hired.SellerOffer.Price)
	escrowID := hired.Escrow.EscrowID
	require.Equal(t, jobs.Terms(id), e.open[escrowID].Terms)
	require.Equal(t, escrow.ON_EXPIRY_RELEASE, e.open[escrowID].OnExpiry, "release is the default")
	_, replay, err := toolHireForJob(ctx, nil, hireForJobInput{JobID: id, Seller: seller, Amount: "0.4 AETH", ExpiresIn: "48h", IdempotencyKey: "hire-1", WaitSeconds: 1})
	require.NoError(t, err)
	require.True(t, replay.Escrow.Replayed, "a retry of the hire replays it")
	_, _, err = toolHireForJob(ctx, nil, hireForJobInput{JobID: id, Seller: seller, Amount: "1 AETH", ExpiresIn: "48h", IdempotencyKey: "hire-2", WaitSeconds: 1})
	requireCode(t, err, codeJobNotOpen)
	_, _, err = toolCloseJob(ctx, nil, closeJobInput{JobID: id})
	requireCode(t, err, codeJobNotOpen)

	// The seller delivers.
	as(t, dir, "seller")
	_, job, err = toolGetJob(ctx, nil, getJobInput{JobID: id})
	require.NoError(t, err)
	require.Equal(t, "hired_seller", job.YourRole)
	require.True(t, *job.SellerProtected)
	require.Equal(t, jobs.EscrowOpen, job.Escrow.Status)
	_, delivered, err := toolDeliverJob(ctx, nil, deliverJobInput{JobID: id, Content: "# Léame\n", WaitSeconds: 1})
	require.NoError(t, err)
	require.Equal(t, statusConfirmed, delivered.Status, delivered.Message)
	require.Equal(t, jobs.Hash([]byte("# Léame\n")), delivered.Hash)

	// The buyer reads it and pays.
	as(t, dir, "buyer")
	_, job, err = toolGetJob(ctx, nil, getJobInput{JobID: id})
	require.NoError(t, err)
	require.Equal(t, jobs.StateDelivered, job.State)
	require.Equal(t, "# Léame\n", job.Deliveries[0].Content)
	require.Contains(t, job.Next, "release_escrow")
	_, released, err := toolReleaseEscrow(ctx, nil, settleEscrowInput{ID: escrowID, WaitSeconds: 1})
	require.NoError(t, err)
	require.Equal(t, statusReleased, released.Status)
	_, job, err = toolGetJob(ctx, nil, getJobInput{JobID: id})
	require.NoError(t, err)
	require.Equal(t, jobs.EscrowReleased, job.Escrow.Status)
	require.Equal(t, buyer, job.Escrow.By)

	_, mine, err := toolFindJobs(ctx, nil, findJobsInput{State: "all", Mine: true})
	require.NoError(t, err)
	require.Len(t, mine.Jobs, 1)
	_, open, err := toolFindJobs(ctx, nil, findJobsInput{})
	require.NoError(t, err)
	require.Empty(t, open.Jobs, "a delivered job isn't open")
}

// A seller won't deliver into an escrow that can refund at the deadline
// with nobody to appeal to, unless it says so.
func TestJobs_DeliverRefusesAnUnprotectedHire(t *testing.T) {
	e, _ := setupEscrow(t)
	f := &jobsFake{escrowFake: e}
	dialChain = func() (chain, error) { return f, nil }
	prevURL := jobBoardURL
	jobBoardURL = fakeJobHost(t).URL + "/jobs"
	t.Cleanup(func() { jobBoardURL = prevURL })
	ctx := context.Background()
	dir := t.TempDir()
	seller := as(t, dir, "seller")
	as(t, dir, "buyer")

	_, posted, err := toolPostJob(ctx, nil, postJobInput{Title: "A logo", Description: "SVG.", IdempotencyKey: "logo", WaitSeconds: 1})
	require.NoError(t, err)
	_, hired, err := toolHireForJob(ctx, nil, hireForJobInput{JobID: posted.JobID, Seller: seller, Amount: "0.1 AETH", ExpiresIn: "24h", OnExpiry: "refund", IdempotencyKey: "h", WaitSeconds: 1})
	require.NoError(t, err)
	require.Equal(t, statusConfirmed, hired.Escrow.Status)

	as(t, dir, "seller")
	_, job, err := toolGetJob(ctx, nil, getJobInput{JobID: posted.JobID})
	require.NoError(t, err)
	require.False(t, *job.SellerProtected)
	require.Contains(t, job.Next, "go unpaid")
	_, _, err = toolDeliverJob(ctx, nil, deliverJobInput{JobID: posted.JobID, Content: "<svg/>", WaitSeconds: 1})
	requireCode(t, err, codeJobUnprotected)
	_, out, err := toolDeliverJob(ctx, nil, deliverJobInput{JobID: posted.JobID, Content: "<svg/>", AllowUnprotected: true, WaitSeconds: 1})
	require.NoError(t, err)
	require.Equal(t, statusConfirmed, out.Status)

	// Once the escrow is settled there's nothing to deliver for.
	e.expire(hired.Escrow.EscrowID)
	_, _, err = toolDeliverJob(ctx, nil, deliverJobInput{JobID: posted.JobID, Content: "v2", AllowUnprotected: true, WaitSeconds: 1})
	requireCode(t, err, codeJobNotOpen)
}

// A test wallet sells: it finds the job, offers, is hired and delivers,
// with nothing but its handle.
func TestJobs_TestWalletSells(t *testing.T) {
	s, f := newTestSandbox(t)
	f.autoInclude = true
	e := &escrowFake{fakeChain: f, nextID: 1, open: map[uint64]escrow.Escrow{}, createdBy: map[string]uint64{}, outcomes: map[uint64]*wallet.EscrowOutcome{}}
	jf := &jobsFake{escrowFake: e}
	dialChain = func() (chain, error) { return jf, nil }
	prevURL := jobBoardURL
	jobBoardURL = fakeJobHost(t).URL + "/jobs"
	t.Cleanup(func() { jobBoardURL = prevURL })
	ctx := context.Background()

	handle, _, err := s.newHandle()
	require.NoError(t, err)
	_, seller, err := s.open(handle)
	require.NoError(t, err)

	// The buyer is the local agent.
	_, posted, err := toolPostJob(ctx, nil, postJobInput{Title: "Name a cat", Description: "Three names.", Budget: "0.002 AETH", IdempotencyKey: "cat", WaitSeconds: 1})
	require.NoError(t, err)

	_, found, err := toolFindJobsSandbox(ctx, nil, sandboxFindJobsInput{WalletToken: handle})
	require.NoError(t, err)
	require.Len(t, found.Jobs, 1)
	require.Equal(t, "Name a cat", found.Jobs[0].Title)
	_, _, err = toolFindJobsSandbox(ctx, nil, sandboxFindJobsInput{findJobsInput: findJobsInput{Mine: true}})
	requireCode(t, err, codeInvalidArgument)

	_, job, err := toolGetJobSandbox(ctx, nil, sandboxGetJobInput{getJobInput: getJobInput{JobID: posted.JobID}, WalletToken: handle})
	require.NoError(t, err)
	require.Contains(t, job.Next, "offer_from_test_wallet")
	_, offered, err := toolOfferFromTestWallet(ctx, nil, offerFromTestWalletInput{WalletToken: handle, offerOnJobInput: offerOnJobInput{JobID: posted.JobID, Price: "0.002 AETH"}})
	require.NoError(t, err)
	require.Equal(t, statusConfirmed, offered.Status)
	_, _, err = toolDeliverFromTestWallet(ctx, nil, deliverFromTestWalletInput{WalletToken: handle, deliverJobInput: deliverJobInput{JobID: posted.JobID, Content: "Miso"}})
	requireCode(t, err, codeJobNotAllowed)

	_, hired, err := toolHireForJob(ctx, nil, hireForJobInput{JobID: posted.JobID, Seller: seller, Amount: "0.002 AETH", ExpiresIn: "24h", IdempotencyKey: "cat-hire", WaitSeconds: 1})
	require.NoError(t, err)
	require.Equal(t, statusConfirmed, hired.Escrow.Status)

	_, job, err = toolGetJobSandbox(ctx, nil, sandboxGetJobInput{getJobInput: getJobInput{JobID: posted.JobID}, WalletToken: handle})
	require.NoError(t, err)
	require.Equal(t, "hired_seller", job.YourRole)
	require.Contains(t, job.Next, "deliver_from_test_wallet")
	_, delivered, err := toolDeliverFromTestWallet(ctx, nil, deliverFromTestWalletInput{WalletToken: handle, deliverJobInput: deliverJobInput{JobID: posted.JobID, Content: "Miso, Pip, Juniper"}})
	require.NoError(t, err)
	require.Equal(t, statusConfirmed, delivered.Status, delivered.Message)

	_, job, err = toolGetJob(ctx, nil, getJobInput{JobID: posted.JobID})
	require.NoError(t, err)
	require.Equal(t, "Miso, Pip, Juniper", job.Deliveries[0].Content)
	require.Equal(t, seller, job.Hire.Seller)
}
