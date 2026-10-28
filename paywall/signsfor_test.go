package paywall

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/wallet"
)

// storedKeys is the keys on accounts, as the chain holds them; an account
// it doesn't list has none.
type storedKeys map[string][]byte

func (s storedKeys) AccountPubKey(_ context.Context, a string) ([]byte, error) { return s[a], nil }

type brokenKeys struct{}

func (brokenKeys) AccountPubKey(context.Context, string) ([]byte, error) {
	return nil, errors.New("node down")
}

func rotatedTo(account string, k agentKey) storedKeys {
	return storedKeys{account: k.sk.PubKey().Bytes()}
}

// After an owner rotates its key, mandates its retired key signed stop
// verifying, and its new key's do. With no chain access (Verify) the old
// hash-only rule is unchanged.
func TestMandate_FollowsTheOwnersKeyRotation(t *testing.T) {
	owner, next := newAgentKey(t), newAgentKey(t)
	ctx := context.Background()
	old := signedMandate(t, owner, nil)
	byNew := signedMandate(t, next, func(m *Mandate) { m.Owner = owner.address })

	require.NoError(t, old.VerifyWith(ctx, storedKeys{}), "never rotated: its own key")
	require.NoError(t, old.VerifyWith(ctx, rotatedTo(owner.address, owner)))
	require.ErrorContains(t, byNew.VerifyWith(ctx, storedKeys{}), "not signed by the owner's key")

	rotated := rotatedTo(owner.address, next)
	require.ErrorContains(t, old.VerifyWith(ctx, rotated), "not signed by the owner's key", "the retired key")
	require.NoError(t, byNew.VerifyWith(ctx, rotated))
	require.Error(t, old.VerifyWith(ctx, brokenKeys{}), "can't tell: refused")

	want := MandateExpectation{Network: "aether-testnet-1", PayTo: "aether1payee", Payer: "aether1agent", Amount: math.NewInt(1), Denom: Asset, Now: mandateNow.Unix()}
	require.Error(t, CheckMandateWith(ctx, rotated, old, want))
	require.NoError(t, CheckMandateWith(ctx, rotated, byNew, want))
}

// A receipt signed by a payee's retired key no longer verifies, nor does
// a delegation it signed; the new key's do.
func TestReceipt_FollowsThePayeesKeyRotation(t *testing.T) {
	h := newReceiptHarness(t, payeeSigns)
	resp, body, hash := h.payMemo("/forecast", "")
	r, err := DecodeReceipt(resp.Header.Get(HeaderReceipt))
	require.NoError(t, err)
	ctx := context.Background()
	want := h.expect("/forecast", hash, "", body, 200)
	require.NoError(t, CheckReceiptWith(ctx, storedKeys{}, r, want))

	next := newAgentKey(t)
	rotated := rotatedTo(h.payee.address, next)
	require.ErrorContains(t, CheckReceiptWith(ctx, rotated, r, want), "isn't the payee's")

	// The new key delegates to a receipt key; the retired key can't.
	receiptKey := newAgentKey(t)
	expires := time.Now().Add(time.Hour).Unix()
	byNew, err := NewReceiptDelegation(h.payee.address, receiptKey.address, expires, next.sign)
	require.NoError(t, err)
	byOld, err := NewReceiptDelegation(h.payee.address, receiptKey.address, expires, h.payee.sign)
	require.NoError(t, err)
	for _, c := range []struct {
		d  *ReceiptDelegation
		ok bool
	}{{byNew, true}, {byOld, false}} {
		rr := *r
		rr.Delegation = c.d
		sig, pub, err := receiptKey.sign(ReceiptSigningMessage(rr))
		require.NoError(t, err)
		rr.Signer, rr.Signature = b64(pub), b64(sig)
		if c.ok {
			require.NoError(t, rr.VerifyWith(ctx, rotated))
		} else {
			require.ErrorContains(t, rr.VerifyWith(ctx, rotated), "not signed by the payee's key")
		}
	}
}

