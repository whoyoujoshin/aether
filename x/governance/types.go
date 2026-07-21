package governance

const (
	ModuleName = "governance"
	StoreKey   = ModuleName
)

type Params struct {
	MinDeposit    int64 `json:"min_deposit" yaml:"min_deposit"`       // Minimum aeth deposit required to enter voting period
	DepositPeriod int64 `json:"deposit_period" yaml:"deposit_period"` // Seconds a proposal can accumulate deposit before expiring
	VotingPeriod  int64 `json:"voting_period" yaml:"voting_period"`   // Seconds a proposal stays open for voting once deposit is met
}

type GenesisState struct {
	Params Params `json:"params"`
}

func DefaultGenesisState() GenesisState {
	return GenesisState{
		Params: Params{
			MinDeposit:    25_000_000,
			DepositPeriod: 14 * 24 * 60 * 60, // 14 days in seconds
			VotingPeriod:  7 * 24 * 60 * 60,  // 7 days in seconds
		},
	}
}

var (
	KeyNextProposalID = []byte("next_proposal_id")
	KeyProposalPrefix = []byte("proposal/")
	KeyDepositPrefix  = []byte("deposit/") // deposit/{proposalID}/{depositorAddr}
)