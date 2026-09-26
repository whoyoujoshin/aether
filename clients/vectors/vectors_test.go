// Package vectors produces clients/testdata/vectors.json: the exact
// bytes the Go implementation (the chain's own code) generates for keys,
// signatures, transactions and paywall messages. The TypeScript and
// Python clients must reproduce them byte for byte.
//
//	go test ./clients/vectors -update-vectors   # regenerate after an intended change
package vectors

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/cosmos-sdk/x/authz"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/directory"
	"github.com/whoyoujoshin/aether/paywall"
	"github.com/whoyoujoshin/aether/wallet"
)

var update = flag.Bool("update-vectors", false, "rewrite testdata/vectors.json")

const mnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon art"

type txVector struct {
	ChainID       string `json:"chainId"`
	AccountNumber uint64 `json:"accountNumber"`
	Sequence      uint64 `json:"sequence"`
	From          string `json:"from"`
	To            string `json:"to"`
	AmountUaeth   string `json:"amountUaeth"`
	Memo          string `json:"memo"`
	GasLimit      uint64 `json:"gasLimit"`
	BodyBytes     string `json:"bodyBytes"`
	AuthInfoBytes string `json:"authInfoBytes"`
	SignDoc       string `json:"signDoc"`
	Signature     string `json:"signature"`
	TxBytes       string `json:"txBytes"`
	TxHash        string `json:"txHash"`
}

type vectors struct {
	Key struct {
		Mnemonic string `json:"mnemonic"`
		Seed     string `json:"seed"`
		PubKey   string `json:"pubKey"`
		Address  string `json:"address"`
	} `json:"key"`
	Signature struct {
		Message   string `json:"message"`
		Signature string `json:"signature"` // deterministic (FIPS 204, empty context)
	} `json:"signature"`
	Txs     []txVector `json:"txs"`
	Prepaid struct {
		Network, PayTo, Host, Method, Path string
		Body                               string `json:"body"`
		MaxPrice                           string `json:"maxPrice"`
		Timestamp                          int64  `json:"timestamp"`
		RequestID, DepositTx               string
		Message                            string `json:"message"`
	} `json:"prepaid"`
	Queries struct {
		AccountInfoRequest string `json:"accountInfoRequest"`
		BalanceRequest     string `json:"balanceRequest"`
	} `json:"queries"`
	// Pull: the aether-pull signing message for the prepaid fields
	// (without the deposit), and the transactions and query it needs.
	Pull struct {
		Message string `json:"message"`
		// An allowance: the vector key lets grantee send up to limit,
		// only to payTo, until expiration.
		Grant struct {
			Grantee       string   `json:"grantee"`
			LimitUaeth    string   `json:"limitUaeth"`
			AllowList     []string `json:"allowList"`
			Expiration    int64    `json:"expiration"` // Unix seconds
			AccountNumber uint64   `json:"accountNumber"`
			Sequence      uint64   `json:"sequence"`
			BodyBytes     string   `json:"bodyBytes"`
			TxBytes       string   `json:"txBytes"`
			TxHash        string   `json:"txHash"`
		} `json:"grant"`
		// A collection: the vector key, as grantee, sends amount from
		// granter to payTo under the granter's allowance.
		Exec struct {
			Granter       string `json:"granter"`
			To            string `json:"to"`
			AmountUaeth   string `json:"amountUaeth"`
			Memo          string `json:"memo"`
			AccountNumber uint64 `json:"accountNumber"`
			Sequence      uint64 `json:"sequence"`
			BodyBytes     string `json:"bodyBytes"`
			TxBytes       string `json:"txBytes"`
			TxHash        string `json:"txHash"`
		} `json:"exec"`
		GrantsRequest  string `json:"grantsRequest"`  // Query/Grants for (vector key, grantee, MsgSend)
		GrantsResponse string `json:"grantsResponse"` // one SendAuthorization: 750000uaeth, allow list [payTo], expiring then
	} `json:"pull"`
	DirectoryAddress string `json:"directoryAddress"`
	Receipts         struct {
		Direct            paywall.Receipt `json:"direct"` // signed by the payee's own key
		DirectMessage     string          `json:"directMessage"`
		Delegated         paywall.Receipt `json:"delegated"` // signed by a key the payee delegated to
		DelegatedMessage  string          `json:"delegatedMessage"`
		DelegationMessage string          `json:"delegationMessage"`
	} `json:"receipts"`
}

func TestMain(m *testing.M) {
	app.SetAddressPrefixes()
	os.Exit(m.Run())
}

