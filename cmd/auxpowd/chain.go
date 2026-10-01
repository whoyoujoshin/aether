package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	sdkmath "cosmossdk.io/math"
	cometrpchttp "github.com/cometbft/cometbft/rpc/client/http"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/whoyoujoshin/aether/wallet"
	pow "github.com/whoyoujoshin/aether/x/pow"
)

// nodeChain is chain backed by an Aether node: CometBFT RPC for the latest
// block, gRPC for x/pow's state and for broadcasting.
type nodeChain struct {
	rpc    *cometrpchttp.HTTP
	powQ   pow.QueryClient
	txs    txtypes.ServiceClient
	wallet *wallet.Wallet
	client *wallet.Client

	from   string // keyring name of the bridge's signing key
	signer string // its address
	fees   sdk.Coins
	gas    uint64

	mu      sync.Mutex
	chainID string
	accNum  uint64
	seq     uint64
	haveSeq bool
}

func newNodeChain(rpcAddr string, conn *grpc.ClientConn, w *wallet.Wallet, c *wallet.Client, from, signer string, fees sdk.Coins, gas uint64) (*nodeChain, error) {
	rpc, err := cometrpchttp.New(rpcAddr, "/websocket")
	if err != nil {
		return nil, fmt.Errorf("CometBFT RPC %s: %w", rpcAddr, err)
	}
	return &nodeChain{
		rpc:    rpc,
		powQ:   pow.NewQueryClient(conn),
		txs:    txtypes.NewServiceClient(conn),
		wallet: w,
		client: c,
		from:   from,
		signer: signer,
		fees:   fees,
		gas:    gas,
	}, nil
}

func (n *nodeChain) State(ctx context.Context) (chainState, error) {
	blk, err := n.rpc.Block(ctx, nil)
	if err != nil {
		return chainState{}, fmt.Errorf("latest block: %w", err)
	}
	diff, err := n.powQ.Difficulty(ctx, &pow.QueryDifficultyRequest{})
	if err != nil {
		return chainState{}, fmt.Errorf("difficulty: %w", err)
	}
	// Below MergedMiningActivationHeight there's no separate AuxPoW
	// difficulty; the bridge refuses work there anyway.
	d := diff.AuxDifficulty
	if d == "" {
		d = diff.Difficulty
	}
	auxDifficulty, err := strconv.ParseUint(d, 10, 64)
	if err != nil {
		return chainState{}, fmt.Errorf("difficulty %q: %w", d, err)
	}
	reward, err := n.powQ.BlockReward(ctx, &pow.QueryBlockRewardRequest{})
	if err != nil {
		return chainState{}, fmt.Errorf("block reward: %w", err)
	}
	r, ok := sdkmath.NewIntFromString(reward.BlockReward)
	if !ok {
		return chainState{}, fmt.Errorf("block reward %q isn't an integer", reward.BlockReward)
	}
	params, err := n.powQ.Params(ctx, &pow.QueryParamsRequest{})
	if err != nil {
		return chainState{}, fmt.Errorf("params: %w", err)
	}
	n.mu.Lock()
	n.chainID = blk.Block.ChainID
	n.mu.Unlock()
	return chainState{
		ChainID:       blk.Block.ChainID,
		Height:        blk.Block.Height,
		BlockHash:     blk.BlockID.Hash,
		AuxDifficulty: auxDifficulty,
		BlockReward:   r,
		ShareBps:      params.MergedMiningRewardShareBps,
		RecencyWindow: params.RecencyWindowK,
	}, nil
}

// Submit signs a MsgSubmitPoW with the bridge's key. The key pays only the
// fee (--fees, zero by default): from MergedMiningActivationHeight the
// reward goes to the address the proof commits to. It keeps its own
// sequence so a submission doesn't wait on the last one being included,
// and refetches it once if the node disagrees.
func (n *nodeChain) Submit(ctx context.Context, d *pow.AuxPowData) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	msg := &pow.MsgSubmitPoW{Miner: n.signer, Submission: &pow.MsgSubmitPoW_AuxPow{AuxPow: d}}
	for attempt := 0; ; attempt++ {
		if !n.haveSeq {
			acc, seq, err := n.client.GetAccountInfo(n.signer)
			if err != nil {
				return "", fmt.Errorf("account %s: %w", n.signer, err)
			}
			n.accNum, n.seq, n.haveSeq = acc, seq, true
		}
		signed, err := n.wallet.BuildAndSignMsgTx(n.from, msg, wallet.TxParams{
			ChainID:       n.chainID,
			AccountNumber: n.accNum,
			Sequence:      n.seq,
			GasLimit:      n.gas,
			Fees:          n.fees,
		})
		if err != nil {
			return "", err
		}
		res, err := n.client.BroadcastTx(signed)
		if err != nil {
			n.haveSeq = false
			return "", err
		}
		if res.Code == 0 {
			n.seq++
			return res.TxHash, nil
		}
		n.haveSeq = false
		if res.Codespace == sdkerrors.ErrWrongSequence.Codespace() && res.Code == sdkerrors.ErrWrongSequence.ABCICode() && attempt == 0 {
			continue
		}
		return "", fmt.Errorf("node refused the tx: code %d (%s): %s", res.Code, res.Codespace, res.RawLog)
	}
}

func (n *nodeChain) TxResult(ctx context.Context, hash string) (txResult, bool, error) {
	resp, err := n.txs.GetTx(ctx, &txtypes.GetTxRequest{Hash: hash})
	if status.Code(err) == codes.NotFound || (err != nil && strings.Contains(err.Error(), "not found")) {
		return txResult{}, false, nil
	}
	if err != nil {
		return txResult{}, false, err
	}
	r := resp.TxResponse
	return txResult{Height: r.Height, Code: r.Code, Codespace: r.Codespace, Log: r.RawLog}, true, nil
}
