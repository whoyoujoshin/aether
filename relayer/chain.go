// Package relayer is a minimal, purpose-built relayer -- not a
// general-purpose one -- for driving exactly one real IBC handshake
// and relaying one real packet between Aether and a genuinely separate
// counterparty process (see counterparty/app.go and docs/IBC.md). It
// exists because app/ibc_handshake_test.go's in-process ibctesting
// harness proves IBC works against itself, not against a real second
// chain over real RPC/gRPC with real proofs.
package relayer

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	rpchttp "github.com/cometbft/cometbft/rpc/client/http"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
)

// Chain is a minimal RPC+gRPC+signing handle to one side of the relay.
//
// Bech32Prefix matters beyond just this chain's own encoding: the SDK's
// AccountRetriever (used inside tx.Factory.Prepare, to fetch account
// number/sequence before signing) calls addr.String() on our signer
// address, which reads the process-global sdk.GetConfig() bech32
// prefix -- not any per-chain codec. Since a single relayer process
// talks to two chains with two different prefixes ("aether" and
// "cparty"), setGlobalPrefix must run immediately before any call that
// might trigger that lookup, or the wrong chain's own account query
// handler rejects the resulting address string as an hrp mismatch.
type Chain struct {
	Name         string // "aether" or "counterparty", for log messages only
	ChainID      string
	Bech32Prefix string
	ClientCtx    client.Context
	Factory      tx.Factory
	FromAddr     sdk.AccAddress
	FromAddrStr  string
}

func (c *Chain) setGlobalPrefix() {
	cfg := sdk.GetConfig()
	cfg.SetBech32PrefixForAccount(c.Bech32Prefix, c.Bech32Prefix+"pub")
}

// NewChain dials rpcAddr/grpcAddr and builds a client.Context + tx.Factory
// signing as fromName out of kr. gasPrices is a coin string like
// "0.0001uaeth" or "" for a chain with no minimum gas price.
func NewChain(name, rpcAddr, grpcAddr, chainID, bech32Prefix string, cdc codec.Codec, txConfig client.TxConfig, kr keyring.Keyring, fromName, gasPrices string) (*Chain, error) {
	rpcClient, err := rpchttp.New(rpcAddr, "/websocket")
	if err != nil {
		return nil, fmt.Errorf("%s: dialing rpc %s: %w", name, rpcAddr, err)
	}
	grpcConn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("%s: dialing grpc %s: %w", name, grpcAddr, err)
	}

	record, err := kr.Key(fromName)
	if err != nil {
		return nil, fmt.Errorf("%s: key %q: %w", name, fromName, err)
	}
	addr, err := record.GetAddress()
	if err != nil {
		return nil, err
	}
	fromAddrStr, err := address.NewBech32Codec(bech32Prefix).BytesToString(addr)
	if err != nil {
		return nil, fmt.Errorf("%s: encoding %s address: %w", name, bech32Prefix, err)
	}

	clientCtx := client.Context{}.
		WithChainID(chainID).
		WithClient(rpcClient).
		WithGRPCClient(grpcConn).
		WithCodec(cdc).
		WithInterfaceRegistry(cdc.InterfaceRegistry()).
		WithTxConfig(txConfig).
		WithKeyring(kr).
		WithFromName(fromName).
		WithFromAddress(addr).
		WithBroadcastMode("sync").
		WithAccountRetriever(authtypes.AccountRetriever{}).
		WithSkipConfirmation(true).
		WithInput(bufio.NewReader(os.Stdin)).
		WithOutput(os.Stdout)

	factory := tx.Factory{}.
		WithChainID(chainID).
		WithTxConfig(txConfig).
		WithKeybase(kr).
		WithAccountRetriever(authtypes.AccountRetriever{}).
		WithGas(600_000).
		WithSimulateAndExecute(false)
	if gasPrices != "" {
		factory = factory.WithGasPrices(gasPrices)
	}

	return &Chain{
		Name:         name,
		ChainID:      chainID,
		Bech32Prefix: bech32Prefix,
		ClientCtx:    clientCtx,
		Factory:      factory,
		FromAddr:     addr,
		FromAddrStr:  fromAddrStr,
	}, nil
}

// SignAndBroadcast builds, signs and broadcasts msgs as this chain's
// FromAddr, waits for the tx to actually land in a block (not just be
// mempool-accepted), and returns its emitted events -- every handshake
// step depends on the previous one already being committed, and needs
// the ID (client/connection/channel) that step's events assigned.
func (c *Chain) SignAndBroadcast(msgs ...sdk.Msg) ([]abci.Event, error) {
	c.setGlobalPrefix()
	txf, err := c.Factory.Prepare(c.ClientCtx)
	if err != nil {
		return nil, fmt.Errorf("%s: preparing tx: %w", c.Name, err)
	}
	unsigned, err := txf.BuildUnsignedTx(msgs...)
	if err != nil {
		return nil, fmt.Errorf("%s: building tx: %w", c.Name, err)
	}
	if err := tx.Sign(context.Background(), txf, c.ClientCtx.FromName, unsigned, true); err != nil {
		return nil, fmt.Errorf("%s: signing tx: %w", c.Name, err)
	}
	txBytes, err := c.ClientCtx.TxConfig.TxEncoder()(unsigned.GetTx())
	if err != nil {
		return nil, err
	}
	res, err := c.ClientCtx.BroadcastTx(txBytes)
	if err != nil {
		return nil, fmt.Errorf("%s: broadcasting tx: %w", c.Name, err)
	}
	if res.Code != 0 {
		return nil, fmt.Errorf("%s: tx %s rejected: code %d: %s", c.Name, res.TxHash, res.Code, res.RawLog)
	}
	return c.waitForTx(res.TxHash)
}

func (c *Chain) waitForTx(hash string) ([]abci.Event, error) {
	hashBytes, err := hex.DecodeString(hash)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		res, err := c.ClientCtx.Client.Tx(context.Background(), hashBytes, false)
		if err == nil {
			if res.TxResult.Code != 0 {
				return nil, fmt.Errorf("%s: tx %s failed in block: code %d: %s", c.Name, hash, res.TxResult.Code, res.TxResult.Log)
			}
			return res.TxResult.Events, nil
		}
		time.Sleep(1 * time.Second)
	}
	return nil, fmt.Errorf("%s: tx %s did not land within 30s", c.Name, hash)
}

// EventAttr returns the value of attrKey on the first event named
// eventType, or an error naming what it was looking for.
func EventAttr(events []abci.Event, eventType, attrKey string) (string, error) {
	for _, e := range events {
		if e.Type != eventType {
			continue
		}
		for _, a := range e.Attributes {
			if a.Key == attrKey {
				return a.Value, nil
			}
		}
	}
	return "", fmt.Errorf("no %s.%s in tx events", eventType, attrKey)
}

// LatestHeight returns this chain's current committed height.
func (c *Chain) LatestHeight() (int64, error) {
	node, err := c.ClientCtx.GetNode()
	if err != nil {
		return 0, err
	}
	info, err := node.ABCIInfo(context.Background())
	if err != nil {
		return 0, err
	}
	if info.Response.LastBlockHeight == 0 {
		return 0, errors.New("chain has not committed a block yet")
	}
	return info.Response.LastBlockHeight, nil
}
