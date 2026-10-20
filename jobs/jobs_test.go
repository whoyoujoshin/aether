package jobs

import (
	"strings"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/wallet"
)

const (
	buyer  = "aether1buyer"
	seller = "aether1seller"
	other  = "aether1other"
)

var (
	one       = sdk.NewCoins(sdk.NewInt64Coin("uaeth", 1))
	specHash  = strings.Repeat("ab", 32)
	workHash  = strings.Repeat("cd", 32)
	work2Hash = strings.Repeat("ef", 32)
)

func pay(hash string, height int64, from, memo string) wallet.IncomingPayment {
	return wallet.IncomingPayment{Hash: hash, Height: height, From: from, Memo: memo, Amount: one}
}

// txHash gives each fake transaction a distinct hash whose first 16
// digits are its id.
func txHash(id string) string { return strings.ToUpper(id) + strings.Repeat("0", 48) }

func hire(id uint64, height int64, payer, payee, jobID, onExpiry string) wallet.EscrowCreated {
	return wallet.EscrowCreated{ID: id, Payer: payer, Payee: payee, Amount: "2000000uaeth", OnExpiry: onExpiry,
		Terms: Terms(jobID), TxHash: txHash("e" + strings.Repeat("0", 15)), Height: height}
}

func TestDerive_ALifecycle(t *testing.T) {
	const id = "00000000000000a1"
	offer, _ := OfferMemo(id, "1.5 AETH", "done in an hour")
	cheaper, _ := OfferMemo(id, "1 AETH", "")
	otherOffer, _ := OfferMemo(id, "3 AETH", "")
	payments := []wallet.IncomingPayment{
		pay(txHash(id), 10, buyer, PostMemo(specHash)),
		pay(txHash("1"+strings.Repeat("0", 15)), 11, seller, offer),
		pay(txHash("2"+strings.Repeat("0", 15)), 12, buyer, offer), // the poster can't offer
		pay(txHash("3"+strings.Repeat("0", 15)), 13, other, otherOffer),
		pay(txHash("4"+strings.Repeat("0", 15)), 14, seller, cheaper),                   // replaces the first
		pay(txHash("5"+strings.Repeat("0", 15)), 15, other, DeliverMemo(id, workHash)),  // not hired
		pay(txHash("6"+strings.Repeat("0", 15)), 21, seller, DeliverMemo(id, workHash)), // hired at 20
		pay(txHash("7"+strings.Repeat("0", 15)), 22, other, otherOffer),                 // after the hire
		pay(txHash("8"+strings.Repeat("0", 15)), 23, buyer, CloseMemo(id)),              // after the hire: no effect
		pay(txHash("9"+strings.Repeat("0", 15)), 24, seller, DeliverMemo(id, work2Hash)),
	}
	escrows := []wallet.EscrowCreated{
		hire(7, 19, other, seller, id, "ON_EXPIRY_RELEASE"), // not the poster's
		hire(8, 20, buyer, seller, id, "ON_EXPIRY_RELEASE"),
		hire(9, 21, buyer, other, id, "ON_EXPIRY_REFUND"), // the first hire stands
	}
	board := Derive(payments, escrows)
	require.Len(t, board, 1)
	j := board[0]
	require.Equal(t, id, j.ID)
	require.Equal(t, buyer, j.Poster)
	require.Equal(t, StateDelivered, j.State)
	require.Zero(t, j.ClosedHeight)
	require.Len(t, j.Offers, 2)
	require.Equal(t, Offer{Seller: seller, Price: "1 AETH", TxHash: txHash("4" + strings.Repeat("0", 15)), Height: 14}, *j.OfferOf(seller))
	require.Equal(t, "3 AETH", j.OfferOf(other).Price)
	require.NotNil(t, j.Hire)
	require.Equal(t, uint64(8), j.Hire.EscrowID)
	require.Equal(t, "release", j.Hire.OnExpiry)
	require.True(t, j.Hire.SellerProtected())
	require.Equal(t, []string{workHash, work2Hash}, []string{j.Deliveries[0].Hash, j.Deliveries[1].Hash})

	n, ok := Referenced(board, specHash)
	require.True(t, ok)
	require.Equal(t, MaxSpecBytes, n)
	n, ok = Referenced(board, work2Hash)
	require.True(t, ok)
	require.Equal(t, MaxDeliveryBytes, n)
	_, ok = Referenced(board, strings.Repeat("99", 32))
	require.False(t, ok)
}