func build(t *testing.T) vectors {
	var v vectors
	registry := codectypes.NewInterfaceRegistry()
	mldsa.RegisterInterfaces(registry)
	w, err := wallet.NewWallet("aetherd", "test", t.TempDir(), codec.NewProtoCodec(registry))
	require.NoError(t, err)
	acc, err := w.ImportAccount("k", mnemonic)
	require.NoError(t, err)

	seed, err := mldsa.Algo.Derive()(mnemonic, "", "")
	require.NoError(t, err)
	v.Key.Mnemonic, v.Key.Seed, v.Key.PubKey, v.Key.Address = mnemonic, hex.EncodeToString(seed), hex.EncodeToString(acc.PubKey), acc.Address

	msg := []byte("aether cross-language vector")
	sig, _, err := w.SignBytes("k", msg)
	require.NoError(t, err)
	v.Signature.Message, v.Signature.Signature = hex.EncodeToString(msg), hex.EncodeToString(sig)

	to := sdk.AccAddress("vector_recipient____").String()
	for _, c := range []struct {
		accNum, seq uint64
		amount      int64
		memo        string
	}{
		{7, 3, 1_500_000, "invoice-42"},
		{0, 0, 1, ""}, // zero-valued fields must be omitted, as Go does
	} {
		signed, err := w.BuildAndSignSendTx("k", acc.Address, to, sdk.NewCoins(sdk.NewInt64Coin("uaeth", c.amount)), wallet.TxParams{
			ChainID: "aether-testnet-1", AccountNumber: c.accNum, Sequence: c.seq, GasLimit: 400_000,
			Fees: sdk.NewCoins(sdk.NewCoin("uaeth", math.ZeroInt())), Memo: c.memo,
		})
		require.NoError(t, err)
		var raw txtypes.TxRaw
		require.NoError(t, raw.Unmarshal(signed.Bytes))
		doc, err := (&txtypes.SignDoc{BodyBytes: raw.BodyBytes, AuthInfoBytes: raw.AuthInfoBytes, ChainId: "aether-testnet-1", AccountNumber: c.accNum}).Marshal()
		require.NoError(t, err)
		v.Txs = append(v.Txs, txVector{
			ChainID: "aether-testnet-1", AccountNumber: c.accNum, Sequence: c.seq, From: acc.Address, To: to,
			AmountUaeth: math.NewInt(c.amount).String(), Memo: c.memo, GasLimit: 400_000,
			BodyBytes: hex.EncodeToString(raw.BodyBytes), AuthInfoBytes: hex.EncodeToString(raw.AuthInfoBytes),
			SignDoc: hex.EncodeToString(doc), Signature: hex.EncodeToString(raw.Signatures[0]),
			TxBytes: hex.EncodeToString(signed.Bytes), TxHash: wallet.TxHash(signed),
		})
	}

	f := paywall.RequestFields{
		Network: "aether-testnet-1", PayTo: to, Host: "api.example:8402", Method: "POST", Path: "/v1/translate",
		Body: []byte(`{"text":"hello"}`), MaxPrice: math.NewInt(20_000), Timestamp: 1_790_000_000, RequestID: "req-1", DepositTx: "ABCDEF",
	}
	p := &v.Prepaid
	p.Network, p.PayTo, p.Host, p.Method, p.Path = f.Network, f.PayTo, f.Host, f.Method, f.Path
	p.Body, p.MaxPrice, p.Timestamp, p.RequestID, p.DepositTx = string(f.Body), f.MaxPrice.String(), f.Timestamp, f.RequestID, f.DepositTx
	p.Message = hex.EncodeToString(paywall.SigningMessage(f))

	ai, err := (&authtypes.QueryAccountInfoRequest{Address: acc.Address}).Marshal()
	require.NoError(t, err)
	bal, err := (&banktypes.QueryBalanceRequest{Address: acc.Address, Denom: "uaeth"}).Marshal()
	require.NoError(t, err)
	v.Queries.AccountInfoRequest, v.Queries.BalanceRequest = hex.EncodeToString(ai), hex.EncodeToString(bal)
	v.DirectoryAddress = directory.Address()

	// Pull.
	f.DepositTx = ""
	v.Pull.Message = hex.EncodeToString(paywall.PullSigningMessage(f))
	grantee := sdk.AccAddress("vector_collector____").String()
	exp := time.Unix(1_790_600_000, 0).UTC()
	g := &v.Pull.Grant
	g.Grantee, g.LimitUaeth, g.AllowList, g.Expiration, g.AccountNumber, g.Sequence = grantee, "750000", []string{to}, exp.Unix(), 7, 4
	grantMsg, err := wallet.SendGrantMsg(acc.Address, grantee, sdk.NewCoins(sdk.NewInt64Coin("uaeth", 750_000)), []string{to}, exp)
	require.NoError(t, err)
	g.BodyBytes, g.TxBytes, g.TxHash = signedMsgTx(t, w, grantMsg, 7, 4, "")
	e := &v.Pull.Exec
	e.Granter, e.To, e.AmountUaeth, e.Memo, e.AccountNumber, e.Sequence = sdk.AccAddress("vector_buyer________").String(), to, "30000", "x402-pull:0123456789abcdef", 9, 2
	execMsg, err := wallet.ExecSendMsg(acc.Address, e.Granter, to, sdk.NewCoins(sdk.NewInt64Coin("uaeth", 30_000)))
	require.NoError(t, err)
	e.BodyBytes, e.TxBytes, e.TxHash = signedMsgTx(t, w, execMsg, 9, 2, e.Memo)
	gr, err := (&authz.QueryGrantsRequest{Granter: acc.Address, Grantee: grantee, MsgTypeUrl: sdk.MsgTypeURL(&banktypes.MsgSend{})}).Marshal()
	require.NoError(t, err)
	v.Pull.GrantsRequest = hex.EncodeToString(gr)
	respResp := &authz.QueryGrantsResponse{Grants: []*authz.Grant{{Authorization: grantMsg.Grant.Authorization, Expiration: &exp}}}
	rr, err := respResp.Marshal()
	require.NoError(t, err)
	v.Pull.GrantsResponse = hex.EncodeToString(rr)

	// Receipts: signed by the vector key (the payee), and by a second key
	// the payee delegated receipts to.
	k2, err := w.ImportAccount("k2", "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about")
	require.NoError(t, err)
	signWith := func(name string) paywall.Signer {
		return func(msg []byte) ([]byte, []byte, error) { return w.SignBytes(name, msg) }
	}
	sum := func(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
	sign := func(r paywall.Receipt, name string) paywall.Receipt {
		sig, pub, err := signWith(name)(paywall.ReceiptSigningMessage(r))
		require.NoError(t, err)
		r.Signer, r.Signature = base64.StdEncoding.EncodeToString(pub), base64.StdEncoding.EncodeToString(sig)
		require.NoError(t, r.Verify())
		return r
	}
	base := paywall.Receipt{
		X402Version: 1, Network: "aether-testnet-1", PayTo: acc.Address, Payer: to, Scheme: "aether-memo",
		Payment: "8A679ACE95C8D1F0E3B2A4C6D8E0F1A3B5C7D9E1F3A5B7C9D1E3F5A7B9C1D3E5", Amount: "20000", Method: "POST",
		Host: "api.example:8402", Path: "/v1/translate", RequestHash: sum(`{"text":"hello"}`), Status: 200,
		ResponseHash: sum(`{"text":"hallo"}`), At: 1_790_000_100,
	}
	v.Receipts.Direct = sign(base, "k")
	v.Receipts.DirectMessage = hex.EncodeToString(paywall.ReceiptSigningMessage(base))
	d, err := paywall.NewReceiptDelegation(acc.Address, k2.Address, 1_800_000_000, signWith("k"))
	require.NoError(t, err)
	v.Receipts.DelegationMessage = hex.EncodeToString(paywall.DelegationSigningMessage(d.PayTo, d.Signer, d.Expires))
	delegated := base
	delegated.Scheme, delegated.Payment, delegated.ResponseHash, delegated.Delegation = "aether-prepaid", "req-1", "", d
	v.Receipts.Delegated = sign(delegated, "k2")
	v.Receipts.DelegatedMessage = hex.EncodeToString(paywall.ReceiptSigningMessage(delegated))
	return v
}

// signedMsgTx signs msg from the vector key, returning hex body bytes,
// hex tx bytes and the hash.
func signedMsgTx(t *testing.T, w *wallet.Wallet, msg sdk.Msg, accNum, seq uint64, memo string) (string, string, string) {
	signed, err := w.BuildAndSignMsgTx("k", msg, wallet.TxParams{
		ChainID: "aether-testnet-1", AccountNumber: accNum, Sequence: seq, GasLimit: 400_000,
		Fees: sdk.NewCoins(sdk.NewCoin("uaeth", math.ZeroInt())), Memo: memo,
	})
	require.NoError(t, err)
	var raw txtypes.TxRaw
	require.NoError(t, raw.Unmarshal(signed.Bytes))
	return hex.EncodeToString(raw.BodyBytes), hex.EncodeToString(signed.Bytes), wallet.TxHash(signed)
}

func TestVectors(t *testing.T) {
	v := build(t)
	bz, err := json.MarshalIndent(v, "", "  ")
	require.NoError(t, err)
	path := filepath.Join("..", "testdata", "vectors.json")
	if *update {
		require.NoError(t, os.WriteFile(path, append(bz, '\n'), 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "run: go test ./clients/vectors -update-vectors")
	require.JSONEq(t, string(want), string(bz), "the Go implementation no longer produces the vectors the clients are tested against")
}
