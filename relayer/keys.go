package relayer

import (
	"context"
	"fmt"
	"io"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/whoyoujoshin/aether/crypto/ethsecp256k1"
)

// OpenCounterpartyKeyring opens the keyring holding the key that signs on
// the other chain. Besides ordinary secp256k1 keys it reads and creates
// eth_secp256k1 keys (Injective's), so a keyring made by injectived
// (`injectived keys add --keyring-backend test --home <dir>`) works as is.
// It registers that key type on cdc's interface registry. The file
// backend reads its passphrase from in.
func OpenCounterpartyKeyring(backend, dir string, in io.Reader, cdc codec.Codec) (keyring.Keyring, error) {
	ethsecp256k1.RegisterInterfaces(cdc.InterfaceRegistry())
	return keyring.New("counterpartyd", backend, dir, in, cdc, func(o *keyring.Options) {
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
