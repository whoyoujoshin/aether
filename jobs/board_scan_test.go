package jobs

import (
	"context"
	"errors"
	"fmt"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/wallet"
)

// pagedChain answers scans as the wallet does: oldest first, at or above
// from, stopping with ErrTooMuchHistory once it has page results.
type pagedChain struct {
	page     int
	payments []wallet.IncomingPayment
	escrows  []wallet.EscrowCreated
	calls    int
}

func paged[T any](items []T, height func(T) int64, from int64, page int) ([]T, error) {
	var out []T
	for _, it := range items {
		if height(it) < from {
			continue
		}
		out = append(out, it)
		if len(out) >= page {
			return out, wallet.ErrTooMuchHistory
		}
	}
	return out, nil
}

func (c *pagedChain) board() *Board {
	return &Board{
		Scan: func(_ context.Context, from int64) ([]wallet.IncomingPayment, error) {
			c.calls++
			return paged(c.payments, func(p wallet.IncomingPayment) int64 { return p.Height }, from, c.page)
		},
		Escrows: func(_ context.Context, from int64) ([]wallet.EscrowCreated, error) {
			return paged(c.escrows, func(e wallet.EscrowCreated) int64 { return e.Height }, from, c.page)
		},
	}
}

func (c *pagedChain) memo(height int64, hash, memo string) {
	c.payments = append(c.payments, wallet.IncomingPayment{Hash: hash, Height: height, From: "aether1poster", Memo: memo,
		Amount: sdk.NewCoins(sdk.NewInt64Coin("uaeth", 1))})
}

// spam fills n memos, three to a block, from height 1.
func (c *pagedChain) spam(n int) int64 {
	h := int64(1)
	for i := 0; i < n; i++ {
		c.memo(h, fmt.Sprintf("%064X", i), "not a job memo")
		if i%3 == 2 {
			h++
		}
	}
	return h + 1
}

func TestBoard_PostPastAFullPageIsSeen(t *testing.T) {
	c := &pagedChain{page: 10}
	h := c.spam(95) // nine and a half pages of memos nobody can parse
	post := fmt.Sprintf("%064X", 1_000_000)
	c.memo(h, post, PostMemo(Hash([]byte("spec"))))

	board, err := c.board().Jobs(context.Background())
	require.NoError(t, err)
	require.Len(t, board, 1, "the post after the spam is on the board")
	require.Equal(t, post[:IDLength], board[0].ID)
}

func TestBoard_ReadsOnlyWhatsNewAfterTheFirstTime(t *testing.T) {
	c := &pagedChain{page: 10}
	h := c.spam(40)
	b := c.board()
	_, err := b.Fresh(context.Background(), 0)
	require.NoError(t, err)
	first := c.calls

	c.memo(h, fmt.Sprintf("%064X", 2_000_000), PostMemo(Hash([]byte("spec"))))
	board, err := b.Fresh(context.Background(), 0)
	require.NoError(t, err)
	require.Len(t, board, 1)
	require.Equal(t, 1, c.calls-first, "one page for the new block")
}

func TestBoard_NoDuplicatesAcrossPagesInOneBlock(t *testing.T) {
	c := &pagedChain{page: 4}
	for i := 0; i < 3; i++ { // three memos in block 4, three posts in block 5: a page ends inside block 5
		c.memo(4, fmt.Sprintf("%064X", 100+i), "filler")
	}
	for i := 0; i < 3; i++ {
		c.memo(5, fmt.Sprintf("%064X", 200+i), PostMemo(Hash([]byte(fmt.Sprintf("spec %d", i)))))
	}
	var h History
	payments, _, err := h.Read(
		func(from int64) ([]wallet.IncomingPayment, error) {
			return paged(c.payments, func(p wallet.IncomingPayment) int64 { return p.Height }, from, c.page)
		},
		func(int64) ([]wallet.EscrowCreated, error) { return nil, nil })
	require.NoError(t, err)
	seen := map[string]bool{}
	for _, p := range payments {
		require.False(t, seen[p.Hash], "%s read twice", p.Hash)
		seen[p.Hash] = true
	}
	require.Len(t, payments, 6)
}

func TestBoard_RefusesRatherThanServeAPartialBoard(t *testing.T) {
	t.Run("one block holds more than a page", func(t *testing.T) {
		c := &pagedChain{page: 3}
		for i := 0; i < 5; i++ {
			c.memo(7, fmt.Sprintf("%064X", i), "filler")
		}
		_, err := c.board().Jobs(context.Background())
		require.ErrorIs(t, err, ErrBoardIncomplete)
	})
	t.Run("the scan fails part way", func(t *testing.T) {
		c := &pagedChain{page: 10}
		c.spam(25)
		b := c.board()
		scan := b.Scan
		b.Scan = func(ctx context.Context, from int64) ([]wallet.IncomingPayment, error) {
			if from > 1 {
				return nil, errors.New("node unreachable")
			}
			return scan(ctx, from)
		}
		_, err := b.Jobs(context.Background())
		require.ErrorContains(t, err, "node unreachable")
	})
}
