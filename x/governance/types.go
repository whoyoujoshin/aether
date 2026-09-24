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
//
// Originally set to 77000 alongside x/pow's BanEnforcement/
// RotationRevocation gates -- part of the same binary (commit
// 796e9c8) that was never actually rolled out fleet-wide before the
// live chain's tip passed that height. See
// BanEnforcementActivationHeight's doc comment in x/pow/types.go for
// the full incident: a solo-upgraded node diverged immediately because
// replaying/continuing past a gate height with new logic disagrees
// with a chain whose real history was finalized by the old, ungated
// logic. 77000 is burned for the same reason here, whether or not a
// malformed Amount was ever actually submitted in that window --
// re-set to 90000 in lockstep with the pow module's gates, to be
// crossed only after a coordinated, whole-fleet binary swap.
const AmountValidationActivationHeight int64 = 90000

// ParamChangeGovernanceActivationHeight gates two things together,
// both brand new:
//
//  1. MsgSubmitParamChangeProposal -- rejected unconditionally before
//     this height. A genuinely new message type needs no gate for
//     fresh-replay correctness (no historical block can ever contain
//     a message type that didn't exist yet, so there's no "old vs new
//     code disagrees" risk the way there is for changing existing
//     decision logic) -- gated anyway purely so the whole feature
//     turns on at one clean, predictable height rather than "the
//     instant this binary deploys."
//
//  2. The cached-context-with-rollback execution pattern for BOTH
//     proposal kinds (treasury-spend and param-change), landing in a
//     new PROPOSAL_STATUS_EXECUTION_FAILED status on failure instead
//     of the old behavior (treasury-spend only, today: run directly on
//     ctx with no rollback, log-but-still-mark-PASSED on failure). This
//     DOES modify existing, already-live decision logic, so per this
//     project's standing discipline it's gated even though the PASSED
//     branch has zero historical footprint -- no proposal has ever
//     reached it (Proposal 1 resolved FAILED_QUORUM first).
//
// Before this height, treasury-spend execution behaves exactly as it
// always has; at and after it, both proposal kinds use the safer,
// cached-context pattern with the new distinct failure status.
//
// Originally set to 80000. Re-set to 90000, in lockstep with x/pow's
// BanEnforcement/RotationRevocation gates and this file's own
// AmountValidationActivationHeight, after Gitty flagged that the tip
// (~79959) was only ~41 blocks from 80000 with no coordinated
// fleet upgrade in place -- merging or running this code past that
// height ungated across the fleet would have burned it exactly the
// way 77000 was burned (see BanEnforcementActivationHeight's doc
// comment in x/pow/types.go). Confirm/adjust against the seed's actual
// height immediately before the coordinated cutover.
const ParamChangeGovernanceActivationHeight int64 = 90000

// QuorumActiveValidatorCountActivationHeight gates a change to
// computeQuorumThreshold: before this height, quorum is 60% of
// x/pow's GetTopKSize (the fixed target validator-set size, e.g. 21);
// at and after it, quorum is 60% of GetActiveValidatorCount (however
// many validators are actually bonded right now).
//
// This is a deliberate policy change, not a bug fix -- the original
// TopK-based quorum (see computeQuorumThreshold's own doc comment,
// "the locked 60% quorum spec") exists specifically so a thin
// validator set can't have its governance captured by a small
// colluding minority: with only 4 active validators today, 60% of
// them is just 3, which is not a meaningful supermajority. Switching
// to active-count quorum trades that protection away permanently in
// exchange for governance actually being able to resolve (Proposal 1
// and Proposal 2 both resolved FAILED_QUORUM under the TopK rule with
// only 4 of a TopKSize=21 target bonded) -- this was an explicit,
// discussed tradeoff, not an oversight.
//
// Gated per this project's standing discipline: computeQuorumThreshold
// is existing, already-exercised decision logic (both real proposals
// so far have gone through it), so an ungated change risks the exact
// fresh-replay divergence documented on ParamChangeGovernanceActivationHeight
// and AmountValidationActivationHeight above -- a node with the old
// binary and a node with the new one would resolve the same pending
// proposal differently at the same height.
//
// Placeholder height -- Gitty's report that put this fix in motion
// had the live tip at ~91395, which is already past the 90000 gate
// used above, so this number is NOT safe to deploy as-is. Confirm the
// seed's actual live tip immediately before the coordinated
// fleet-wide binary swap and raise this if the tip is already close,
// exactly as instructed on ParamChangeGovernanceActivationHeight after
// 77000 and 80000 were both burned the same way.
const QuorumActiveValidatorCountActivationHeight int64 = 100_000