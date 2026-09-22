package governance

import (
	"bytes"
	"context"

	sdkerrors "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"cosmossdk.io/math"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
)

type msgServer struct {
	Keeper
}

func NewMsgServerImpl(keeper Keeper) MsgServer {
	return &msgServer{Keeper: keeper}
}

func (k msgServer) SubmitProposal(goCtx context.Context, msg *MsgSubmitProposal) (*MsgSubmitProposalResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	proposerAddr, err := sdk.AccAddressFromBech32(msg.Proposer)
	if err != nil {
		return nil, sdkerrors.Wrapf(ErrInvalidProposer, "invalid proposer address %q: %s", msg.Proposer, err)
	}
	if _, err := sdk.AccAddressFromBech32(msg.Recipient); err != nil {
		return nil, sdkerrors.Wrapf(ErrInvalidRecipient, "invalid recipient address %q: %s", msg.Recipient, err)
	}

	depositAmount, ok := math.NewIntFromString(msg.Deposit)
	if !ok || depositAmount.IsNegative() {
		return nil, sdkerrors.Wrapf(ErrInvalidDeposit, "invalid deposit amount %q", msg.Deposit)
	}

	// A real, live-flagged gap (Gitty, Section 3 item 5): msg.Amount --
	// the actual treasury spend amount -- was never validated here,
	// only at executeTreasurySpend, which only ever runs if the
	// proposal reaches PASSED. A malformed Amount could sit through the
	// full deposit period plus multi-day voting period only to fail at
	// the very last step, with the failure only ever logged (proposal
	// status already committed to PASSED beforehand). Gated on
	// AmountValidationActivationHeight per the same discipline as every
	// other fix in this project -- see that constant's doc comment.
	if ctx.BlockHeight() >= AmountValidationActivationHeight {
		amount, ok := math.NewIntFromString(msg.Amount)
		if !ok || !amount.IsPositive() {
			return nil, sdkerrors.Wrapf(ErrInvalidAmount, "invalid or non-positive amount %q", msg.Amount)
		}
	}

	proposalID := k.Keeper.NextProposalID(ctx)
	now := ctx.BlockTime().Unix()
	depositPeriod := k.Keeper.GetDepositPeriod(ctx)

	proposal := Proposal{
		Id:             proposalID,
		Recipient:      msg.Recipient,
		Amount:         msg.Amount,
		TotalDeposit:   "0",
		Status:         ProposalStatus_PROPOSAL_STATUS_DEPOSIT_PERIOD,
		SubmitTime:     now,
		DepositEndTime: now + depositPeriod,
	}
	k.Keeper.SetProposal(ctx, proposal)

	if depositAmount.IsPositive() {
		if err := k.Keeper.addDeposit(ctx, proposalID, proposerAddr, depositAmount); err != nil {
			return nil, err
		}
	}

	return &MsgSubmitProposalResponse{ProposalId: proposalID}, nil
}

// SubmitParamChangeProposal is the generic path for governance to
// authorize any module's authority-gated message -- see
// ParamChangeGovernanceActivationHeight's doc comment for why this is
// gated, and MsgSubmitParamChangeProposal's proto comment for why the
// execute_msg field is a generic Any rather than per-parameter typed
// fields.
//
// Validation mirrors stock Cosmos SDK gov's own SubmitProposal exactly
// (see x/gov/keeper/proposal.go): the packed message must decode, run
// ValidateBasic if it has one, have exactly one signer, that signer
// must be this module's own account (never a real, externally-signed
// address -- a module account has no private key), and the chain's
// message router must have a registered handler for it. All checked
// at submission time, not deferred to execution -- exactly the
// discipline the Amount-validation fix (above) exists to enforce.
func (k msgServer) SubmitParamChangeProposal(goCtx context.Context, msg *MsgSubmitParamChangeProposal) (*MsgSubmitParamChangeProposalResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	if ctx.BlockHeight() < ParamChangeGovernanceActivationHeight {
		return nil, sdkerrors.Wrapf(ErrUnroutableProposalMsg, "param-change proposals are not active until height %d", ParamChangeGovernanceActivationHeight)
	}

	proposerAddr, err := sdk.AccAddressFromBech32(msg.Proposer)
	if err != nil {
		return nil, sdkerrors.Wrapf(ErrInvalidProposer, "invalid proposer address %q: %s", msg.Proposer, err)
	}

	depositAmount, ok := math.NewIntFromString(msg.Deposit)
	if !ok || depositAmount.IsNegative() {
		return nil, sdkerrors.Wrapf(ErrInvalidDeposit, "invalid deposit amount %q", msg.Deposit)
	}

	if msg.ExecuteMsg == nil {
		return nil, sdkerrors.Wrap(ErrInvalidExecuteMsg, "execute_msg is required")
	}

	var sdkMsg sdk.Msg
	if err := k.Keeper.cdc.InterfaceRegistry().UnpackAny(msg.ExecuteMsg, &sdkMsg); err != nil {
		return nil, sdkerrors.Wrapf(ErrInvalidExecuteMsg, "could not unpack execute_msg: %s", err)
	}

	if m, ok := sdkMsg.(sdk.HasValidateBasic); ok {
		if err := m.ValidateBasic(); err != nil {
			return nil, sdkerrors.Wrapf(ErrInvalidExecuteMsg, "execute_msg failed validation: %s", err)
		}
	}

	signers, _, err := k.Keeper.cdc.GetMsgV1Signers(sdkMsg)
	if err != nil {
		return nil, sdkerrors.Wrapf(ErrInvalidExecuteMsg, "could not determine execute_msg's signers: %s", err)
	}
	if len(signers) != 1 {
		return nil, sdkerrors.Wrapf(ErrInvalidExecuteMsgSigner, "execute_msg must have exactly one signer, got %d", len(signers))
	}
	governanceAddr := authtypes.NewModuleAddress(ModuleName)
	if !bytes.Equal(signers[0], governanceAddr) {
		return nil, sdkerrors.Wrapf(ErrInvalidExecuteMsgSigner, "execute_msg's signer must be %s, got %s", governanceAddr.String(), sdk.AccAddress(signers[0]).String())
	}

	if k.Keeper.router.Handler(sdkMsg) == nil {
		return nil, sdkerrors.Wrapf(ErrUnroutableProposalMsg, "no registered handler for %s", sdk.MsgTypeURL(sdkMsg))
	}

	proposalID := k.Keeper.NextProposalID(ctx)
	now := ctx.BlockTime().Unix()
	depositPeriod := k.Keeper.GetDepositPeriod(ctx)

	proposal := Proposal{
		Id:             proposalID,
		Amount:         "0",
		TotalDeposit:   "0",
		Status:         ProposalStatus_PROPOSAL_STATUS_DEPOSIT_PERIOD,
		SubmitTime:     now,
		DepositEndTime: now + depositPeriod,
		ProposalType:   ProposalType_PROPOSAL_TYPE_PARAM_CHANGE,
		ExecuteMsg:     msg.ExecuteMsg,
	}
	k.Keeper.SetProposal(ctx, proposal)

	if depositAmount.IsPositive() {
		if err := k.Keeper.addDeposit(ctx, proposalID, proposerAddr, depositAmount); err != nil {
			return nil, err
		}
	}

	return &MsgSubmitParamChangeProposalResponse{ProposalId: proposalID}, nil
}

