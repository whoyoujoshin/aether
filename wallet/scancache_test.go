package wallet

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"sync"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// heightSearcher pages a height-ordered history the way CometBFT's tx
// search does, honouring the query's tx.height>=N, and records the
// heights each scan started from.
type heightSearcher struct {
	mu      sync.Mutex
	all     []*sdk.TxResponse
	starts  []int64 // the N of each page-1 query
	decoded int     // transactions returned, all pages
	fail    error
}

var heightRe = regexp.MustCompile(`tx\.height>=(\d+)`)

func (f *heightSearcher) GetTxsEvent(_ context.Context, in *txtypes.GetTxsEventRequest, _ ...grpc.CallOption) (*txtypes.GetTxsEventResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return nil, f.fail
	}
	m := heightRe.FindStringSubmatch(in.Query)
	from, _ := strconv.ParseInt(m[1], 10, 64)
	if in.Page == 1 {
		f.starts = append(f.starts, from)
	}
	var match []*sdk.TxResponse
	for _, r := range f.all {
		if r.Height >= from {
			match = append(match, r)
		}
	}
	start := int((in.Page - 1) * in.Limit)
	end := min(start+int(in.Limit), len(match))
	resp := &txtypes.GetTxsEventResponse{Total: uint64(len(match))}
	if start < len(match) {
		resp.TxResponses = match[start:end]
		for _, r := range resp.TxResponses {
			resp.Txs = append(resp.Txs, &txtypes.Tx{Body: &txtypes.TxBody{Memo: "memo-" + r.TxHash}})
		}
		f.decoded += len(resp.TxResponses)
	}
	return resp, nil
}

func (f *heightSearcher) add(height int64, events ...abci.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.all = append(f.all, &sdk.TxResponse{TxHash: fmt.Sprintf("TX%d-%d", height, len(f.all)), Height: height, Events: events})
}

// addNext adds a transaction in a new block after the newest, as a node
// indexes them: in height order.
func (f *heightSearcher) addNext(events ...abci.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	h := int64(1)
	if n := len(f.all); n > 0 {
		h = f.all[n-1].Height + 1
	}
	f.all = append(f.all, &sdk.TxResponse{TxHash: fmt.Sprintf("TX%d-%d", h, len(f.all)), Height: h, Events: events})
}

func cachedIncoming(c *scanCache[IncomingPayment], f txSearcher, addr string, since int64, max int) ([]IncomingPayment, error) {
	return c.scan("node|incoming|"+addr, since, max, func(p IncomingPayment) int64 { return p.Height },
		func(from int64, m int) ([]IncomingPayment, error) {
			return incomingPayments(context.Background(), f, addr, from, m)
		})
}

// The cache answers exactly what a full scan would, while new blocks
// land, more transactions arrive in the newest block, and callers ask
// from different heights; and after the first call it asks the node
// only from the newest result's height.
func TestScanCache_MatchesFullScan(t *testing.T) {
	me := sdk.AccAddress("me__________________").String()
	payer := sdk.AccAddress("payer_______________").String()
	f := &heightSearcher{}
	for h := int64(10); h < 260; h++ {
		f.add(h, transfer(payer, me, "1uaeth"))
	}
	var c scanCache[IncomingPayment]
	check := func(since int64) {
		t.Helper()
		got, err := cachedIncoming(&c, f, me, since, 10_000)
		require.NoError(t, err)
		want, err := incomingPayments(context.Background(), &heightSearcher{all: f.all}, me, since, 10_000)
		require.NoError(t, err)
		require.Equal(t, want, got, "since %d", since)
	}

	check(1)
	require.Equal(t, []int64{1}, f.starts)
	f.add(259, transfer(payer, me, "2uaeth")) // the newest block grew
	f.add(260, transfer(payer, me, "3uaeth"))
	check(1)
	require.Equal(t, int64(259), f.starts[1], "resumes at the newest result's height, inclusive")
	check(200) // a later window: no new scan needed beyond the resume
	require.Equal(t, int64(260), f.starts[2])
	check(1)
	f.add(300, transfer(payer, sdk.AccAddress("someone_else________").String(), "9uaeth"))
	f.add(301, transfer(payer, me, "4uaeth"))
	check(5)
	check(301)
	check(302)

	// Each refresh decoded a handful of transactions, not the history.
	before := f.decoded
	check(1)
	require.LessOrEqual(t, f.decoded-before, 2)
}

