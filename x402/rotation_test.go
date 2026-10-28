package x402

import (
	"context"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

// keyedChain is fakeChain plus the keys stored on accounts.
type keyedChain struct {
	*fakeChain
	keys map[string][]byte
}

func (k keyedChain) AccountPubKey(_ context.Context, address string) ([]byte, error) {
	return k.keys[address], nil
}

// A payer whose key was rotated (docs/PQ-KEY-ROTATION.md) signs with a
// key whose address isn't its own: accepted only if that is the key on
// the account.
func TestVerifyRotatedPayer(t *testing.T) {
	s := newSigner(t)
	acc, err := s.w.GetAccount("payer")
	require.NoError(t, err)
	account := sdk.AccAddress("rotated_account_addr").String()
	req := requirement()
	tx := s.sign(t, []sdk.Msg{send(account, payTo, sdk.NewCoins(sdk.NewInt64Coin("uaeth", 500)))}, 7, 3, 0)
	ctx := context.Background()

	plain := &fakeChain{number: 7, sequence: 3, height: 100}
	got := NewFacilitator(testChain, plain).Verify(ctx, payloadFor(req, tx), req)
	require.Equal(t, ReasonSigner, got.InvalidReason, "a chain that can't read account keys accepts only a key's own address")

	rotated := keyedChain{fakeChain: plain, keys: map[string][]byte{account: acc.PubKey}}
	got = NewFacilitator(testChain, rotated).Verify(ctx, payloadFor(req, tx), req)
	require.Equal(t, VerifyResponse{IsValid: true, Payer: account}, got)

	other := newSigner(t)
	otherAcc, err := other.w.GetAccount("payer")
	require.NoError(t, err)
	for name, key := range map[string][]byte{"another key on the account": otherAcc.PubKey, "no key on the account": nil} {
		c := keyedChain{fakeChain: plain, keys: map[string][]byte{account: key}}
		got = NewFacilitator(testChain, c).Verify(ctx, payloadFor(req, tx), req)
		require.Equal(t, ReasonSigner, got.InvalidReason, name)
	}
}

// After a rotation the retired key still hashes to the account's address;
// the account now holds another key, so the retired key no longer signs
// for it. A payer that never rotated (its stored key is its own, or none
// yet) is accepted as before.
func TestVerifyRetiredKeyRefused(t *testing.T) {
	s := newSigner(t)
	acc, err := s.w.GetAccount("payer")
	require.NoError(t, err)
	req := requirement()
	tx := s.sign(t, []sdk.Msg{send(s.addr, payTo, sdk.NewCoins(sdk.NewInt64Coin("uaeth", 500)))}, 7, 3, 0)
	ctx := context.Background()
	plain := &fakeChain{number: 7, sequence: 3, height: 100}

	successor := newSigner(t)
	newAcc, err := successor.w.GetAccount("payer")
	require.NoError(t, err)
	rotated := keyedChain{fakeChain: plain, keys: map[string][]byte{s.addr: newAcc.PubKey}}
	got := NewFacilitator(testChain, rotated).Verify(ctx, payloadFor(req, tx), req)
	require.Equal(t, ReasonSigner, got.InvalidReason)

	for name, key := range map[string][]byte{"its own key on chain": acc.PubKey, "no key on chain yet": nil} {
		c := keyedChain{fakeChain: plain, keys: map[string][]byte{s.addr: key}}
		got = NewFacilitator(testChain, c).Verify(ctx, payloadFor(req, tx), req)
		require.Equal(t, VerifyResponse{IsValid: true, Payer: s.addr}, got, name)
	}
}
