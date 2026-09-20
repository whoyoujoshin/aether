package testutil

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// MockBankKeeper is a minimal in-memory stand-in for treasury.BankKeeper.
// Balances is keyed by denom and set directly by tests, deliberately
// independent of SendCalls -- this is what lets a test simulate a real
// direct MsgSend to the treasury module's address (raising the real
// balance without the tracked ledger ever knowing), the exact scenario
// GetRealBankBalance/QueryBalanceResponse exist to surface.
type MockBankKeeper struct {
	SendCalls []SendCall
	Balances  sdk.Coins

	SendErr error
}

type SendCall struct {
	SenderModule  string
	RecipientAddr sdk.AccAddress
	Coins         sdk.Coins
}

func NewMockBankKeeper() *MockBankKeeper {
	return &MockBankKeeper{Balances: sdk.NewCoins()}
}

func (m *MockBankKeeper) SendCoinsFromModuleToAccount(ctx context.Context, senderModule string, recipientAddr sdk.AccAddress, amt sdk.Coins) error {
	if m.SendErr != nil {
		return m.SendErr
	}
	m.SendCalls = append(m.SendCalls, SendCall{SenderModule: senderModule, RecipientAddr: recipientAddr, Coins: amt})
	m.Balances = m.Balances.Sub(amt...)
	return nil
}

func (m *MockBankKeeper) GetBalance(ctx context.Context, addr sdk.AccAddress, denom string) sdk.Coin {
	return sdk.NewCoin(denom, m.Balances.AmountOf(denom))
}