// An earlier starting height than the entry covers scans in full again.
func TestScanCache_EarlierSinceScansAgain(t *testing.T) {
	me := sdk.AccAddress("me__________________").String()
	payer := sdk.AccAddress("payer_______________").String()
	f := &heightSearcher{}
	for h := int64(10); h < 20; h++ {
		f.add(h, transfer(payer, me, "1uaeth"))
	}
	var c scanCache[IncomingPayment]
	got, err := cachedIncoming(&c, f, me, 15, 100)
	require.NoError(t, err)
	require.Len(t, got, 5)
	got, err = cachedIncoming(&c, f, me, 1, 100)
	require.NoError(t, err)
	require.Len(t, got, 10)
	require.Equal(t, []int64{15, 1}, f.starts)
}

// A failed or capped scan is never kept: the next call starts over.
func TestScanCache_ErrorsAndCapsAreNotKept(t *testing.T) {
	me := sdk.AccAddress("me__________________").String()
	payer := sdk.AccAddress("payer_______________").String()
	f := &heightSearcher{}
	for h := int64(1); h <= 30; h++ {
		f.add(h, transfer(payer, me, "1uaeth"))
	}
	var c scanCache[IncomingPayment]

	f.fail = errors.New("node down")
	_, err := cachedIncoming(&c, f, me, 1, 100)
	require.ErrorContains(t, err, "node down")
	f.fail = nil

	capped, err := cachedIncoming(&c, f, me, 1, 10)
	require.ErrorIs(t, err, ErrTooMuchHistory)
	require.GreaterOrEqual(t, len(capped), 10)

	got, err := cachedIncoming(&c, f, me, 1, 100)
	require.NoError(t, err)
	require.Len(t, got, 30)
	require.Equal(t, []int64{1, 1}, f.starts, "the failed scan asked nothing; the capped one was kept by nobody")

	// A refresh that fails leaves the kept results alone.
	f.add(31, transfer(payer, me, "1uaeth"))
	f.fail = errors.New("node down")
	_, err = cachedIncoming(&c, f, me, 1, 100)
	require.Error(t, err)
	f.fail = nil
	got, err = cachedIncoming(&c, f, me, 1, 100)
	require.NoError(t, err)
	require.Len(t, got, 31)

	// Grown past the cap: ErrTooMuchHistory with the oldest, as a full
	// scan would.
	got, err = cachedIncoming(&c, f, me, 1, 31)
	require.ErrorIs(t, err, ErrTooMuchHistory)
	require.Len(t, got, 31)
	require.Equal(t, int64(1), got[0].Height)
}

func TestScanCache_Off(t *testing.T) {
	t.Setenv("AETHER_SCAN_CACHE", "off")
	me := sdk.AccAddress("me__________________").String()
	f := &heightSearcher{}
	f.add(5, transfer("x", me, "1uaeth"))
	var c scanCache[IncomingPayment]
	for range 3 {
		_, err := cachedIncoming(&c, f, me, 1, 100)
		require.NoError(t, err)
	}
	require.Equal(t, []int64{1, 1, 1}, f.starts)
}

// Escrows: several per height, and new ones in the newest block.
func TestScanCache_Escrows(t *testing.T) {
	f := &heightSearcher{}
	f.add(10, created(1, "job:00000000000000a1"))
	f.add(12, created(2, "job:00000000000000b2"), created(3, "job:00000000000000b3"))
	var c scanCache[EscrowCreated]
	scan := func() []EscrowCreated {
		got, err := c.scan("node|escrows|job:", 1, 1000, func(e EscrowCreated) int64 { return e.Height },
			func(from int64, m int) ([]EscrowCreated, error) {
				return escrowsCreatedWithTerms(context.Background(), f, "job:", from, m)
			})
		require.NoError(t, err)
		return got
	}
	require.Len(t, scan(), 3)
	f.add(12, created(4, "job:00000000000000b4"))
	f.add(13, created(5, "job:00000000000000c5"))
	got := scan()
	require.Len(t, got, 5)
	var ids []uint64
	for _, e := range got {
		ids = append(ids, e.ID)
	}
	require.Equal(t, []uint64{1, 2, 3, 4, 5}, ids, "no duplicates from the re-read block, none missed")
	require.Equal(t, []int64{1, 12}, f.starts)
}

// Many callers at once share one refresh per entry and see whole results.
func TestScanCache_Concurrent(t *testing.T) {
	me := sdk.AccAddress("me__________________").String()
	f := &heightSearcher{}
	for h := int64(1); h <= 50; h++ {
		f.add(h, transfer("x", me, "1uaeth"))
	}
	var c scanCache[IncomingPayment]
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i%5 == 0 {
				f.addNext(transfer("x", me, "1uaeth"))
			}
			got, err := cachedIncoming(&c, f, me, 1, 1000)
			require.NoError(t, err)
			for j := 1; j < len(got); j++ {
				require.LessOrEqual(t, got[j-1].Height, got[j].Height)
			}
		}()
	}
	wg.Wait()
	got, err := cachedIncoming(&c, f, me, 1, 1000)
	require.NoError(t, err)
	require.Len(t, got, 54)
}
