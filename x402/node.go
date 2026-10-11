package x402

import (
	"context"
	"errors"

	"github.com/whoyoujoshin/aether/wallet"
)

// NodeChain is Chain on a node's gRPC, through wallet.Client.
type NodeChain struct{ Client *wallet.Client }

func (n NodeChain) Account(_ context.Context, address string) (uint64, uint64, error) {
	return n.Client.GetAccountInfo(address)
}

func (n NodeChain) LatestHeight(context.Context) (int64, error) { return n.Client.GetLatestHeight() }

func (n NodeChain) Simulate(ctx context.Context, txBytes []byte) (uint64, error) {
	return n.Client.Simulate(ctx, txBytes)
}

func (n NodeChain) Broadcast(_ context.Context, txBytes []byte) (string, uint32, string, error) {
	r, err := n.Client.BroadcastTx(wallet.SignedTx{Bytes: txBytes})
	return r.TxHash, r.Code, r.RawLog, err
}

func (n NodeChain) TxResult(_ context.Context, hash string) (bool, uint32, string, error) {
	d, err := n.Client.GetTransactionByHash(hash)
	if errors.Is(err, wallet.ErrTransactionNotFound) {
		return false, 0, "", nil
	}
	if err != nil {
		return false, 0, "", err
	}
	return true, d.Code, d.RawLog, nil
}
