package wallet_test

import (
	"testing"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/wallet"
)

func testCodec() codec.Codec {
	registry := codectypes.NewInterfaceRegistry()
	mldsa.RegisterInterfaces(registry)
	return codec.NewProtoCodec(registry)
}

func TestCopyTestKey(t *testing.T) {
	cdc := testCodec()
	srcDir, dstDir := t.TempDir(), t.TempDir()
	src, err := wallet.NewWallet("aetherd", "test", srcDir, cdc)
	require.NoError(t, err)
	orig, _, err := src.CreateAccount("mine")
	require.NoError(t, err)
	_, _, err = src.CreateAccount("other")
	require.NoError(t, err)

	got, err := wallet.CopyTestKey(srcDir, dstDir, "mine", cdc)
	require.NoError(t, err)
	require.Equal(t, orig.Address, got.Address)

	dst, err := wallet.NewWallet("aetherd", "test", dstDir, cdc)
	require.NoError(t, err)
	list, err := dst.ListAccounts()
	require.NoError(t, err)
	require.Len(t, list, 1, "only the chosen key comes over")

	// The copy signs with the original key.
	_, pub, err := dst.SignBytes("mine", []byte("hello"))
	require.NoError(t, err)
	require.Equal(t, orig.PubKey, pub)

	_, err = wallet.CopyTestKey(srcDir, dstDir, "mine", cdc)
	require.ErrorContains(t, err, "already has")
	_, err = wallet.CopyTestKey(srcDir, dstDir, "../other", cdc)
	require.ErrorContains(t, err, "invalid account name")
	_, err = wallet.CopyTestKey(srcDir, dstDir, "missing", cdc)
	require.Error(t, err)
}
