package governance_test

import (
	"errors"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/baseapp"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	consensustypes "github.com/cosmos/cosmos-sdk/x/consensus/types"
	tmproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/x/governance"
)

// governanceAddr is what a real execute_msg's sole signer must be --
// governance's own module account, which has no private key, so this
// can only ever be satisfied from inside governance's own execution
// path, never by a real externally-signed tx.
func governanceAddr() sdk.AccAddress {
	return authtypes.NewModuleAddress(governance.ModuleName)
}

func packUpdateParams(t *testing.T, authority string) *codectypes.Any {
	t.Helper()
	msg := &consensustypes.MsgUpdateParams{
		Authority: authority,
		Block:     &tmproto.BlockParams{MaxGas: -1, MaxBytes: 22020096},
		Evidence:  &tmproto.EvidenceParams{},
		Validator: &tmproto.ValidatorParams{PubKeyTypes: []string{"ed25519"}},
	}
	any, err := codectypes.NewAnyWithValue(msg)
	require.NoError(t, err)
	return any
}

// --- SubmitParamChangeProposal ---

func TestSubmitParamChangeProposal_RejectsBeforeActivation(t *testing.T) {
	k, ctx, _, _ := setupKeeperWithRouter(t)
	srv := governance.NewMsgServerImpl(k)

	_, proposerStr := validProposerAddr(t)
	msg := &governance.MsgSubmitParamChangeProposal{
		Proposer:   proposerStr,
		Deposit:    "0",
		ExecuteMsg: packUpdateParams(t, governanceAddr().String()),
	}

	_, err := srv.SubmitParamChangeProposal(ctx, msg)
	require.Error(t, err, "param-change proposals must be rejected before the activation height")
}

func TestSubmitParamChangeProposal_RejectsInvalidProposer(t *testing.T) {
	k, ctx, _, _ := setupKeeperWithRouter(t)
	srv := governance.NewMsgServerImpl(k)
	ctx = ctx.WithBlockHeight(governance.ParamChangeGovernanceActivationHeight)

	msg := &governance.MsgSubmitParamChangeProposal{
		Proposer:   "not-a-valid-address",
		Deposit:    "0",
		ExecuteMsg: packUpdateParams(t, governanceAddr().String()),
	}

	_, err := srv.SubmitParamChangeProposal(ctx, msg)
	require.Error(t, err)
	require.True(t, errors.Is(err, governance.ErrInvalidProposer))
}

func TestSubmitParamChangeProposal_RejectsMissingExecuteMsg(t *testing.T) {
	k, ctx, _, _ := setupKeeperWithRouter(t)
	srv := governance.NewMsgServerImpl(k)
	ctx = ctx.WithBlockHeight(governance.ParamChangeGovernanceActivationHeight)

	_, proposerStr := validProposerAddr(t)
	msg := &governance.MsgSubmitParamChangeProposal{Proposer: proposerStr, Deposit: "0"}

	_, err := srv.SubmitParamChangeProposal(ctx, msg)
	require.Error(t, err)
	require.True(t, errors.Is(err, governance.ErrInvalidExecuteMsg))
}

func TestSubmitParamChangeProposal_RejectsWrongSigner(t *testing.T) {
	k, ctx, router, _ := setupKeeperWithRouter(t)
	srv := governance.NewMsgServerImpl(k)
	ctx = ctx.WithBlockHeight(governance.ParamChangeGovernanceActivationHeight)

	router.Handlers["/cosmos.consensus.v1.MsgUpdateParams"] = func(ctx sdk.Context, req sdk.Msg) (*sdk.Result, error) {
		return &sdk.Result{}, nil
	}

	_, proposerStr := validProposerAddr(t)
	someoneElse := sdk.AccAddress("not_governances_own_ad")
	msg := &governance.MsgSubmitParamChangeProposal{
		Proposer:   proposerStr,
		Deposit:    "0",
		ExecuteMsg: packUpdateParams(t, someoneElse.String()),
	}

	_, err := srv.SubmitParamChangeProposal(ctx, msg)
	require.Error(t, err, "execute_msg signed by anyone other than governance's own account must be rejected")
	require.True(t, errors.Is(err, governance.ErrInvalidExecuteMsgSigner))
}

