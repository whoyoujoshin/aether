package paywall

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"
)

var mandateNow = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

func signedMandate(t *testing.T, owner agentKey, edit func(*Mandate)) *Mandate {
	t.Helper()
	m := Mandate{
		Network: "aether-testnet-1", Owner: owner.address, Agent: "aether1agent",
		MaxPerPayment: "10000", MaxTotal: "1000000", NotBefore: mandateNow.Add(-time.Hour).Unix(),
		Expires: mandateNow.Add(24 * time.Hour).Unix(), Purpose: "research assistant",
	}
	if edit != nil {
		edit(&m)
	}
	out, err := NewMandate(m, owner.sign)
	require.NoError(t, err)
	return out
}

func TestMandate_SignVerifyAndRoundTrip(t *testing.T) {
	owner := newAgentKey(t)
	m := signedMandate(t, owner, nil)
	require.Len(t, m.ID, 32)
	require.Equal(t, MandateVersion, m.Version)
	require.NoError(t, m.Verify())

	header, err := EncodeMandate(m)
	require.NoError(t, err)
	back, err := DecodeMandate(header)
	require.NoError(t, err)
	require.Equal(t, m, back)
	require.NoError(t, back.Verify())

	// Any signed field changed breaks the signature.
	for name, edit := range map[string]func(*Mandate){
		"agent":   func(m *Mandate) { m.Agent = "aether1other" },
		"max":     func(m *Mandate) { m.MaxPerPayment = "99999" },
		"payTo":   func(m *Mandate) { m.PayTo = []string{"aether1x"} },
		"expires": func(m *Mandate) { m.Expires++ },
		"purpose": func(m *Mandate) { m.Purpose = "anything" },
	} {
		c := *back
		edit(&c)
		require.ErrorContains(t, c.Verify(), "bad signature", name)
	}
	// Someone else's key can't sign for the owner.
	other := newAgentKey(t)
	forged := *back
	forged.OwnerPubKey = signedMandate(t, other, nil).OwnerPubKey
	require.ErrorContains(t, forged.Verify(), "bad signature")
	claims := signedMandate(t, other, func(m *Mandate) { m.Owner = back.Owner })
	require.ErrorContains(t, claims.Verify(), "not signed by the owner's key")
}

func TestMandate_RefusesWhatCantBeSigned(t *testing.T) {
	owner := newAgentKey(t)
	for name, edit := range map[string]func(*Mandate){
		"line break":  func(m *Mandate) { m.Purpose = "a\nb" },
		"comma payee": func(m *Mandate) { m.PayTo = []string{"aether1a,aether1b"} },
		"no agent":    func(m *Mandate) { m.Agent = "" },
		"bad amount":  func(m *Mandate) { m.MaxPerPayment = "1 AETH" },
		"backwards":   func(m *Mandate) { m.Expires = m.NotBefore },
	} {
		m := Mandate{Network: "aether-testnet-1", Owner: owner.address, Agent: "aether1agent", MaxPerPayment: "10000",
			NotBefore: 1, Expires: 2}
		edit(&m)
		_, err := NewMandate(m, owner.sign)
		require.Error(t, err, name)
	}
}

