package testutil

import (
	"sort"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

type MockPowKeeper struct {
	TenureRatios     map[string]math.LegacyDec
	ActiveValidators map[string]bool
	TopKSize         int64
	// Powers is each validator's power; an active validator missing
	// from it has power 1.
	Powers map[string]int64
}

func (m *MockPowKeeper) GetTopKSize(ctx sdk.Context) int64 {
	return m.TopKSize
}

func (m *MockPowKeeper) GetActiveValidatorCount(ctx sdk.Context) int64 {
	return int64(len(m.ActiveValidators))
}

func NewMockPowKeeper() *MockPowKeeper {
	return &MockPowKeeper{
		TenureRatios:     make(map[string]math.LegacyDec),
		ActiveValidators: make(map[string]bool),
		Powers:           make(map[string]int64),
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

func (m *MockPowKeeper) IterateActiveValidators(ctx sdk.Context) []sdk.AccAddress {
	var out []sdk.AccAddress
	for a, ok := range m.ActiveValidators {
		if !ok {
			continue
		}
		addr, err := sdk.AccAddressFromBech32(a)
		if err != nil {
			continue
		}
		out = append(out, addr)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

func (m *MockPowKeeper) GetValidatorVotingPower(ctx sdk.Context, minerAddr sdk.AccAddress) int64 {
	if !m.ActiveValidators[minerAddr.String()] {
		return 0
	}
	if p, ok := m.Powers[minerAddr.String()]; ok {
		return p
	}
	return 1
}