func TestSubmitParamChangeProposal_RejectsUnroutableMsg(t *testing.T) {
	k, ctx, _, _ := setupKeeperWithRouter(t)
	srv := governance.NewMsgServerImpl(k)
	ctx = ctx.WithBlockHeight(governance.ParamChangeGovernanceActivationHeight)
	// Deliberately never registering a handler in the mock router.

	_, proposerStr := validProposerAddr(t)
	msg := &governance.MsgSubmitParamChangeProposal{
		Proposer:   proposerStr,
		Deposit:    "0",
		ExecuteMsg: packUpdateParams(t, governanceAddr().String()),
	}

	_, err := srv.SubmitParamChangeProposal(ctx, msg)
	require.Error(t, err, "a message with no registered router handler must be rejected at submission, not discovered only at execution")
	require.True(t, errors.Is(err, governance.ErrUnroutableProposalMsg))
}

func TestSubmitParamChangeProposal_Success_CreatesProposal(t *testing.T) {
	k, ctx, router, _ := setupKeeperWithRouter(t)
	srv := governance.NewMsgServerImpl(k)
	ctx = ctx.WithBlockHeight(governance.ParamChangeGovernanceActivationHeight)

	router.Handlers["/cosmos.consensus.v1.MsgUpdateParams"] = func(ctx sdk.Context, req sdk.Msg) (*sdk.Result, error) {
		return &sdk.Result{}, nil
	}

	_, proposerStr := validProposerAddr(t)
	msg := &governance.MsgSubmitParamChangeProposal{
		Proposer:   proposerStr,
		Deposit:    "0",
		ExecuteMsg: packUpdateParams(t, governanceAddr().String()),
	}

	resp, err := srv.SubmitParamChangeProposal(ctx, msg)
	require.NoError(t, err)

	proposal, ok := k.GetProposal(ctx, resp.ProposalId)
	require.True(t, ok)
	require.Equal(t, governance.ProposalType_PROPOSAL_TYPE_PARAM_CHANGE, proposal.ProposalType)
	require.NotNil(t, proposal.ExecuteMsg)
}

// --- ResolveProposal (post-activation execution path) ---

func TestResolveProposal_AfterActivation_ParamChangeProposalExecutesViaRouter(t *testing.T) {
	k, ctx, router, mockPow := setupKeeperWithRouter(t)
	ctx = ctx.WithBlockHeight(governance.ParamChangeGovernanceActivationHeight)

	var handlerCalled bool
	router.Handlers["/cosmos.consensus.v1.MsgUpdateParams"] = func(ctx sdk.Context, req sdk.Msg) (*sdk.Result, error) {
		handlerCalled = true
		return &sdk.Result{}, nil
	}

	proposal := governance.Proposal{
		Id:           1,
		TotalDeposit: "25000000",
		Status:       governance.ProposalStatus_PROPOSAL_STATUS_VOTING_PERIOD,
		ProposalType: governance.ProposalType_PROPOSAL_TYPE_PARAM_CHANGE,
		ExecuteMsg:   packUpdateParams(t, governanceAddr().String()),
	}
	k.SetProposal(ctx, proposal)

	voterYes := sdk.AccAddress("paramchange_voter_yes_")
	mockPow.ActiveValidators[voterYes.String()] = true
	k.SetVote(ctx, governance.Vote{ProposalId: 1, Voter: voterYes.String(), Option: governance.VoteOption_VOTE_OPTION_YES, Weight: "1"})

	err := k.ResolveProposal(ctx, proposal, 0)
	require.NoError(t, err)
	require.True(t, handlerCalled, "the router's handler must actually be invoked for a passed param-change proposal")

	updated, ok := k.GetProposal(ctx, 1)
	require.True(t, ok)
	require.Equal(t, governance.ProposalStatus_PROPOSAL_STATUS_PASSED, updated.Status)
}

