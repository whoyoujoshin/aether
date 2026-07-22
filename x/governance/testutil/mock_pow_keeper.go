package testutil

import (
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

type MockPowKeeper struct {
	TenureRatios     map[string]math.LegacyDec
	ActiveValidators map[string]bool
	TopKSize         int64
}

func (m *MockPowKeeper) GetTopKSize(ctx sdk.Context) int64 {
	return m.TopKSize
}

func NewMockPowKeeper() *MockPowKeeper {
	return &MockPowKeeper{
		TenureRatios:     make(map[string]math.LegacyDec),
		ActiveValidators: make(map[string]bool),
	}
}

func (m *MockPowKeeper) GetValidatorTenureRatio(ctx sdk.Context, minerAddr sdk.AccAddress) math.LegacyDec {
	if ratio, ok := m.TenureRatios[minerAddr.String()]; ok {
		return ratio
	}
	return math.LegacyZeroDec()
}

func (m *MockPowKeeper) IsActiveValidator(ctx sdk.Context, minerAddr sdk.AccAddress) bool {
	return m.ActiveValidators[minerAddr.String()]
}