package pow

const (
	ModuleName = "pow"
	StoreKey   = ModuleName
)

type Params struct {
	BlockReward int `json:"block_reward"`
	Difficulty  int `json:"difficulty"`
}

type GenesisState struct {
	Params Params `json:"params"`
}

func DefaultGenesisState() GenesisState {
	return GenesisState{
		Params: Params{
			BlockReward: 5,
			Difficulty:  1,
		},
	}
}
