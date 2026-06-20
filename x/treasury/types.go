package treasury

const (
	ModuleName = "treasury"
	StoreKey   = ModuleName
)

type Params struct {
	InitialBalance int `json:"initial_balance"`
}

type GenesisState struct {
	Params Params `json:"params"`
}

func DefaultGenesisState() GenesisState {
	return GenesisState{
		Params: Params{
			InitialBalance: 1000000,
		},
	}
}
