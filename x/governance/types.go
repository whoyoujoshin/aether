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
	KeyVotePrefix = []byte("vote/")
)

// AmountValidationActivationHeight gates a real, live-flagged fix
// (Gitty, Section 3 item 5): SubmitProposal validated the recipient
// address and the deposit amount immediately, but never validated
// msg.Amount -- the actual treasury spend amount -- until
// executeTreasurySpend, which only ever runs if the proposal reaches
// PASSED. A malformed Amount (unparseable, negative, zero) could sit
// through the full deposit period plus multi-day voting period only to
// fail at the very last step, and since ResolveProposal already sets
// status to PASSED before attempting execution, that failure is only
// logged, never reflected in queryable state -- a proposal that
// "passed" but never actually paid looks identical to one that passed
// and paid.
//
// Gated per the same discipline as every other fix in this project:
// an unconditional new rejection reason on SubmitProposal is exactly
// the class of change that has repeatedly broken fresh-node replay
// here. Aether's whole governance history is a single proposal
// (Proposal 1, a 5 AETH treasury spend that resolved FAILED_QUORUM
// before ever reaching execution, so its own Amount field's validity
// was never actually exercised) -- confirm it parses as a valid
// positive integer before assuming this gate needs no accompanying
// height-specific care, the same way the pow module gates were each
// checked against real history first.
const AmountValidationActivationHeight int64 = 77000