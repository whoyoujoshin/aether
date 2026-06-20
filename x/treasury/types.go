package treasury

const (
	ModuleName = "treasury"
	StoreKey   = ModuleName
)

type Params struct {
	TreasuryShare int `json:"treasury_share"`
}

type GenesisState struct {
	Params Params `json:"params"`
}

func DefaultGenesisState() GenesisState {
	return GenesisState{
		Params: Params{
			TreasuryShare: 15,
		},
	}
}