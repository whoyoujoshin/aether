package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/paywall"
)

// mandateSeller is a paid API that requires owners' spending mandates.
func mandateSeller(t *testing.T, f *fakeChain, priceUaeth int64) (*httptest.Server, *atomic.Int32, *atomic.Value) {
	t.Helper()
	pw, err := paywall.New(paywall.Config{
		PayTo: sellerAddr(), Price: math.NewInt(priceUaeth), Network: chainID, Lookup: f.lookup,
		Mandates: &paywall.MandateConfig{Required: true},
	})
	require.NoError(t, err)
	var served atomic.Int32
	var owner atomic.Value
	srv := httptest.NewServer(pw.MandateGate(pw.Middleware(pw.MandatePayerCheck(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served.Add(1)
		owner.Store(r.Header.Get(paywall.HeaderMandateOwner))
		fmt.Fprint(w, `{"answer":42}`)
	})))))
	t.Cleanup(srv.Close)
	return srv, &served, &owner
}

func resetMandate(t *testing.T) {
	reset := func() {
		mandate, mandateHdr, mandateFile, mandateAgent = nil, "", "", ""
		mandateAgentOnce = sync.Once{}
	}
	reset()
	t.Cleanup(reset)
}

// writeMandate signs a mandate for this agent with a fresh owner key and
// writes it where loadMandate reads it.
func writeMandate(t *testing.T, edit func(*paywall.Mandate)) (path, owner string) {
	t.Helper()
	sk, err := mldsa.GenPrivKey()
	require.NoError(t, err)
	owner = sdk.AccAddress(sk.PubKey().Address()).String()
	m := paywall.Mandate{Network: chainID, Owner: owner, Agent: payingAccount(), MaxPerPayment: "50000",
		NotBefore: time.Now().Add(-time.Minute).Unix(), Expires: time.Now().Add(time.Hour).Unix(), Purpose: "tests"}
	if edit != nil {
		edit(&m)
	}
	signed, err := paywall.NewMandate(m, func(msg []byte) ([]byte, []byte, error) {
		sig, err := sk.Sign(msg)
		return sig, sk.PubKey().Bytes(), err
	})
	require.NoError(t, err)
	bz, _ := json.Marshal(signed)
	path = filepath.Join(t.TempDir(), "mandate.json")
	require.NoError(t, os.WriteFile(path, bz, 0o600))
	return path, owner
}

func TestMandate_SellerRequiresOne(t *testing.T) {
	f := setupAgent(t)
	f.autoInclude = true
	resetMandate(t)
	srv, served, seen := mandateSeller(t, f, 20_000)

	// No mandate: refused before anything is paid.
	_, err := fetch(t, srv.URL+"/", "0.05 AETH", "m-1", 5)
	requireCode(t, err, codeMandateRefused)
	require.Contains(t, err.Error(), "MANDATE_REQUIRED")
	require.Empty(t, f.broadcasts)

	// The owner's mandate: paid and served, and the seller learns the owner.
	path, owner := writeMandate(t, nil)
	require.NoError(t, loadMandate(path))
	out, err := fetch(t, srv.URL+"/", "0.05 AETH", "m-2", 5)
	require.NoError(t, err)
	require.Equal(t, "paid", out.Status)
	require.Equal(t, `{"answer":42}`, out.Body)
	require.Equal(t, int32(1), served.Load())
	require.Equal(t, owner, seen.Load())

	_, got, err := toolGetMandate(context.Background(), nil, struct{}{})
	require.NoError(t, err)
	require.True(t, got.Present)
	require.True(t, got.Sent)
	require.Equal(t, owner, got.Owner)
	require.Equal(t, "50000", got.MaxPerPayment)

	// A mandate below the price: refused, nothing more paid.
	path, _ = writeMandate(t, func(m *paywall.Mandate) { m.MaxPerPayment = "10000" })
	require.NoError(t, loadMandate(path))
	_, err = fetch(t, srv.URL+"/", "0.05 AETH", "m-3", 5)
	requireCode(t, err, codeMandateRefused)
	require.Contains(t, err.Error(), "at most 10000")
	require.Len(t, f.broadcasts, 1)
}

func TestMandate_NotSentWhenItNamesAnotherAgent(t *testing.T) {
	setupAgent(t)
	resetMandate(t)
	path, _ := writeMandate(t, func(m *paywall.Mandate) { m.Agent = sellerAddr() })
	require.NoError(t, loadMandate(path))
	require.Empty(t, mandateHeader())
	_, got, err := toolGetMandate(context.Background(), nil, struct{}{})
	require.NoError(t, err)
	require.False(t, got.Sent)
	require.Contains(t, got.Message, "isn't sent")
}

func TestMandate_LoadRefusesBadFiles(t *testing.T) {
	setupAgent(t)
	resetMandate(t)
	path, _ := writeMandate(t, func(m *paywall.Mandate) { m.Network = "aether-devnet" })
	require.ErrorContains(t, loadMandate(path), "not "+chainID)

	path, _ = writeMandate(t, nil)
	var m paywall.Mandate
	bz, _ := os.ReadFile(path)
	require.NoError(t, json.Unmarshal(bz, &m))
	m.MaxPerPayment = "99999999"
	bz, _ = json.Marshal(m)
	require.NoError(t, os.WriteFile(path, bz, 0o600))
	require.ErrorContains(t, loadMandate(path), "bad signature")

	_, got, err := toolGetMandate(context.Background(), nil, struct{}{})
	require.NoError(t, err)
	require.False(t, got.Present)
}

func TestMandateCommand_SignsWithTheOwnersKeyring(t *testing.T) {
	setupAgent(t)
	resetMandate(t)
	agent := payingAccount()
	out := filepath.Join(t.TempDir(), "m.json")
	// The agent's own keyring stands in for the owner's here.
	require.NoError(t, runMandateCommand([]string{
		"--owner-key", accountName, "--keyring-dir", keyringDir, "--agent", agent,
		"--max-per-payment", "0.05 AETH", "--max-total", "2 USDC", "--valid-for", "1h", "--purpose", "test agent", "--out", out,
	}))
	require.NoError(t, loadMandate(out))
	require.Equal(t, agent, mandate.Owner)
	require.Equal(t, "50000", mandate.MaxPerPayment)
	require.Equal(t, "2000000ibc/064D82A67318DD30F54A4E17B6E487C2864E8B075A5871D3ACAD7A7129F32C5C", mandate.MaxTotal)
	require.InDelta(t, time.Now().Add(time.Hour).Unix(), mandate.Expires, 5)

	require.ErrorContains(t, runMandateCommand([]string{"--owner-key", accountName, "--keyring-dir", keyringDir, "--agent", agent,
		"--max-per-payment", "50000"}), "unit")
}
