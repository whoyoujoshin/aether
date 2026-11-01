package relayer

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/whoyoujoshin/aether/crypto/ethsecp256k1"
	"github.com/whoyoujoshin/aether/wallet"
)

// OpenCounterpartyKeyring opens the keyring holding the key that signs on
// the other chain. Besides ordinary secp256k1 keys it reads and creates
// eth_secp256k1 keys (Injective's), so a keyring made by injectived
// (`injectived keys add --keyring-backend test --home <dir>`) works as is.
// It registers that key type on cdc's interface registry. The file
// backend reads its passphrase from in.
func OpenCounterpartyKeyring(backend, dir string, in io.Reader, cdc codec.Codec) (keyring.Keyring, error) {
	ethsecp256k1.RegisterInterfaces(cdc.InterfaceRegistry())
	return wallet.NewKeyring("counterpartyd", backend, dir, in, cdc, func(o *keyring.Options) {
		o.SupportedAlgos = keyring.SigningAlgoList{hd.Secp256k1, ethsecp256k1.Algo}
	})
}

// accountInfoRetriever looks an account up with auth's AccountInfo query,
// which answers with a plain BaseAccount whatever type the chain stores.
// The SDK's own retriever unpacks the chain's account type, and
// Injective's (EthAccount) isn't one this binary knows.
type accountInfoRetriever struct{}

var _ client.AccountRetriever = accountInfoRetriever{}

func (r accountInfoRetriever) GetAccount(clientCtx client.Context, addr sdk.AccAddress) (client.Account, error) {
	acc, _, err := r.GetAccountWithHeight(clientCtx, addr)
	return acc, err
}

func (accountInfoRetriever) GetAccountWithHeight(clientCtx client.Context, addr sdk.AccAddress) (client.Account, int64, error) {
	res, err := authtypes.NewQueryClient(clientCtx).AccountInfo(context.Background(), &authtypes.QueryAccountInfoRequest{Address: addr.String()})
	if err != nil {
		return nil, 0, fmt.Errorf("account %s: %w", addr, err)
	}
	if res.Info == nil {
		return nil, 0, fmt.Errorf("account %s: no account info", addr)
	}
	return res.Info, 0, nil
}

func (r accountInfoRetriever) EnsureExists(clientCtx client.Context, addr sdk.AccAddress) error {
	_, err := r.GetAccount(clientCtx, addr)
	return err
}

func (r accountInfoRetriever) GetAccountNumberSequence(clientCtx client.Context, addr sdk.AccAddress) (uint64, uint64, error) {
	acc, err := r.GetAccount(clientCtx, addr)
	if err != nil {
		return 0, 0, err
	}
	return acc.GetAccountNumber(), acc.GetSequence(), nil
}

// CopyCounterpartyKey copies the named secp256k1 or eth_secp256k1 key from
// one keyring into another, e.g. from a "test" keyring into an encrypted
// "file" one, and checks the address is unchanged. The chains' own
// `keys export` armors private keys with the legacy amino codec, which
// the counterparty binaries may not know for eth_secp256k1, so the raw key
// is imported directly.
func CopyCounterpartyKey(name string, from, to keyring.Keyring) (sdk.AccAddress, error) {
	rec, err := from.Key(name)
	if err != nil {
		return nil, err
	}
	local := rec.GetLocal()
	if local == nil || local.PrivKey == nil {
		return nil, fmt.Errorf("%s is not a local key (ledger, offline or multisig keys can't be copied)", name)
	}
	var algo hd.PubKeyType
	priv := local.PrivKey.GetCachedValue()
	switch priv.(type) {
	case *secp256k1.PrivKey:
		algo = hd.Secp256k1Type
	case *ethsecp256k1.PrivKey:
		algo = ethsecp256k1.Algo.Name()
	default:
		return nil, fmt.Errorf("%s is a %s key; only secp256k1 and eth_secp256k1 keys are copied", name, local.PrivKey.TypeUrl)
	}
	if _, err := to.Key(name); err == nil {
		return nil, fmt.Errorf("the destination already has a key named %s", name)
	}
	if err := to.ImportPrivKeyHex(name, hex.EncodeToString(priv.(cryptotypes.PrivKey).Bytes()), string(algo)); err != nil {
		return nil, err
	}
	want, err := rec.GetAddress()
	if err != nil {
		return nil, err
	}
	dst, err := to.Key(name)
	if err != nil {
		return nil, err
	}
	got, err := dst.GetAddress()
	if err != nil {
		return nil, err
	}
	if !got.Equals(want) {
		_ = to.Delete(name)
		return nil, errors.New("the copy has a different address; removed it")
	}
	return got, nil
}
