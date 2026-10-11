package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/x402"
)

type downChain struct{ err error }

func (c downChain) Account(context.Context, string) (uint64, uint64, error) { return 0, 0, c.err }
func (c downChain) LatestHeight(context.Context) (int64, error)             { return 10, c.err }
func (c downChain) Simulate(context.Context, []byte) (uint64, error)        { return 0, c.err }
func (c downChain) Broadcast(context.Context, []byte) (string, uint32, string, error) {
	return "", 0, "", c.err
}
func (c downChain) TxResult(context.Context, string) (bool, uint32, string, error) {
	return false, 0, "", c.err
}

func do(t *testing.T, h http.Handler, method, path, body string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	return rec.Code, out
}

func TestSupportedNamesAetherExact(t *testing.T) {
	h := newHandler(x402.NewFacilitator("aether-testnet-1", downChain{}))
	code, out := do(t, h, "GET", "/supported", "")
	require.Equal(t, 200, code)
	kinds := out["kinds"].([]any)
	require.Len(t, kinds, 1)
	k := kinds[0].(map[string]any)
	require.Equal(t, float64(2), k["x402Version"])
	require.Equal(t, "exact", k["scheme"])
	require.Equal(t, "cosmos:aether-testnet-1", k["network"])
	require.NotNil(t, out["signers"], "signers is required by the spec, even empty")
}

func TestVerifyAndSettleAnswerWithReasons(t *testing.T) {
	h := newHandler(x402.NewFacilitator("aether-testnet-1", downChain{}))

	code, out := do(t, h, "POST", "/verify", "{not json")
	require.Equal(t, 400, code)
	require.Equal(t, x402.ReasonPayload, out["invalidReason"])
	require.Equal(t, false, out["isValid"])

	big := `{"x402Version":2,"paymentPayload":{"payload":"` + strings.Repeat("A", maxBody) + `"}}`
	code, _ = do(t, h, "POST", "/settle", big)
	require.Equal(t, 400, code)

	v1 := `{"x402Version":1,"paymentPayload":{"x402Version":1,"accepted":{},"payload":{}},"paymentRequirements":{}}`
	code, out = do(t, h, "POST", "/verify", v1)
	require.Equal(t, 200, code)
	require.Equal(t, x402.ReasonVersion, out["invalidReason"])

	code, out = do(t, h, "POST", "/settle", v1)
	require.Equal(t, 200, code)
	require.Equal(t, false, out["success"])
	require.Equal(t, x402.ReasonVersion, out["errorReason"])
	require.Equal(t, "cosmos:aether-testnet-1", out["network"])

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/verify", nil))
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestHealthzReportsTheNode(t *testing.T) {
	code, _ := do(t, newHandler(x402.NewFacilitator("aether-testnet-1", downChain{})), "GET", "/healthz", "")
	require.Equal(t, 200, code)
	code, out := do(t, newHandler(x402.NewFacilitator("aether-testnet-1", downChain{err: errors.New("down")})), "GET", "/healthz", "")
	require.Equal(t, 503, code)
	require.Equal(t, false, out["ok"])
}
