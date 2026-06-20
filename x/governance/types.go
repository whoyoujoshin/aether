package governance

const (
	ModuleName = "governance"
	StoreKey   = ModuleName
)

type Params struct {
	VotingPeriod int `json:"voting_period"`
}

type GenesisState struct {
	Params Params `json:"params"`
}

func DefaultGenesisState() GenesisState {
	return GenesisState{
		Params: Params{
			VotingPeriod: 604800, // 1 week in seconds
		},
	}
}