func TestResolveProposal_AfterActivation_ExecutionFailureSetsExecutionFailedAndRollsBack(t *testing.T) {
	k, ctx, router, mockPow := setupKeeperWithRouter(t)
	ctx = ctx.WithBlockHeight(governance.ParamChangeGovernanceActivationHeight)

	router.Handlers["/cosmos.consensus.v1.MsgUpdateParams"] = func(ctx sdk.Context, req sdk.Msg) (*sdk.Result, error) {
		return nil, errors.New("simulated execution failure")
	}

	proposal := governance.Proposal{
		Id:           1,
		TotalDeposit: "25000000",
		Status:       governance.ProposalStatus_PROPOSAL_STATUS_VOTING_PERIOD,
		ProposalType: governance.ProposalType_PROPOSAL_TYPE_PARAM_CHANGE,
		ExecuteMsg:   packUpdateParams(t, governanceAddr().String()),
	}
	k.SetProposal(ctx, proposal)

	depositor := sdk.AccAddress("paramchange_depositor_")
	k.SetDeposit(ctx, governance.Deposit{ProposalId: 1, Depositor: depositor.String(), Amount: "25000000"})

	voterYes := sdk.AccAddress("paramchange_voter_yes2")
	mockPow.ActiveValidators[voterYes.String()] = true
	k.SetVote(ctx, governance.Vote{ProposalId: 1, Voter: voterYes.String(), Option: governance.VoteOption_VOTE_OPTION_YES, Weight: "1"})

	err := k.ResolveProposal(ctx, proposal, 0)
	require.NoError(t, err, "ResolveProposal itself must not error just because execution failed internally")

	updated, ok := k.GetProposal(ctx, 1)
	require.True(t, ok)
	require.Equal(t, governance.ProposalStatus_PROPOSAL_STATUS_EXECUTION_FAILED, updated.Status,
		"a proposal that passed but never actually executed must be distinguishable from one that passed and executed")
}

func TestResolveProposal_AfterActivation_TreasurySpendAlsoUsesExecutionFailedStatus(t *testing.T) {
	k, ctx, mockBank, mockPow, mockTreasury := setupKeeperWithTreasury(t)
	ctx = ctx.WithBlockHeight(governance.ParamChangeGovernanceActivationHeight)
	mockTreasury.SpendErr = errors.New("treasury balance insufficient")

	proposal := governance.Proposal{
		Id:           1,
		Recipient:    sdk.AccAddress("treasury_recipient____").String(),
		Amount:       "10000000",
		TotalDeposit: "25000000",
		Status:       governance.ProposalStatus_PROPOSAL_STATUS_VOTING_PERIOD,
	}
	k.SetProposal(ctx, proposal)

	depositor := sdk.AccAddress("treasury_depositor____")
	k.SetDeposit(ctx, governance.Deposit{ProposalId: 1, Depositor: depositor.String(), Amount: "25000000"})

	voterYes := sdk.AccAddress("treasury_voter_yes____")
	mockPow.ActiveValidators[voterYes.String()] = true
	k.SetVote(ctx, governance.Vote{ProposalId: 1, Voter: voterYes.String(), Option: governance.VoteOption_VOTE_OPTION_YES, Weight: "1"})

	err := k.ResolveProposal(ctx, proposal, 0)
	require.NoError(t, err)

	updated, ok := k.GetProposal(ctx, 1)
	require.True(t, ok)
	require.Equal(t, governance.ProposalStatus_PROPOSAL_STATUS_EXECUTION_FAILED, updated.Status,
		"at and after the activation height, even a treasury-spend proposal's execution failure must use the new distinct status, not silently PASSED")

	var sawRefund bool
	for _, c := range mockBank.SendCalls {
		if c.RecipientAddr.Equals(depositor) {
			sawRefund = true
		}
	}
	require.True(t, sawRefund, "deposit must still refund even when execution fails")
}

var _ baseapp.MessageRouter // referenced only to confirm the import resolves
