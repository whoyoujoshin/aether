package wallet

import (
	"context"
	"fmt"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"

	"github.com/whoyoujoshin/aether/app"
)

// TxParams bundles everything needed to construct and sign a
// transaction beyond the message itself -- kept explicit rather than
// hidden inside Client/Wallet state, since a real wallet often needs
// to query these values fresh (a stale sequence number is a common,
// real source of broadcast failures) rather than cache them silently.
type TxParams struct {
	ChainID       string
	AccountNumber uint64
	Sequence      uint64
	GasLimit      uint64
	Fees          sdk.Coins
}

// SignedTx holds a fully signed, broadcast-ready transaction. The raw
// bytes are what actually gets sent over the wire; TxBuilder is
// retained only so callers can inspect the signed transaction (e.g.
// for display) before broadcasting.
type SignedTx struct {
	Bytes []byte
}

// BuildAndSignSendTx constructs a real bank.MsgSend, then signs it
// using the named account's real key from the wallet's keyring --
// for an mldsa account, this transparently invokes the genuine
// ML-DSA-44 Sign implementation via the keyring's standard interface,
// no special-casing required. Kept as a single function (rather than
// separate Build/Sign steps) for this first, minimal version of the
// library; splitting build and sign into independently callable steps
// (for a genuine offline-signing workflow) is a reasonable future
// extension once this core path is proven correct.
func (w *Wallet) BuildAndSignSendTx(fromName, fromAddr, toAddr string, amount sdk.Coins, params TxParams) (SignedTx, error) {
	encodingConfig := app.MakeEncodingConfig()

	msg := banktypes.NewMsgSend(sdk.MustAccAddressFromBech32(fromAddr), sdk.MustAccAddressFromBech32(toAddr), amount)

	factory := tx.Factory{}.
		WithTxConfig(encodingConfig.TxConfig).
		WithKeybase(w.kr).
		WithChainID(params.ChainID).
		WithAccountNumber(params.AccountNumber).
		WithSequence(params.Sequence).
		WithGas(params.GasLimit).
		WithFees(params.Fees.String())

	txBuilder, err := factory.BuildUnsignedTx(msg)
	if err != nil {
		return SignedTx{}, fmt.Errorf("failed to build unsigned tx: %w", err)
	}

	if err := tx.Sign(context.Background(), factory, fromName, txBuilder, true); err != nil {
		return SignedTx{}, fmt.Errorf("failed to sign tx: %w", err)
	}

	bz, err := encodingConfig.TxConfig.TxEncoder()(txBuilder.GetTx())
	if err != nil {
		return SignedTx{}, fmt.Errorf("failed to encode signed tx: %w", err)
	}

	return SignedTx{Bytes: bz}, nil
}

// BroadcastResult is a simplified view of the real broadcast response
// -- exposing just what a caller typically needs.
type BroadcastResult struct {
	TxHash string
	Code   uint32
	RawLog string
}

// BroadcastTx sends an already-signed transaction to the chain via
// the standalone gRPC broadcast endpoint -- deliberately decoupled
// from signing, so a signed transaction could in principle be
// produced offline and broadcast later, through a different
// connection, by different code.
func (c *Client) BroadcastTx(signed SignedTx) (BroadcastResult, error) {
	txClient := txtypes.NewServiceClient(c.conn)

	resp, err := txClient.BroadcastTx(context.Background(), &txtypes.BroadcastTxRequest{
		TxBytes: signed.Bytes,
		Mode:    txtypes.BroadcastMode_BROADCAST_MODE_SYNC,
	})
	if err != nil {
		return BroadcastResult{}, err
	}

	return BroadcastResult{
		TxHash: resp.TxResponse.TxHash,
		Code:   resp.TxResponse.Code,
		RawLog: resp.TxResponse.RawLog,
	}, nil
}

var _ client.TxBuilder // referenced only to confirm the import resolves; TxBuilder itself is used via factory.BuildUnsignedTx's return type
var _ keyring.Keyring   // referenced only to confirm the import resolves; used via Wallet.kr's type