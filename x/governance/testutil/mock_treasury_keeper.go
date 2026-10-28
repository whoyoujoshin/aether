package testutil

import (
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

type MockTreasuryKeeper struct {
	SpendCalls     []SpendCall
	SpendErr       error
	SpendCoinCalls []SpendCoinCall
}

type SpendCoinCall struct {
	Recipient sdk.AccAddress
	Coin      sdk.Coin
}

type SpendCall struct {
	Recipient sdk.AccAddress
	Amount    math.Int
}

func NewMockTreasuryKeeper() *MockTreasuryKeeper {
	return &MockTreasuryKeeper{}
}

func (m *MockTreasuryKeeper) Spend(ctx sdk.Context, recipient sdk.AccAddress, amount math.Int) error {
	if m.SpendErr != nil {
		return m.SpendErr
	}
	m.SpendCalls = append(m.SpendCalls, SpendCall{Recipient: recipient, Amount: amount})
	return nil
}

func (m *MockTreasuryKeeper) SpendCoin(ctx sdk.Context, recipient sdk.AccAddress, coin sdk.Coin) error {
	if m.SpendErr != nil {
		return m.SpendErr
	}
	m.SpendCoinCalls = append(m.SpendCoinCalls, SpendCoinCall{Recipient: recipient, Coin: coin})
	return nil
}
