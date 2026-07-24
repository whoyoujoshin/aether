package testutil

import (
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

type MockTreasuryKeeper struct {
	FundCalls []FundCall
}

type FundCall struct {
	Amount math.Int
}

func NewMockTreasuryKeeper() *MockTreasuryKeeper {
	return &MockTreasuryKeeper{}
}

func (m *MockTreasuryKeeper) FundTreasury(ctx sdk.Context, amount math.Int) {
	m.FundCalls = append(m.FundCalls, FundCall{Amount: amount})
}