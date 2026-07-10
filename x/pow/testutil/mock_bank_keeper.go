package testutil

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// MockBankKeeper is a minimal in-memory stand-in for types.BankKeeper,
// letting keeper tests assert exactly what was minted/sent without
// spinning up a real bank module.
type MockBankKeeper struct {
	MintCalls []MintCall
	SendCalls []SendCall

	MintErr error
	SendErr error
}

type MintCall struct {
	Module string
	Coins  sdk.Coins
}

type SendCall struct {
	SenderModule    string
	RecipientAddr   sdk.AccAddress
	RecipientModule string
	Coins           sdk.Coins
}

func NewMockBankKeeper() *MockBankKeeper {
	return &MockBankKeeper{}
}

func (m *MockBankKeeper) MintCoins(ctx context.Context, moduleName string, amt sdk.Coins) error {
	if m.MintErr != nil {
		return m.MintErr
	}
	m.MintCalls = append(m.MintCalls, MintCall{Module: moduleName, Coins: amt})
	return nil
}

func (m *MockBankKeeper) SendCoinsFromModuleToAccount(ctx context.Context, senderModule string, recipientAddr sdk.AccAddress, amt sdk.Coins) error {
	if m.SendErr != nil {
		return m.SendErr
	}
	m.SendCalls = append(m.SendCalls, SendCall{SenderModule: senderModule, RecipientAddr: recipientAddr, Coins: amt})
	return nil
}

func (m *MockBankKeeper) SendCoinsFromModuleToModule(ctx context.Context, senderModule, recipientModule string, amt sdk.Coins) error {
	if m.SendErr != nil {
		return m.SendErr
	}
	m.SendCalls = append(m.SendCalls, SendCall{SenderModule: senderModule, RecipientModule: recipientModule, Coins: amt})
	return nil
}