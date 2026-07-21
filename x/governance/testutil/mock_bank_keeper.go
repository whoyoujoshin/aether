package testutil

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

type MockBankKeeper struct {
	MintCalls []MintCall
	BurnCalls []BurnCall
	SendCalls []SendCall

	MintErr error
	BurnErr error
	SendErr error
}

type MintCall struct {
	Module string
	Coins  sdk.Coins
}

type BurnCall struct {
	Module string
	Coins  sdk.Coins
}

type SendCall struct {
	SenderAddr      sdk.AccAddress
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

func (m *MockBankKeeper) BurnCoins(ctx context.Context, moduleName string, amt sdk.Coins) error {
	if m.BurnErr != nil {
		return m.BurnErr
	}
	m.BurnCalls = append(m.BurnCalls, BurnCall{Module: moduleName, Coins: amt})
	return nil
}

func (m *MockBankKeeper) SendCoinsFromAccountToModule(ctx context.Context, senderAddr sdk.AccAddress, recipientModule string, amt sdk.Coins) error {
	if m.SendErr != nil {
		return m.SendErr
	}
	m.SendCalls = append(m.SendCalls, SendCall{SenderAddr: senderAddr, RecipientModule: recipientModule, Coins: amt})
	return nil
}

func (m *MockBankKeeper) SendCoinsFromModuleToAccount(ctx context.Context, senderModule string, recipientAddr sdk.AccAddress, amt sdk.Coins) error {
	if m.SendErr != nil {
		return m.SendErr
	}
	m.SendCalls = append(m.SendCalls, SendCall{SenderModule: senderModule, RecipientAddr: recipientAddr, Coins: amt})
	return nil
}