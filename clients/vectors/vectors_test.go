// Package vectors produces clients/testdata/vectors.json: the exact
// bytes the Go implementation (the chain's own code) generates for keys,
// signatures, transactions and paywall messages. The TypeScript and
// Python clients must reproduce them byte for byte.
//
//	go test ./clients/vectors -update-vectors   # regenerate after an intended change
package vectors

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
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
	DirectoryAddress string `json:"directoryAddress"`
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
	return v
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