func (k msgServer) Deposit(goCtx context.Context, msg *MsgDeposit) (*MsgDepositResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	depositorAddr, err := sdk.AccAddressFromBech32(msg.Depositor)
	if err != nil {
		return nil, sdkerrors.Wrapf(ErrInvalidProposer, "invalid depositor address %q: %s", msg.Depositor, err)
	}

	proposal, ok := k.Keeper.GetProposal(ctx, msg.ProposalId)
	if !ok {
		return nil, sdkerrors.Wrapf(ErrProposalNotFound, "no proposal with id %d", msg.ProposalId)
	}
	if proposal.Status != ProposalStatus_PROPOSAL_STATUS_DEPOSIT_PERIOD {
		return nil, sdkerrors.Wrapf(ErrDepositPeriodEnded, "proposal %d is not in its deposit period", msg.ProposalId)
	}

	amount, ok := math.NewIntFromString(msg.Amount)
	if !ok || !amount.IsPositive() {
		return nil, sdkerrors.Wrapf(ErrInvalidDeposit, "invalid deposit amount %q", msg.Amount)
	}

	if err := k.Keeper.addDeposit(ctx, msg.ProposalId, depositorAddr, amount); err != nil {
		return nil, err
	}

	return &MsgDepositResponse{}, nil
}

func (k msgServer) Vote(goCtx context.Context, msg *MsgVote) (*MsgVoteResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	voterAddr, err := sdk.AccAddressFromBech32(msg.Voter)
	if err != nil {
		return nil, sdkerrors.Wrapf(ErrInvalidProposer, "invalid voter address %q: %s", msg.Voter, err)
	}

	proposal, ok := k.Keeper.GetProposal(ctx, msg.ProposalId)
	if !ok {
		return nil, sdkerrors.Wrapf(ErrProposalNotFound, "no proposal with id %d", msg.ProposalId)
	}
	if proposal.Status != ProposalStatus_PROPOSAL_STATUS_VOTING_PERIOD {
		return nil, sdkerrors.Wrapf(ErrNotInVotingPeriod, "proposal %d is not in its voting period", msg.ProposalId)
	}

	if msg.Option == VoteOption_VOTE_OPTION_UNSPECIFIED {
		return nil, sdkerrors.Wrapf(ErrInvalidVoteOption, "vote option must be specified")
	}

	// Voting power (tenure ratio) is locked in at the moment of casting,
	// not recomputed at tally time -- see design discussion. This is
	// separate from active-validator STATUS, which IS re-checked at tally
	// time: if this voter has since fallen out of the active set (or been
	// slashed), their vote counts for nothing at tally, regardless of
	// what weight was recorded here.
	weight := k.Keeper.powKeeper.GetValidatorTenureRatio(ctx, voterAddr)

	vote := Vote{
		ProposalId: msg.ProposalId,
		Voter:      msg.Voter,
		Option:     msg.Option,
		Weight:     weight.String(),
	}
	k.Keeper.SetVote(ctx, vote)

	return &MsgVoteResponse{}, nil
}