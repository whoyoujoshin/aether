package governance

const (
	ModuleName = "governance"
	StoreKey   = ModuleName
)

type Params struct {
	VotingPeriod  int `json:"voting_period"`
	Quorum        int `json:"quorum"`
	Supermajority int `json:"supermajority"`
}

type GenesisState struct {
	Params Params `json:"params"`
}

func DefaultGenesisState() GenesisState {
	return GenesisState{
		Params: Params{
			VotingPeriod:  21 * 24 * 60 * 60,
			Quorum:        15,
			Supermajority: 70,
		},
	}
}