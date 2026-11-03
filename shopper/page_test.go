package shopper

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestApprovalPageDoesNotSignWithoutAKey(t *testing.T) {
	dir := t.TempDir()
	a := Approval{
		ID: "abc", QuoteID: "q", PayTo: "aether1x", Amount: "1", Asset: "uaeth",
		PriceDisplay: "0.000001 AETH", Name: "Hello", Expires: time.Now().Add(time.Hour),
	}
	require.NoError(t, saveApproval(dir, a))
	h := ApprovalPage{Dir: dir}.Handler()
	srv := httptest.NewServer(h)
	defer srv.Close()
	res, err := srv.Client().Get(srv.URL + "/approve/abc")
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	body := mustRead(t, res)
	require.Contains(t, body, "Allow this buy?")
	require.Contains(t, body, "0.000001 AETH")
	require.NotContains(t, body, "mnemonic")

	post, err := srv.Client().Post(srv.URL+"/approve/abc", "application/x-www-form-urlencoded", strings.NewReader("decision=approve"))
	require.NoError(t, err)
	defer post.Body.Close()
	require.Equal(t, http.StatusForbidden, post.StatusCode)
}

func TestLoopback(t *testing.T) {
	require.True(t, Loopback("127.0.0.1:8470"))
	require.True(t, Loopback("localhost:8470"))
	require.False(t, Loopback("0.0.0.0:8470"))
	require.False(t, Loopback("157.245.252.221:8470"))
}

func mustRead(t *testing.T, res *http.Response) string {
	t.Helper()
	buf, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return string(buf)
}
