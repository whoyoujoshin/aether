// Package node reads mining work from an Aether node over gRPC. It is
// apart from package miner, which x/pow's tests import.
package node

import (
	"context"
	"fmt"
	"strconv"

	"github.com/cosmos/cosmos-sdk/client/grpc/cmtservice"
	"google.golang.org/grpc"

	"github.com/whoyoujoshin/aether/miner"
	pow "github.com/whoyoujoshin/aether/x/pow"
)

// Work reads the work from a node: its latest block, the difficulty
// now (what x/pow records at that height) and the recency window.
func Work(ctx context.Context, conn grpc.ClientConnInterface) (miner.Work, error) {
	latest, err := cmtservice.NewServiceClient(conn).GetLatestBlock(ctx, &cmtservice.GetLatestBlockRequest{})
	if err != nil {
		return miner.Work{}, fmt.Errorf("latest block: %w", err)
	}
	q := pow.NewQueryClient(conn)
	diff, err := q.Difficulty(ctx, &pow.QueryDifficultyRequest{})
	if err != nil {
		return miner.Work{}, fmt.Errorf("difficulty: %w", err)
	}
	d, err := strconv.ParseUint(diff.Difficulty, 10, 64)
	if err != nil {
		return miner.Work{}, fmt.Errorf("difficulty %q: %w", diff.Difficulty, err)
	}
	recency := int64(60)
	if p, err := q.Params(ctx, &pow.QueryParamsRequest{}); err == nil && p.RecencyWindowK > 0 {
		recency = p.RecencyWindowK
	}
	var height int64
	var chainID string
	if b := latest.SdkBlock; b != nil {
		height, chainID = b.Header.Height, b.Header.ChainID
	} else if b := latest.Block; b != nil {
		height, chainID = b.Header.Height, b.Header.ChainID
	}
	if latest.BlockId == nil || len(latest.BlockId.Hash) != 32 || height <= 0 {
		return miner.Work{}, fmt.Errorf("the node's latest block has no hash or height")
	}
	return miner.Work{ChainID: chainID, Height: height, Hash: latest.BlockId.Hash, Difficulty: d, RecencyWindow: recency}, nil
}
