package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	cometrpchttp "github.com/cometbft/cometbft/rpc/client/http"
	cmttypes "github.com/cometbft/cometbft/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// searchedTx is one indexed transaction, decoded only as far as its events
// and raw bytes. Block time is filled when the node still has the block.
type searchedTx struct {
	hash   string
	height int64
	when   time.Time
	code   uint32
	events []abci.Event
	raw    []byte
}

// recentActionTxs is the newest transactions whose message.action is
// typeURL, up to limit. The node's indexer has to be recording that
// event; a failure is returned so the caller can say so instead of
// showing an empty list as if nothing had happened.
func recentActionTxs(ctx context.Context, rpc *cometrpchttp.HTTP, typeURL string, limit int) ([]searchedTx, error) {
	if limit <= 0 {
		limit = 20
	}
	page := 1
	res, err := rpc.TxSearch(ctx, fmt.Sprintf("message.action='%s'", typeURL), false, &page, &limit, "desc")
	if err != nil {
		return nil, err
	}
	out := make([]searchedTx, 0, len(res.Txs))
	heights := make([]int64, 0, len(res.Txs))
	for _, r := range res.Txs {
		heights = append(heights, r.Height)
		out = append(out, searchedTx{
			hash:   strings.ToUpper(hex.EncodeToString(cmttypes.Tx(r.Tx).Hash())),
			height: r.Height,
			code:   r.TxResult.Code,
			events: r.TxResult.Events,
			raw:    r.Tx,
		})
	}
	times := blockTimes(ctx, rpc, heights)
	for i := range out {
		if t, ok := times[out[i].height]; ok {
			out[i].when = t
		}
	}
	return out, nil
}

var (
	blockTimeMu    sync.Mutex
	blockTimeCache = map[int64]time.Time{}
)

// blockTimes is the block time at each height. A committed block's time
// never changes, so a hit is remembered. At most 30 uncached heights are
// fetched per call, so a cold scan can't pin the node.
func blockTimes(ctx context.Context, rpc *cometrpchttp.HTTP, heights []int64) map[int64]time.Time {
	out := map[int64]time.Time{}
	var missing []int64
	seen := map[int64]bool{}
	blockTimeMu.Lock()
	for _, h := range heights {
		if h <= 0 || seen[h] {
			continue
		}
		seen[h] = true
		if t, ok := blockTimeCache[h]; ok {
			out[h] = t
			continue
		}
		missing = append(missing, h)
	}
	blockTimeMu.Unlock()
	fetched := 0
	for _, h := range missing {
		if fetched >= 30 || ctx.Err() != nil {
			break
		}
		hh := h
		b, err := rpc.Block(ctx, &hh)
		fetched++
		if err != nil || b == nil || b.Block == nil {
			continue
		}
		out[h] = b.Block.Time
		blockTimeMu.Lock()
		blockTimeCache[h] = b.Block.Time
		blockTimeMu.Unlock()
	}
	return out
}

func grpcUnimplemented(err error) bool {
	st, ok := status.FromError(err)
	return ok && st.Code() == codes.Unimplemented
}

// clipErr is a single-line, length-capped error for a note on a page.
// The raw RPC text (HTML, "unexpected end of JSON") stays out of banners.
func clipErr(err error, n int) string {
	if err == nil {
		return ""
	}
	s := strings.Join(strings.Fields(err.Error()), " ")
	if n > 0 && len(s) > n {
		return s[:n] + "…"
	}
	return s
}