func TestDerive_CloseBeforeHire(t *testing.T) {
	const id = "00000000000000b2"
	offer, _ := OfferMemo(id, "1 AETH", "")
	payments := []wallet.IncomingPayment{
		pay(txHash(id), 10, buyer, PostMemo(specHash)),
		pay(txHash("1"+strings.Repeat("0", 15)), 11, other, CloseMemo(id)), // not the poster's
		pay(txHash("2"+strings.Repeat("0", 15)), 12, buyer, CloseMemo(id)),
		pay(txHash("3"+strings.Repeat("0", 15)), 13, seller, offer), // after the close
	}
	board := Derive(payments, []wallet.EscrowCreated{hire(1, 14, buyer, seller, id, "ON_EXPIRY_REFUND")})
	j := board[0]
	require.Equal(t, StateClosed, j.State)
	require.Equal(t, int64(12), j.ClosedHeight)
	require.Nil(t, j.Hire, "a hire after the close doesn't count")
	require.Empty(t, j.Offers)
}

func TestDerive_IgnoresWhatDoesntCount(t *testing.T) {
	const id = "00000000000000c3"
	failed := pay(txHash("f"+strings.Repeat("0", 15)), 9, buyer, PostMemo(specHash))
	failed.Code = 5
	unpaid := pay(txHash("e"+strings.Repeat("0", 15)), 9, buyer, PostMemo(specHash))
	unpaid.Amount = sdk.NewCoins(sdk.NewInt64Coin("ibc/USDC", 5))
	payments := []wallet.IncomingPayment{
		failed, unpaid,
		pay(txHash("d"+strings.Repeat("0", 15)), 9, buyer, PostMemo("not-a-hash")),
		pay(txHash(id), 10, buyer, PostMemo(specHash)),
		pay(txHash(id)[:63]+"1", 11, other, PostMemo(workHash)), // same id: the first post keeps it
		pay(txHash("1"+strings.Repeat("0", 15)), 12, seller, OfferPrefix+id+":lots"),
		pay(txHash("2"+strings.Repeat("0", 15)), 12, seller, OfferPrefix+"0000000000000000:1 AETH"),
	}
	board := Derive(payments, []wallet.EscrowCreated{hire(1, 13, buyer, buyer, id, "ON_EXPIRY_RELEASE")}) // self-hire
	require.Len(t, board, 1)
	j := board[0]
	require.Equal(t, specHash, j.SpecHash)
	require.Equal(t, StateOpen, j.State)
	require.Empty(t, j.Offers)
	require.Nil(t, j.Hire)
}

func TestDerive_NewestFirst(t *testing.T) {
	board := Derive([]wallet.IncomingPayment{
		pay(txHash("0000000000000001"), 10, buyer, PostMemo(specHash)),
		pay(txHash("0000000000000002"), 30, buyer, PostMemo(workHash)),
		pay(txHash("0000000000000003"), 20, other, PostMemo(work2Hash)),
	}, nil)
	require.Equal(t, []string{"0000000000000002", "0000000000000003", "0000000000000001"}, []string{board[0].ID, board[1].ID, board[2].ID})
}

func TestSpecAndMemos(t *testing.T) {
	b, h, err := Spec{Title: "Summarize a paper", Description: "Two paragraphs.", Budget: "1 AETH", Tags: []string{"writing"}}.Encode()
	require.NoError(t, err)
	require.Equal(t, Hash(b), h)
	s, err := DecodeSpec(b)
	require.NoError(t, err)
	require.Equal(t, "Summarize a paper", s.Title)

	_, _, err = Spec{Title: "", Description: "x"}.Encode()
	require.Error(t, err)
	_, _, err = Spec{Title: "x", Description: strings.Repeat("a", MaxDescription+1)}.Encode()
	require.Error(t, err)

	m, err := OfferMemo("00000000000000a1", "2 USDC", "")
	require.NoError(t, err)
	require.Equal(t, "job-offer:00000000000000a1:2 USDC", m)
	for _, bad := range []string{"2", "AETH", "-1 AETH", "1 AETH:x"} {
		_, err := OfferMemo("00000000000000a1", bad, "")
		require.Error(t, err, bad)
	}
	_, err = OfferMemo("A1", "1 AETH", "")
	require.Error(t, err)

	// The longest memos fit the chain's 256-byte limit.
	long, err := OfferMemo("00000000000000a1", "123456789.123456 abcdefghijklmnop", strings.Repeat("é", MaxNote/2))
	require.NoError(t, err)
	require.LessOrEqual(t, len(long), 256)
	_, err = OfferMemo("00000000000000a1", "1 AETH", strings.Repeat("é", MaxNote/2+1))
	require.Error(t, err)
	require.LessOrEqual(t, len(DeliverMemo("00000000000000a1", workHash)), 256)
	require.Equal(t, "00000000000000a1", IDOf("00000000000000A1FFFF"))
}