// A paywall whose payee rotated refuses to start signing receipts with
// the retired key, and starts with the new one.
func TestReceipt_StartCheckFollowsTheStoredKey(t *testing.T) {
	payee, next := newAgentKey(t), newAgentKey(t)
	cfg := func(sign Signer) Config {
		return Config{PayTo: payee.address, Price: math.NewInt(1), Network: "aether-testnet-1",
			Lookup: (&fakeLedger{}).lookup, AccountKeys: rotatedTo(payee.address, next), Receipts: &ReceiptConfig{Sign: sign}}
	}
	_, err := New(cfg(payee.sign))
	require.ErrorContains(t, err, "receipts wouldn't verify")
	_, err = New(cfg(next.sign))
	require.NoError(t, err)
}

// Prepaid sign-in follows the account's stored key: the retired key is
// refused, the new one accepted, and a node that can't answer refuses
// with a retryable code rather than letting the request through.
func TestPrepaid_SignInFollowsTheStoredKey(t *testing.T) {
	payTo := seller()
	h := newPrepaidHarness(t, payTo, "")
	owner, next := newAgentKey(t), newAgentKey(t)
	h.deposit("D1", owner.address, 50_000)
	resp, _ := h.do(signedReq{key: owner, path: "/api/x", deposit: "D1", requestID: "r1"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	h.setAccountKeys(t, rotatedTo(owner.address, next))
	refused := h.refused(signedReq{key: owner, path: "/api/x", requestID: "r2"}, ErrInvalidSignature)
	require.Contains(t, refused.Message, "does not sign for account")
	asNew := agentKey{sk: next.sk, address: owner.address}
	resp, _ = h.do(signedReq{key: asNew, path: "/api/x", requestID: "r3"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	h.setAccountKeys(t, brokenKeys{})
	h.refused(signedReq{key: asNew, path: "/api/x", requestID: "r4"}, ErrNotConfirmed)
}

// setAccountKeys changes what the paywall reads as accounts' keys,
// between requests.
func (h *prepaidHarness) setAccountKeys(t *testing.T, keys wallet.AccountKeys) {
	t.Helper()
	h.pw.cfg.AccountKeys = keys
}

// The gate before payment and the check after it both read the owner's
// stored key: a mandate signed by the retired key is refused at the gate
// and after payment, one by the new key is served.
func TestMandateGates_FollowTheOwnersKeyRotation(t *testing.T) {
	owner, next, seller := newAgentKey(t), newAgentKey(t), newAgentKey(t)
	p, err := New(Config{PayTo: seller.address, Price: math.NewInt(500), Network: "aether-testnet-1",
		Lookup: (&fakeLedger{}).lookup, Now: func() time.Time { return mandateNow },
		AccountKeys: rotatedTo(owner.address, next), Mandates: &MandateConfig{Required: true}})
	require.NoError(t, err)
	served := 0
	upstream := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { served++ })
	paid := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, _ := EncodeHeader(SettlementResponse{Success: true, Transaction: "AB", Network: "aether-testnet-1", Payer: "aether1agent"})
		w.Header().Set(HeaderPaymentResponse, s)
		p.MandatePayerCheck(upstream).ServeHTTP(w, r)
	})
	do := func(h http.Handler, m *Mandate) int {
		hdr, err := EncodeMandate(m)
		require.NoError(t, err)
		req := httptest.NewRequest("GET", "/api", nil)
		req.Header.Set(HeaderMandate, hdr)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	old := signedMandate(t, owner, nil)
	byNew := signedMandate(t, next, func(m *Mandate) { m.Owner = owner.address })

	require.Equal(t, http.StatusForbidden, do(p.MandateGate(paid), old), "the gate")
	require.Equal(t, http.StatusForbidden, do(paid, old), "the check after payment")
	require.Zero(t, served)
	require.Equal(t, http.StatusOK, do(p.MandateGate(paid), byNew))
	require.Equal(t, 1, served)
}