func TestCheckMandate(t *testing.T) {
	owner := newAgentKey(t)
	usdc := "ibc/064D82A67318DD30F54A4E17B6E487C2864E8B075A5871D3ACAD7A7129F32C5C"
	ok := MandateExpectation{Network: "aether-testnet-1", PayTo: "aether1seller", Payer: "aether1agent",
		Amount: math.NewInt(500), Denom: Asset, Now: mandateNow.Unix()}
	require.NoError(t, CheckMandate(signedMandate(t, owner, nil), ok))

	for name, c := range map[string]struct {
		edit func(*Mandate)
		want func(*MandateExpectation)
		err  string
	}{
		"network":       {nil, func(e *MandateExpectation) { e.Network = "aether-devnet" }, "network"},
		"too early":     {nil, func(e *MandateExpectation) { e.Now = mandateNow.Add(-2 * time.Hour).Unix() }, "not valid yet"},
		"expired":       {nil, func(e *MandateExpectation) { e.Now = mandateNow.Add(48 * time.Hour).Unix() }, "expired"},
		"another payer": {nil, func(e *MandateExpectation) { e.Payer = "aether1thief" }, "but aether1thief paid"},
		"payee":         {func(m *Mandate) { m.PayTo = []string{"aether1other"} }, nil, "payee"},
		"over max":      {nil, func(e *MandateExpectation) { e.Amount = math.NewInt(10_001) }, "at most 10000"},
		"asset":         {nil, func(e *MandateExpectation) { e.Denom = usdc }, "limits payments in uaeth"},
		"owner list":    {nil, func(e *MandateExpectation) { e.Owners = []string{"aether1someoneelse"} }, "doesn't accept"},
	} {
		m := signedMandate(t, owner, c.edit)
		e := ok
		if c.want != nil {
			c.want(&e)
		}
		require.ErrorContains(t, CheckMandate(m, e), c.err, name)
	}

	// Before payment the payer isn't known: everything else still counts.
	e := ok
	e.Payer = ""
	require.NoError(t, CheckMandate(signedMandate(t, owner, nil), e))
	// The payee listed, and a USDC limit for a USDC price.
	m := signedMandate(t, owner, func(m *Mandate) { m.PayTo = []string{"aether1x", "aether1seller"}; m.MaxPerPayment = "2000" + usdc })
	e = ok
	e.Denom, e.Amount = usdc, math.NewInt(2000)
	require.NoError(t, CheckMandate(m, e))
}

func TestMandateGates(t *testing.T) {
	owner, seller := newAgentKey(t), newAgentKey(t)
	p, err := New(Config{PayTo: seller.address, Price: math.NewInt(500), Network: "aether-testnet-1",
		Lookup: (&fakeLedger{}).lookup, Now: func() time.Time { return mandateNow },
		Mandates: &MandateConfig{Required: true}})
	require.NoError(t, err)

	var gotOwner string
	served := 0
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served++
		gotOwner = r.Header.Get(HeaderMandateOwner)
	})
	// Stands in for the paywall middleware: payment settled by aether1agent.
	paid := func(payer string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s, _ := EncodeHeader(SettlementResponse{Success: true, Transaction: "AB", Network: "aether-testnet-1", Payer: payer})
			w.Header().Set(HeaderPaymentResponse, s)
			p.MandatePayerCheck(upstream).ServeHTTP(w, r)
		})
	}
	do := func(h http.Handler, mandate string, spoofOwner bool) (int, map[string]string) {
		req := httptest.NewRequest("GET", "/api", nil)
		if mandate != "" {
			req.Header.Set(HeaderMandate, mandate)
		}
		if spoofOwner {
			req.Header.Set(HeaderMandateOwner, "aether1spoofed")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	good, err := EncodeMandate(signedMandate(t, owner, nil))
	require.NoError(t, err)

	code, out := do(p.MandateGate(paid("aether1agent")), "", false)
	require.Equal(t, http.StatusForbidden, code)
	require.Equal(t, "MANDATE_REQUIRED", out["error"])

	cheap, _ := EncodeMandate(signedMandate(t, owner, func(m *Mandate) { m.MaxPerPayment = "100" }))
	code, out = do(p.MandateGate(paid("aether1agent")), cheap, false)
	require.Equal(t, http.StatusForbidden, code)
	require.Equal(t, "MANDATE_REFUSED", out["error"])
	require.Contains(t, out["message"], "at most 100")
	require.Zero(t, served, "refused before payment")

	code, _ = do(p.MandateGate(paid("aether1agent")), good, true)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, 1, served)
	require.Equal(t, owner.address, gotOwner, "the checked owner, never a caller's header")

	code, out = do(p.MandateGate(paid("aether1thief")), good, false)
	require.Equal(t, http.StatusForbidden, code)
	require.Contains(t, out["message"], "but aether1thief paid")
	require.Equal(t, 1, served)

	code, out = do(p.MandateGate(paid("aether1agent")), "%%%", false)
	require.Equal(t, http.StatusForbidden, code)
	require.Contains(t, out["message"], "base64")

	// Not required: no mandate passes, a bad one is still refused.
	p.cfg.Mandates.Required = false
	code, _ = do(p.MandateGate(paid("aether1agent")), "", true)
	require.Equal(t, http.StatusOK, code)
	require.Empty(t, gotOwner, "no mandate, no owner header")
	code, _ = do(p.MandateGate(paid("aether1agent")), cheap, false)
	require.Equal(t, http.StatusForbidden, code)
}
